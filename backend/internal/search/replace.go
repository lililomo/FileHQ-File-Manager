package search

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"
	"unicode/utf8"

	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/tasks"
	"kppbwi.app/filemanager/internal/versions"
)

// PreviewPayload is POST /search/replace/preview (task replace_preview).
type PreviewPayload struct {
	ContentQuery
	Replace string `json:"replace"`
}

// Hunk is one changed line.
type Hunk struct {
	Line   int    `json:"line"`
	Before string `json:"before"`
	After  string `json:"after"`
}

// PreviewFile is the preview of one file.
type PreviewFile struct {
	Path         string `json:"path"`
	Replacements int    `json:"replacements"`
	Hunks        []Hunk `json:"hunks,omitempty"`
	// HunksTotal counts every changed line (Hunks holds at most
	// PreviewHunksPerFil of them; the task result shows the first
	// PreviewFullFiles files in full).
	HunksTotal int    `json:"hunksTotal"`
	SHA256     string `json:"sha256,omitempty"`
	Skipped    string `json:"skipped,omitempty"` // encoding | too_large | binary | unreadable
}

// PreviewResult is the task result of replace_preview.
type PreviewResult struct {
	PreviewID         string        `json:"previewId"`
	Files             []PreviewFile `json:"files"`
	TotalFiles        int           `json:"totalFiles"`
	TotalReplacements int           `json:"totalReplacements"`
	Truncated         bool          `json:"truncated"`
	Engine            string        `json:"engine"`
	ExpiresAt         string        `json:"expiresAt"`
}

// previewDoc is what tmp/sr-<task>/preview.json holds.
type previewDoc struct {
	UserID  string         `json:"userId"`
	Query   ContentQuery   `json:"query"`
	Replace string         `json:"replace"`
	Files   []PreviewFile  `json:"files"`
	Expires time.Time      `json:"expires"`
	Extra   map[string]any `json:"extra,omitempty"`
}

type previewMeta struct {
	userID  string
	expires time.Time
}

// ApplyPayload is POST /search/replace (task replace_apply).
type ApplyPayload struct {
	PreviewID string   `json:"previewId"`
	Files     []string `json:"files,omitempty"`
}

// ApplyFile is the outcome for one file.
type ApplyFile struct {
	Path         string `json:"path"`
	Status       string `json:"status"` // ok | skipped
	Reason       string `json:"reason,omitempty"`
	Replacements int    `json:"replacements"`
	VersionID    string `json:"versionId,omitempty"`
}

// ApplyResult is the task result of replace_apply.
type ApplyResult struct {
	Files        []ApplyFile `json:"files"`
	Replacements int         `json:"replacements"`
	VersionIDs   []string    `json:"versionIds"`
	Skipped      int         `json:"skipped"`
}

const previewDirPrefix = "sr-"

// Preview validates the request and queues the preview task.
func (s *Service) Preview(ctx context.Context, j *fsjail.Jail, userID string, p PreviewPayload) (tasks.Task, error) {
	if s.o.Tasks == nil {
		return tasks.Task{}, newErr(CodeInternal, "task engine unavailable", http.StatusServiceUnavailable, nil)
	}
	s.CleanPreviews()
	if err := s.normalizeContent(&p.ContentQuery); err != nil {
		return tasks.Task{}, err
	}
	if _, err := compile(p.ContentQuery); err != nil {
		return tasks.Task{}, err
	}
	if len(p.Replace) > MaxQueryLen {
		return tasks.Task{}, validationErr("replacement too long", map[string]any{"replace": "max 512"})
	}
	if err := scopeFolder(j, fsjail.VPath(p.Path)); err != nil {
		return tasks.Task{}, err
	}
	title := "Pratinjau ganti \"" + p.Q + "\" di " + p.Path
	return s.o.Tasks.Enqueue(ctx, tasks.EnqueueRequest{UserID: userID, Type: TaskPreview, Title: title, Payload: p})
}

// Apply queues the replace task for a stored preview of the user.
func (s *Service) Apply(ctx context.Context, userID string, p ApplyPayload) (tasks.Task, error) {
	if s.o.Tasks == nil {
		return tasks.Task{}, newErr(CodeInternal, "task engine unavailable", http.StatusServiceUnavailable, nil)
	}
	if p.PreviewID == "" {
		return tasks.Task{}, validationErr("previewId is required", map[string]any{"previewId": "required"})
	}
	doc, err := s.loadPreview(userID, p.PreviewID)
	if err != nil {
		return tasks.Task{}, err
	}
	known := map[string]bool{}
	for _, f := range doc.Files {
		if f.Skipped == "" {
			known[f.Path] = true
		}
	}
	for _, f := range p.Files {
		if !known[f] {
			return tasks.Task{}, validationErr("file is not part of the preview", map[string]any{"files": f})
		}
	}
	n := len(p.Files)
	if n == 0 {
		n = len(known)
	}
	title := "Ganti \"" + doc.Query.Q + "\" di " + itoa(n) + " file"
	return s.o.Tasks.Enqueue(ctx, tasks.EnqueueRequest{UserID: userID, Type: TaskApply, Title: title, Payload: p, Locks: p.Files})
}

func itoa(n int) string { return strconv.Itoa(n) }

// --- preview storage (DATA_DIR/tmp/sr-<task>/preview.json) ---

func (s *Service) tmp() (*fsjail.SysDir, error) { return fsjail.OpenSys(s.o.DataDir, "tmp") }

func (s *Service) savePreview(id string, doc *previewDoc) error {
	sys, err := s.tmp()
	if err != nil {
		return err
	}
	defer func() { _ = sys.Close() }()
	dir := previewDirPrefix + id
	if err := sys.MkdirAll(dir); err != nil {
		return err
	}
	f, err := sys.Create(dir + "/preview.json")
	if err != nil {
		return err
	}
	enc := json.NewEncoder(f)
	if err := enc.Encode(doc); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	s.previewMu.Lock()
	s.previews[id] = &previewMeta{userID: doc.UserID, expires: doc.Expires}
	s.previewMu.Unlock()
	return nil
}

func (s *Service) loadPreview(userID, id string) (*previewDoc, error) {
	s.previewMu.Lock()
	meta := s.previews[id]
	s.previewMu.Unlock()
	if meta == nil || meta.userID != userID || !meta.expires.After(s.o.Now()) {
		return nil, ErrPreviewExpired
	}
	sys, err := s.tmp()
	if err != nil {
		return nil, err
	}
	defer func() { _ = sys.Close() }()
	f, err := sys.Open(previewDirPrefix + id + "/preview.json")
	if err != nil {
		return nil, ErrPreviewExpired
	}
	defer func() { _ = f.Close() }()
	var doc previewDoc
	if err := json.NewDecoder(f).Decode(&doc); err != nil {
		return nil, ErrPreviewExpired
	}
	if doc.UserID != userID {
		return nil, ErrPreviewExpired
	}
	return &doc, nil
}

func (s *Service) dropPreview(id string) {
	s.previewMu.Lock()
	delete(s.previews, id)
	s.previewMu.Unlock()
	if sys, err := s.tmp(); err == nil {
		_ = sys.RemoveAll(previewDirPrefix + id)
		_ = sys.Close()
	}
}

// CleanPreviews drops expired previews (scheduler / tests).
func (s *Service) CleanPreviews() int {
	now := s.o.Now()
	var gone []string
	s.previewMu.Lock()
	for id, m := range s.previews {
		if !m.expires.After(now) {
			gone = append(gone, id)
		}
	}
	s.previewMu.Unlock()
	for _, id := range gone {
		s.dropPreview(id)
	}
	return len(gone)
}

// --- the replacement itself ---

// replaceLines applies the pattern line by line (ripgrep/grep are line
// oriented, so a multi-line match is never reported and never replaced).
// Line endings are kept as found (LF / CRLF per line).
func replaceLines(content []byte, pat *pattern, replacement string) (out []byte, hunks []Hunk, total, replacements int) {
	var buf bytes.Buffer
	buf.Grow(len(content))
	rest := content
	no := 0
	for len(rest) > 0 {
		no++
		i := bytes.IndexByte(rest, '\n')
		var line, eol []byte
		if i < 0 {
			line, rest = rest, nil
		} else {
			line, rest = rest[:i], rest[i+1:]
			eol = []byte("\n")
		}
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
			eol = append([]byte("\r"), eol...)
		}
		ls := string(line)
		n := len(pat.re.FindAllStringIndex(ls, -1))
		if n > 0 {
			var ns string
			if pat.q.Regex {
				ns = pat.re.ReplaceAllString(ls, replacement)
			} else {
				ns = pat.re.ReplaceAllLiteralString(ls, replacement)
			}
			if ns != ls {
				total++
				replacements += n
				if len(hunks) < PreviewHunksPerFil {
					hunks = append(hunks, Hunk{Line: no, Before: clip(ls), After: clip(ns)})
				}
			}
			buf.WriteString(ns)
		} else {
			buf.Write(line)
		}
		buf.Write(eol)
	}
	return buf.Bytes(), hunks, total, replacements
}

func sha(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// readText reads a candidate file; skipped names the reason when it cannot
// be replaced safely (binary, invalid UTF-8, too large).
func (s *Service) readText(j *fsjail.Jail, vp fsjail.VPath, maxSize int64) (content []byte, info fsjail.Info, skipped string, err error) {
	info, err = j.Lstat(vp)
	if err != nil {
		return nil, info, "unreadable", nil
	}
	if info.IsDir || info.IsSymlink {
		return nil, info, "unreadable", nil
	}
	if info.Size > maxSize {
		return nil, info, "too_large", nil
	}
	f, err := j.Open(vp)
	if err != nil {
		return nil, info, "unreadable", nil
	}
	defer func() { _ = f.Close() }()
	content, err = io.ReadAll(io.LimitReader(f, maxSize+1))
	if err != nil || int64(len(content)) > maxSize {
		return nil, info, "unreadable", nil
	}
	n := len(content)
	if n > BinaryProbeBytes {
		n = BinaryProbeBytes
	}
	if isBinary(content[:n]) {
		return nil, info, "binary", nil
	}
	if !utf8.Valid(content) {
		// Step 16a brings the decoder; until then only UTF-8/ASCII is rewritten
		return nil, info, "encoding", nil
	}
	return content, info, "", nil
}

// runPreview collects the matching files with the content engine and
// computes the replacement in memory.
func (s *Service) runPreview(ctx context.Context, rt *tasks.Runtime) (any, error) {
	var p PreviewPayload
	if err := json.Unmarshal(rt.Task.Payload, &p); err != nil {
		return nil, validationErr("bad payload", map[string]any{"payload": "invalid"})
	}
	if err := s.normalizeContent(&p.ContentQuery); err != nil {
		return nil, err
	}
	pat, err := compile(p.ContentQuery)
	if err != nil {
		return nil, err
	}
	j, release, err := rt.Jail(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := scopeFolder(j, fsjail.VPath(p.Path)); err != nil {
		return nil, err
	}
	// 1. find the files (same engine as the search; matches themselves are
	// recomputed per line below, so only the file set matters here)
	sctx, cancel := context.WithTimeout(ctx, s.o.ContentTimeout)
	defer cancel()
	em := &emitter{j: j, q: &p.ContentQuery, emit: func(Match) error { return nil }, stat: map[string]bool{}, files: map[string]bool{}, cancel: cancel}
	engine := s.Engine()
	var rerr error
	if engine == "rg" {
		rerr = s.runRg(sctx, j, rt.Task.UserID, pat, em)
	} else {
		rerr = s.runFallback(sctx, j, pat, em)
	}
	if rerr != nil && !em.stopped {
		return nil, rerr
	}
	paths := make([]string, 0, len(em.files))
	for f := range em.files {
		paths = append(paths, f)
	}
	sort.Strings(paths)
	rt.SetTotal(int64(len(paths)))
	rt.Log("info", "%d file cocok (engine %s)", len(paths), engine)
	// 2. compute replacements
	doc := &previewDoc{UserID: rt.Task.UserID, Query: p.ContentQuery, Replace: p.Replace, Expires: s.o.Now().Add(PreviewTTL)}
	res := PreviewResult{PreviewID: rt.Task.ID, Files: []PreviewFile{}, Truncated: em.truncated, Engine: engine, ExpiresAt: doc.Expires.UTC().Format(time.RFC3339)}
	for i, path := range paths {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		vp := fsjail.VPath(path)
		content, _, skipped, err := s.readText(j, vp, p.MaxSize)
		if err != nil {
			return nil, err
		}
		pf := PreviewFile{Path: path}
		if skipped != "" {
			pf.Skipped = skipped
			rt.Log("warn", "%s dilewati: %s", path, skipped)
		} else {
			_, hunks, total, n := replaceLines(content, pat, p.Replace)
			if n == 0 {
				rt.Progress(int64(i+1), int64(len(paths)))
				continue // matches only in lines the replacement leaves unchanged
			}
			pf.Hunks, pf.HunksTotal, pf.Replacements, pf.SHA256 = hunks, total, n, sha(content)
			res.TotalReplacements += n
		}
		doc.Files = append(doc.Files, pf)
		rt.Progress(int64(i+1), int64(len(paths)))
	}
	if err := s.savePreview(rt.Task.ID, doc); err != nil {
		return nil, err
	}
	res.TotalFiles = len(doc.Files)
	for i, f := range doc.Files {
		out := f
		out.SHA256 = ""
		if i >= PreviewFullFiles {
			out.Hunks = nil
		}
		res.Files = append(res.Files, out)
	}
	rt.Log("info", "%d penggantian di %d file; pratinjau berlaku sampai %s", res.TotalReplacements, res.TotalFiles, res.ExpiresAt)
	return res, nil
}

// runApply rewrites the selected files of a preview: write lock, re-read,
// hash check, pre-image version, atomic write, quota, activity, per-file
// log; files that changed since the preview are skipped with a warning.
func (s *Service) runApply(ctx context.Context, rt *tasks.Runtime) (any, error) {
	var p ApplyPayload
	if err := json.Unmarshal(rt.Task.Payload, &p); err != nil {
		return nil, validationErr("bad payload", map[string]any{"payload": "invalid"})
	}
	doc, err := s.loadPreview(rt.Task.UserID, p.PreviewID)
	if err != nil {
		return nil, err
	}
	pat, err := compile(doc.Query)
	if err != nil {
		return nil, err
	}
	want := map[string]bool{}
	for _, f := range p.Files {
		want[f] = true
	}
	var targets []PreviewFile
	for _, f := range doc.Files {
		if f.Skipped != "" {
			continue
		}
		if len(want) == 0 || want[f.Path] {
			targets = append(targets, f)
		}
	}
	j, release, err := rt.Jail(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	res := ApplyResult{Files: []ApplyFile{}, VersionIDs: []string{}}
	rt.SetTotal(int64(len(targets)))
	for i, t := range targets {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		out := s.applyOne(ctx, rt, j, pat, doc.Replace, t)
		res.Files = append(res.Files, out)
		if out.Status == "ok" {
			res.Replacements += out.Replacements
			if out.VersionID != "" {
				res.VersionIDs = append(res.VersionIDs, out.VersionID)
			}
			rt.Log("info", "%s: %d penggantian (versi %s)", t.Path, out.Replacements, orDash(out.VersionID))
		} else {
			res.Skipped++
			rt.Log("warn", "%s dilewati: %s", t.Path, out.Reason)
		}
		rt.Progress(int64(i+1), int64(len(targets)))
	}
	s.dropPreview(p.PreviewID)
	s.InvalidateNames(rt.Task.UserID)
	return res, nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func (s *Service) applyOne(ctx context.Context, rt *tasks.Runtime, j *fsjail.Jail, pat *pattern, replacement string, t PreviewFile) ApplyFile {
	out := ApplyFile{Path: t.Path, Status: "skipped"}
	vp := fsjail.VPath(t.Path)
	if s.o.Locks != nil {
		release, err := s.o.Locks.TryAcquire(rt.Task.UserID, fsjail.Write, "replace:"+rt.Task.ID, vp)
		if err != nil {
			out.Reason = "locked"
			return out
		}
		defer release()
	}
	content, info, skipped, err := s.readText(j, vp, s.o.MaxFileSize)
	if err != nil || skipped != "" {
		out.Reason = skipped
		if out.Reason == "" {
			out.Reason = "unreadable"
		}
		return out
	}
	if sha(content) != t.SHA256 {
		out.Reason = "changed"
		return out
	}
	newContent, _, _, n := replaceLines(content, pat, replacement)
	if n == 0 || bytes.Equal(newContent, content) {
		out.Reason = "no_change"
		return out
	}
	if s.o.Versions != nil && s.o.Versions.Enabled() {
		v, _, err := s.o.Versions.Snapshot(ctx, j, rt.Task.UserID, vp, versions.ReasonReplaceInFiles)
		if err != nil {
			out.Reason = "version_failed"
			s.logger.WarnContext(ctx, "pre-image version failed", slog.String("path", t.Path), slog.String("error", err.Error()))
			return out
		}
		if v != nil {
			out.VersionID = v.ID
		}
	}
	if s.o.Quota != nil && int64(len(newContent)) > info.Size {
		resv, err := s.o.Quota.Reserve(ctx, j, rt.Task.UserID, int64(len(newContent))-info.Size)
		if err != nil {
			out.Reason = "quota"
			return out
		}
		resv.Release()
	}
	written, err := files.WriteAtomic(j, vp, bytes.NewReader(newContent), info.Mode.Perm())
	if err != nil {
		out.Reason = "write_failed"
		s.logger.WarnContext(ctx, "replace write failed", slog.String("path", t.Path), slog.String("error", err.Error()))
		return out
	}
	if s.o.Quota != nil {
		if err := s.o.Quota.Apply(ctx, nil, rt.Task.UserID, written-info.Size, 0, 0); err != nil {
			s.logger.WarnContext(ctx, "quota apply failed", slog.String("error", err.Error()))
		}
	}
	if s.o.Files != nil {
		s.o.Files.RecordActivity(ctx, rt.Task.UserID, files.ActReplace, t.Path, "", map[string]any{"replacements": n, "task": rt.Task.ID, "versionId": out.VersionID})
	}
	if s.o.Invalidate != nil {
		s.o.Invalidate(rt.Task.UserID, vp.Dir())
	}
	if s.o.OnChange != nil {
		s.o.OnChange(ctx, rt.Task.UserID, vp, false)
	}
	out.Status, out.Replacements = "ok", n
	return out
}
