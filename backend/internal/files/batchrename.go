package files

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/fsjail"
)

// MaxRegexLength bounds the find pattern (RE2 is linear-time; this only
// keeps error messages readable).
const MaxRegexLength = 256

// Numbering inserts a counter (PROMPT Step 9a task 6, SPEC §8.5).
type Numbering struct {
	Pattern string `json:"pattern"` // tokens: {n} {name} {ext}; default "{name} {n}"
	Start   int    `json:"start"`   // default 1
	Step    int    `json:"step"`    // default 1
	Pad     int    `json:"pad"`     // zero padding width, default 0
}

// RenameRule is the batch rename rule.
type RenameRule struct {
	Find            string     `json:"find,omitempty"`
	Replace         string     `json:"replace,omitempty"`
	Regex           bool       `json:"regex,omitempty"`
	CaseInsensitive bool       `json:"case_insensitive,omitempty"`
	Numbering       *Numbering `json:"numbering,omitempty"`
	Case            string     `json:"case,omitempty"`     // upper | lower | title | none
	ExtMode         string     `json:"ext_mode,omitempty"` // keep (transform the stem, keep extension) | include (transform the whole name)
}

// PreviewItem is one row of the batch rename preview.
type PreviewItem struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Status string `json:"status"` // ok | collision | invalid
	Reason string `json:"reason,omitempty"`
}

// compiledRule is a validated rule.
type compiledRule struct {
	rule RenameRule
	re   *regexp.Regexp
	fold func(string) string
}

var titleCaser = cases.Title(language.Und)

func compileRule(rule RenameRule) (*compiledRule, error) {
	c := &compiledRule{rule: rule}
	switch rule.Case {
	case "", "none":
	case "upper":
		c.fold = strings.ToUpper
	case "lower":
		c.fold = strings.ToLower
	case "title":
		c.fold = func(s string) string { return titleCaser.String(strings.ToLower(s)) }
	default:
		return nil, &ValidationError{Reason: "case", Fields: map[string]string{"rule.case": "must be upper, lower, title or none"}}
	}
	switch rule.ExtMode {
	case "", "keep", "include":
	default:
		return nil, &ValidationError{Reason: "ext_mode", Fields: map[string]string{"rule.ext_mode": "must be keep or include"}}
	}
	if rule.Find != "" {
		if len(rule.Find) > MaxRegexLength {
			return nil, &ValidationError{Reason: "find", Fields: map[string]string{"rule.find": "too_long"}}
		}
		pattern := rule.Find
		if !rule.Regex {
			pattern = regexp.QuoteMeta(pattern)
		}
		if rule.CaseInsensitive {
			pattern = "(?i)" + pattern
		}
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, &ValidationError{Reason: "regex", Fields: map[string]string{"rule.find": "invalid regex: " + err.Error()}}
		}
		c.re = re
	}
	if n := rule.Numbering; n != nil {
		if n.Pattern == "" {
			n.Pattern = "{name} {n}"
		}
		if !strings.Contains(n.Pattern, "{n}") {
			return nil, &ValidationError{Reason: "numbering", Fields: map[string]string{"rule.numbering.pattern": "must contain {n}"}}
		}
		if n.Step == 0 {
			n.Step = 1
		}
		if n.Start == 0 && n.Step > 0 {
			n.Start = 1
		}
		if n.Pad < 0 || n.Pad > 12 {
			return nil, &ValidationError{Reason: "numbering", Fields: map[string]string{"rule.numbering.pad": "0..12"}}
		}
	}
	return c, nil
}

// apply computes the new name of the i-th item.
func (c *compiledRule) apply(name string, isDir bool, i int) string {
	stem, ext := name, ""
	if c.rule.ExtMode != "include" {
		stem, ext = SplitExt(name, isDir)
	}
	if c.re != nil {
		repl := c.rule.Replace
		if !c.rule.Regex {
			// literal replacement: $ must not expand
			repl = strings.ReplaceAll(repl, "$", "$$")
		}
		stem = c.re.ReplaceAllString(stem, repl)
	}
	if c.fold != nil {
		stem = c.fold(stem)
	}
	if n := c.rule.Numbering; n != nil {
		num := strconv.Itoa(n.Start + i*n.Step)
		if n.Pad > 0 && len(num) < n.Pad {
			num = strings.Repeat("0", n.Pad-len(num)) + num
		}
		r := strings.NewReplacer("{n}", num, "{name}", stem, "{ext}", strings.TrimPrefix(ext, "."))
		stem = r.Replace(n.Pattern)
	}
	return stem + ext
}

// BatchRenamePreview computes the new names without touching the
// filesystem (PROMPT Step 9a task 6). Collisions are detected among the
// results and against entries of the folder that are not being renamed.
func (s *Service) BatchRenamePreview(ctx context.Context, j *fsjail.Jail, paths []fsjail.VPath, rule RenameRule) ([]PreviewItem, error) {
	if len(paths) == 0 {
		return nil, &ValidationError{Reason: "empty", Fields: map[string]string{"paths": "required"}}
	}
	if len(paths) > MaxBatchRenameItems {
		return nil, &TooLargeError{Entries: len(paths), MaxEntries: MaxBatchRenameItems, Reason: "entries"}
	}
	c, err := compileRule(rule)
	if err != nil {
		return nil, err
	}
	paths = dedupe(paths)
	items := make([]PreviewItem, 0, len(paths))
	selected := map[fsjail.VPath]struct{}{}
	for _, p := range paths {
		selected[p] = struct{}{}
	}
	targets := map[fsjail.VPath]int{} // new path → index of first item producing it
	for i, p := range paths {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		it := PreviewItem{From: string(p), Status: "ok"}
		if p.IsRoot() {
			it.Status, it.Reason = "invalid", "root"
			items = append(items, it)
			continue
		}
		info, err := j.Lstat(p)
		if err != nil {
			if isNotFound(err) {
				it.Status, it.Reason = "invalid", "not_found"
				items = append(items, it)
				continue
			}
			return nil, err
		}
		newName := c.apply(p.Base(), info.IsDir, i)
		it.To = newName
		if _, verr := fsjail.ValidateName(newName); verr != nil {
			it.Status, it.Reason = "invalid", reasonOf(verr)
			items = append(items, it)
			continue
		}
		if !info.IsDir && s.Blocked(newName) {
			it.Status, it.Reason = "invalid", "blocked_extension"
			items = append(items, it)
			continue
		}
		newPath, err := fsjail.Join(p.Dir(), newName)
		if err != nil {
			it.Status, it.Reason = "invalid", reasonOf(err)
			items = append(items, it)
			continue
		}
		it.To = string(newPath)
		if first, dup := targets[newPath]; dup {
			it.Status, it.Reason = "collision", "duplicate_result"
			if items[first].Status == "ok" {
				items[first].Status, items[first].Reason = "collision", "duplicate_result"
			}
			items = append(items, it)
			continue
		}
		targets[newPath] = len(items)
		if newPath == p {
			items = append(items, it) // unchanged: ok, skipped on apply
			continue
		}
		if _, isSelected := selected[newPath]; !isSelected {
			if _, err := j.Lstat(newPath); err == nil {
				it.Status, it.Reason = "collision", "exists"
			} else if !isNotFound(err) {
				return nil, err
			}
		}
		items = append(items, it)
	}
	return items, nil
}

func reasonOf(err error) string {
	var fe *fsjail.Error
	if errors.As(err, &fe) && fe.Reason != "" {
		return fe.Reason
	}
	return "invalid"
}

type renamePair struct {
	from, to fsjail.VPath
}

// BatchRename recomputes the preview and applies it when it is clean
// (PROMPT: the server never trusts the client's preview). Renames run in
// two phases through temporary names so chains (a→b, b→a) cannot collide;
// a failure rolls every item back to its original name.
func (s *Service) BatchRename(ctx context.Context, j *fsjail.Jail, userID string, paths []fsjail.VPath, rule RenameRule) (*Result, []PreviewItem, error) {
	preview, err := s.BatchRenamePreview(ctx, j, paths, rule)
	if err != nil {
		return nil, nil, err
	}
	var pairs []renamePair
	for _, it := range preview {
		if it.Status != "ok" {
			return nil, preview, &BatchRenameError{Items: preview}
		}
		if it.To == it.From {
			continue
		}
		pairs = append(pairs, renamePair{from: fsjail.VPath(it.From), to: fsjail.VPath(it.To)})
	}
	res, err := s.applyPairs(ctx, j, userID, pairs, true)
	return res, preview, err
}

// applyPairs executes rename pairs atomically-ish (two-phase with rollback).
func (s *Service) applyPairs(ctx context.Context, j *fsjail.Jail, userID string, pairs []renamePair, recordUndo bool) (*Result, error) {
	res := &Result{Results: []ItemResult{}}
	if len(pairs) == 0 {
		return res, nil
	}
	lockPaths := make([]fsjail.VPath, 0, 2*len(pairs))
	for _, p := range pairs {
		lockPaths = append(lockPaths, p.from, p.to)
	}
	release, err := s.lock(ctx, j, userID, "batch_rename", fsjail.Write, lockPaths...)
	if err != nil {
		return nil, err
	}
	defer release()
	s.hook("batch_rename")
	// Re-check the state under the lock (preview ran unlocked).
	fromSet := map[fsjail.VPath]struct{}{}
	for _, p := range pairs {
		fromSet[p.from] = struct{}{}
	}
	for _, p := range pairs {
		if _, err := j.Lstat(p.from); err != nil {
			return nil, err
		}
		if _, isFrom := fromSet[p.to]; isFrom {
			continue
		}
		if existing, err := j.Lstat(p.to); err == nil {
			return nil, &ConflictError{Conflicts: []Conflict{{Path: string(p.to), Source: string(p.from), Existing: statOf(existing)}}}
		} else if !isNotFound(err) {
			return nil, err
		}
	}
	batchID := db.NewID()
	tmpNames := make([]fsjail.VPath, len(pairs))
	// Phase 1: every source to a temporary name in its own folder.
	for i, p := range pairs {
		tmp := fsjail.VPath(string(p.from.Dir()) + "/." + batchID[:8] + "-" + strconv.Itoa(i) + ".fmtmp")
		if p.from.Dir().IsRoot() {
			tmp = fsjail.VPath("/." + batchID[:8] + "-" + strconv.Itoa(i) + ".fmtmp")
		}
		if err := j.Rename(p.from, tmp); err != nil {
			s.rollbackPairs(ctx, j, pairs, tmpNames, i)
			return nil, err
		}
		tmpNames[i] = tmp
	}
	// Phase 2: temporary names to final names.
	for i, p := range pairs {
		if err := j.Rename(tmpNames[i], p.to); err != nil {
			// undo phase 2 so far, then phase 1
			for k := 0; k < i; k++ {
				_ = j.Rename(pairs[k].to, tmpNames[k])
			}
			s.rollbackPairs(ctx, j, pairs, tmpNames, len(pairs))
			return nil, err
		}
	}
	action := ActBatchRename
	if !recordUndo {
		action = ActUndo
	}
	if err := s.repo.WithTx(ctx, func(tx db.Repository) error {
		// Two phases in the DB as well: old → temp, temp → new.
		for i, p := range pairs {
			if err := UpdateReferences(ctx, tx, userID, string(j.Abs(p.from)), string(j.Abs(tmpNames[i]))); err != nil {
				return err
			}
		}
		for i, p := range pairs {
			if err := UpdateReferences(ctx, tx, userID, string(j.Abs(tmpNames[i])), string(j.Abs(p.to))); err != nil {
				return err
			}
		}
		for _, p := range pairs {
			s.logActivity(ctx, tx, userID, action, string(j.Abs(p.from)), string(j.Abs(p.to)), map[string]any{"op": ActBatchRename}, batchID)
		}
		return nil
	}); err != nil {
		s.logger.ErrorContext(ctx, "batch rename references failed; rolling back", slog.String("error", err.Error()))
		for k := range pairs {
			_ = j.Rename(pairs[k].to, tmpNames[k])
		}
		s.rollbackPairs(ctx, j, pairs, tmpNames, len(pairs))
		return nil, fmt.Errorf("files: batch rename references: %w", err)
	}
	var undoItems []UndoItem
	for _, p := range pairs {
		info, _ := j.Lstat(p.to)
		s.changed(ctx, j, userID, p.from, info.IsDir)
		s.invalidateDir(userID, p.to.Dir())
		res.Results = append(res.Results, ItemResult{Path: string(p.from), To: string(p.to), Status: "ok", IsDir: info.IsDir})
		undoItems = append(undoItems, UndoItem{From: string(p.to), To: string(p.from)})
	}
	res.BatchID = batchID
	if recordUndo {
		s.withUndo(ctx, j, res, userID, UndoInfo{Kind: "batch_rename", BatchID: batchID, Items: undoItems})
	}
	return res, nil
}

// rollbackPairs renames the first n temporaries back to their sources.
func (s *Service) rollbackPairs(ctx context.Context, j *fsjail.Jail, pairs []renamePair, tmp []fsjail.VPath, n int) {
	for k := 0; k < n; k++ {
		if tmp[k] == "" {
			continue
		}
		if err := j.Rename(tmp[k], pairs[k].from); err != nil {
			s.logger.ErrorContext(ctx, "batch rename rollback failed", slog.String("tmp", string(tmp[k])), slog.String("path", string(pairs[k].from)), slog.String("error", err.Error()))
		}
	}
}
