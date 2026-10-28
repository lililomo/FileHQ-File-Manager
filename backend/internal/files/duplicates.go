package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/tasks"
)

// Duplicate finder (SPEC §7.1 duplicate_finder, §8.7 "Duplikat", PROMPT
// Step 23c): size → partial digest (first + last 64 KiB) → full SHA-256.
const (
	TaskDuplicates = "duplicate_finder"
	// DuplicateDeadline bounds one scan; a longer scan fails with TIMEOUT.
	DuplicateDeadline = time.Hour
	// DuplicateResultTTL is how long GET /me/duplicates serves a result.
	DuplicateResultTTL = 24 * time.Hour
	// DuplicateMaxGroups caps the stored result (largest reclaimable first).
	DuplicateMaxGroups = 5000
	// dupEdge is the size of the head and tail hashed in the partial phase;
	// files up to 2*dupEdge are hashed fully right away.
	dupEdge = 64 << 10
)

// ShareLookup reports the owner's shares relevant to a subtree (the share
// package implements it; files cannot import share).
type ShareLookup interface {
	SharedPathsRelated(ctx context.Context, ownerID string, root fsjail.VPath) ([]string, error)
}

// DuplicatesPayload is the payload of duplicate_finder tasks.
type DuplicatesPayload struct {
	Path string `json:"path"`
}

// DuplicateFile is one copy inside a group. Shared: the file or one of its
// folders has a share row (any status).
type DuplicateFile struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Mtime  string `json:"mtime"`
	Shared bool   `json:"shared"`
	mtime  time.Time
}

// DuplicateGroup is a set of distinct files (distinct inodes) with the same
// content; Paths[0] is the default "keep" candidate (oldest, shortest path).
type DuplicateGroup struct {
	Size        int64           `json:"size"`
	Hash        string          `json:"hash"`
	Reclaimable int64           `json:"reclaimable"`
	Paths       []DuplicateFile `json:"paths"`
}

// DuplicateReport is the task result and the body of GET /me/duplicates.
type DuplicateReport struct {
	Path        string           `json:"path"`
	Groups      []DuplicateGroup `json:"groups"`
	Reclaimable int64            `json:"reclaimable"`
	Scanned     int64            `json:"scanned"`
	Candidates  int64            `json:"candidates"`
	GeneratedAt string           `json:"generatedAt"`
	// Truncated: more than DuplicateMaxGroups groups were found; TotalGroups
	// counts all of them.
	Truncated   bool `json:"truncated"`
	TotalGroups int  `json:"totalGroups"`
}

// DuplicateTimeoutError: the scan exceeded its deadline (task error TIMEOUT).
type DuplicateTimeoutError struct {
	Limit   time.Duration
	Phase   string
	Scanned int64
	Hashed  int64
	Total   int64
}

func (e *DuplicateTimeoutError) Error() string {
	return fmt.Sprintf("files: duplicate scan exceeded %s in phase %s (%d files scanned, %d of %d hashed)", e.Limit, e.Phase, e.Scanned, e.Hashed, e.Total)
}

// Unwrap keeps errors.Is(err, context.DeadlineExceeded) true.
func (e *DuplicateTimeoutError) Unwrap() error { return context.DeadlineExceeded }

// TaskCode maps the error to the stored task error (SPEC §7.2 failed).
func (e *DuplicateTimeoutError) TaskCode() (string, string) {
	return "TIMEOUT", fmt.Sprintf("timeout: stopped after %s (%d files scanned, %d of %d hashed)", e.Limit, e.Scanned, e.Hashed, e.Total)
}

// isStagingName reports the temporary names of uploads, copies, archives
// and atomic writes (".fm-upload-…", "x.fm-partial-…", ".x.fm-tmp-…").
func isStagingName(name string) bool {
	return strings.HasPrefix(name, ".fm-") || strings.Contains(name, ".fm-partial-") || IsTempName(name)
}

type dupCand struct {
	path  fsjail.VPath
	size  int64
	mtime time.Time
	dev   uint64
	ino   uint64
	hash  string // hex SHA-256 once grouped
}

func dupLess(a, b dupCand) bool {
	if !a.mtime.Equal(b.mtime) {
		return a.mtime.Before(b.mtime)
	}
	if len(a.path) != len(b.path) {
		return len(a.path) < len(b.path)
	}
	return a.path < b.path
}

type dupScan struct {
	phase                  string
	scanned, hashed, total int64
	rep                    Reporter
}

func (d *dupScan) log(level, format string, args ...any) {
	if d.rep != nil {
		d.rep.Log(level, format, args...)
	}
}

func (d *dupScan) add(n int64) {
	d.hashed += n
	if d.rep != nil && n > 0 {
		d.rep.Add(n)
	}
}

func (s *Service) duplicateDeadline() time.Duration {
	if s.DuplicateDeadline > 0 {
		return s.DuplicateDeadline
	}
	return DuplicateDeadline
}

// FindDuplicates scans the folder root of j for files with identical
// content (SPEC §8.7 "Duplikat"). Symlinks, empty files, staging names and
// entries that cannot be statted are skipped; hard links of one inode are
// listed once. The scan honours ctx and fails with DuplicateTimeoutError
// after DuplicateDeadline.
func (s *Service) FindDuplicates(ctx context.Context, j *fsjail.Jail, userID string, root fsjail.VPath) (*DuplicateReport, error) {
	info, err := j.Stat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir {
		return nil, &ValidationError{Reason: "not_a_directory", Path: string(root), Fields: map[string]string{"path": "not_a_directory"}}
	}
	limit := s.duplicateDeadline()
	dctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	st := &dupScan{phase: "walk", rep: reporterOf(ctx)}
	rep, err := s.findDuplicates(dctx, j, userID, root, st)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			te := &DuplicateTimeoutError{Limit: limit, Phase: st.phase, Scanned: st.scanned, Hashed: st.hashed, Total: st.total}
			st.log("error", "timeout after %s in phase %s: %d files scanned, %d of %d hashed", limit, st.phase, st.scanned, st.hashed, st.total)
			return nil, te
		}
		return nil, err
	}
	return rep, nil
}

func (s *Service) findDuplicates(ctx context.Context, j *fsjail.Jail, userID string, root fsjail.VPath, st *dupScan) (*DuplicateReport, error) {
	// 1. walk: regular, non-empty files grouped by size
	bySize := map[int64][]fsjail.VPath{}
	var skipped int64
	err := j.WalkDir(ctx, root, func(vp string, d fs.DirEntry, err error) error {
		p := fsjail.VPath(vp)
		if err != nil {
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
			skipped++ // unreadable folder or vanished entry
			return nil
		}
		if p == root {
			return nil
		}
		if isStagingName(p.Base()) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() || d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
			return nil
		}
		fi, ierr := d.Info()
		if ierr != nil || !fi.Mode().IsRegular() || fi.Size() == 0 {
			return nil
		}
		st.scanned++
		bySize[fi.Size()] = append(bySize[fi.Size()], p)
		return nil
	})
	if err != nil {
		return nil, err
	}
	// 2. candidates: same size, distinct inodes (hard links count once)
	st.phase = "stat"
	var groups [][]dupCand
	for size, paths := range bySize {
		if len(paths) < 2 {
			continue
		}
		byInode := map[[2]uint64]dupCand{}
		for _, p := range paths {
			li, lerr := j.Lstat(p)
			if lerr != nil || !li.IsRegular() || li.Size != size {
				continue
			}
			c := dupCand{path: p, size: size, mtime: li.ModTime, dev: li.Dev, ino: li.Ino}
			key := [2]uint64{li.Dev, li.Ino}
			if prev, ok := byInode[key]; !ok || dupLess(c, prev) {
				byInode[key] = c
			}
		}
		if len(byInode) < 2 {
			continue
		}
		g := make([]dupCand, 0, len(byInode))
		for _, c := range byInode {
			g = append(g, c)
		}
		groups = append(groups, g)
		st.total += int64(len(g))
	}
	if cerr := ctx.Err(); cerr != nil {
		return nil, cerr
	}
	if st.rep != nil {
		st.rep.SetTotal(st.total)
	}
	st.log("info", "walk done: %d files scanned, %d candidates in %d size groups (%d entries skipped)", st.scanned, st.total, len(groups), skipped)

	// 3. partial digest (full digest for small files)
	st.phase = "partial"
	var final [][]dupCand
	var needFull [][]dupCand
	for _, g := range groups {
		full := g[0].size <= 2*dupEdge
		byDigest := map[string][]dupCand{}
		for _, c := range g {
			if err := itemPause(ctx); err != nil {
				return nil, err
			}
			sum, herr := hashCandidate(ctx, j, c, !full)
			if herr != nil {
				if cerr := ctx.Err(); cerr != nil {
					return nil, cerr
				}
				st.log("warn", "skipped %s: %s", c.path, herr.Error())
				st.add(1)
				continue
			}
			if full {
				st.add(1)
			}
			byDigest[sum] = append(byDigest[sum], c)
		}
		for sum, sub := range byDigest {
			switch {
			case len(sub) < 2:
				if !full {
					st.add(int64(len(sub)))
				}
			case full:
				final = append(final, withHash(sub, sum))
			default:
				needFull = append(needFull, sub)
			}
		}
	}
	var fullCount int
	for _, g := range needFull {
		fullCount += len(g)
	}
	st.log("info", "partial digests done: %d files need a full hash", fullCount)

	// 4. full SHA-256 of the remaining candidates
	st.phase = "full"
	for _, g := range needFull {
		byDigest := map[string][]dupCand{}
		for _, c := range g {
			if err := itemPause(ctx); err != nil {
				return nil, err
			}
			sum, herr := hashCandidate(ctx, j, c, false)
			st.add(1)
			if herr != nil {
				if cerr := ctx.Err(); cerr != nil {
					return nil, cerr
				}
				st.log("warn", "skipped %s: %s", c.path, herr.Error())
				continue
			}
			byDigest[sum] = append(byDigest[sum], c)
		}
		for sum, sub := range byDigest {
			if len(sub) >= 2 {
				final = append(final, withHash(sub, sum))
			}
		}
	}

	// 5. shares, ordering, totals
	shared := map[fsjail.VPath]bool{}
	if s.Shares != nil {
		paths, serr := s.Shares.SharedPathsRelated(ctx, userID, j.Abs(root))
		if serr != nil {
			return nil, serr
		}
		for _, p := range paths {
			if vp, ok := j.RelOf(fsjail.VPath(p)); ok {
				shared[vp] = true
			}
		}
	}
	rep := &DuplicateReport{Path: string(root), Scanned: st.scanned, Candidates: st.total, GeneratedAt: s.now().UTC().Format("2006-01-02T15:04:05.000Z"), Groups: []DuplicateGroup{}}
	for _, g := range final {
		sort.Slice(g, func(a, b int) bool { return dupLess(g[a], g[b]) })
		dg := DuplicateGroup{Size: g[0].size, Hash: g[0].hash, Paths: make([]DuplicateFile, 0, len(g))}
		for _, c := range g {
			dg.Paths = append(dg.Paths, DuplicateFile{Path: string(c.path), Name: c.path.Base(), Size: c.size,
				Mtime: c.mtime.UTC().Format("2006-01-02T15:04:05.000Z"), Shared: isSharedPath(shared, c.path), mtime: c.mtime})
		}
		dg.Reclaimable = dg.Size * int64(len(dg.Paths)-1)
		rep.Groups = append(rep.Groups, dg)
	}
	sortGroups(rep.Groups)
	rep.TotalGroups = len(rep.Groups)
	if len(rep.Groups) > DuplicateMaxGroups {
		rep.Groups, rep.Truncated = rep.Groups[:DuplicateMaxGroups], true
	}
	for _, g := range rep.Groups {
		rep.Reclaimable += g.Reclaimable
	}
	st.log("info", "done: %d groups, %d bytes reclaimable", rep.TotalGroups, rep.Reclaimable)
	return rep, nil
}

func withHash(g []dupCand, sum string) []dupCand {
	for i := range g {
		g[i].hash = sum
	}
	return g
}

func sortGroups(gs []DuplicateGroup) {
	sort.SliceStable(gs, func(a, b int) bool {
		if gs[a].Reclaimable != gs[b].Reclaimable {
			return gs[a].Reclaimable > gs[b].Reclaimable
		}
		if gs[a].Size != gs[b].Size {
			return gs[a].Size > gs[b].Size
		}
		return gs[a].Paths[0].Path < gs[b].Paths[0].Path
	})
}

func isSharedPath(shared map[fsjail.VPath]bool, p fsjail.VPath) bool {
	if len(shared) == 0 {
		return false
	}
	for d := p; ; d = d.Dir() {
		if shared[d] {
			return true
		}
		if d.IsRoot() {
			return false
		}
	}
}

// hashCandidate returns the hex SHA-256 of the whole file, or of its first
// and last dupEdge bytes when partial. The file must still be a regular file
// of the walked size (a file changed since the walk is skipped, never
// grouped on stale data).
func hashCandidate(ctx context.Context, j *fsjail.Jail, c dupCand, partial bool) (string, error) {
	f, err := j.Open(c.path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !fi.Mode().IsRegular() || fi.Size() != c.size {
		return "", errors.New("changed during the scan")
	}
	h := sha256.New()
	if partial {
		buf := make([]byte, dupEdge)
		for _, off := range []int64{0, c.size - dupEdge} {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			if _, err := f.ReadAt(buf, off); err != nil && !errors.Is(err, io.EOF) {
				return "", err
			}
			_, _ = h.Write(buf)
		}
	} else if _, err := io.Copy(h, &ctxReader{ctx: ctx, r: f}); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *Service) runDuplicates(ctx context.Context, rt *tasks.Runtime) (any, error) {
	var p DuplicatesPayload
	if err := json.Unmarshal(rt.Task.Payload, &p); err != nil {
		return nil, &ValidationError{Reason: "bad_payload"}
	}
	if p.Path == "" {
		p.Path = "/"
	}
	vp, err := fsjail.Clean(p.Path)
	if err != nil {
		return nil, err
	}
	j, release, err := rt.Jail(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	return s.FindDuplicates(taskCtx(ctx, rt), j, rt.Task.UserID, vp)
}

// PruneDuplicateReport drops the copies that no longer exist as the same
// regular file (deleted, replaced by a symlink, resized) and the groups
// left with fewer than two copies, then recomputes the totals. GET
// /me/duplicates applies it so a reload never offers a removed path.
func PruneDuplicateReport(j *fsjail.Jail, rep *DuplicateReport) {
	groups := rep.Groups[:0]
	rep.Reclaimable = 0
	for _, g := range rep.Groups {
		kept := g.Paths[:0]
		for _, f := range g.Paths {
			vp, err := fsjail.Clean(f.Path)
			if err != nil {
				continue
			}
			li, err := j.Lstat(vp)
			if err != nil || !li.IsRegular() || li.Size != f.Size {
				continue
			}
			kept = append(kept, f)
		}
		if len(kept) < 2 {
			continue
		}
		g.Paths = kept
		g.Reclaimable = g.Size * int64(len(kept)-1)
		rep.Reclaimable += g.Reclaimable
		groups = append(groups, g)
	}
	rep.Groups = groups
}

// DuplicateLinkMaxBytes bounds the bytes re-hashed synchronously by
// ReplaceWithSymlinks (keep + every path); larger requests answer
// FileTooLargeError (413 FILE_TOO_LARGE).
const DuplicateLinkMaxBytes int64 = 2 << 30

// DuplicateLinkMaxItems bounds paths per "replace with symlink" request.
const DuplicateLinkMaxItems = 200

// LinkGroup is one "keep this file, link these copies to it" request.
type LinkGroup struct {
	Keep  fsjail.VPath
	Paths []fsjail.VPath
}

// ReplaceWithSymlinks replaces every copy of each group with a relative
// symlink to its kept file (SPEC §8.7 "Duplikat", optional cleanup). Each
// path must be a regular file, another inode than keep, with the same size
// and full SHA-256 as keep — re-hashed here, never taken from the client;
// any mismatch refuses the whole request before anything moves. All copies
// go to the Recycle Bin as one batch (quota-accounted, one undo token: undo
// removes the links and restores the originals).
func (s *Service) ReplaceWithSymlinks(ctx context.Context, j *fsjail.Jail, userID string, groups []LinkGroup) (*Result, error) {
	if s.trash == nil {
		return nil, errors.New("files: trash store not configured")
	}
	var all, locked []fsjail.VPath
	seen := map[fsjail.VPath]bool{}
	keeps := map[fsjail.VPath]bool{}
	for _, g := range groups {
		keeps[g.Keep] = true
	}
	for gi := range groups {
		g := &groups[gi]
		if g.Keep.IsRoot() {
			return nil, &ValidationError{Reason: "not_a_file", Path: string(g.Keep), Fields: map[string]string{"keep": "not_a_file"}}
		}
		if len(g.Paths) == 0 {
			return nil, &ValidationError{Reason: "empty", Fields: map[string]string{"paths": "required"}}
		}
		g.Paths = dedupe(g.Paths)
		for _, p := range g.Paths {
			switch {
			case keeps[p]:
				return nil, &ValidationError{Reason: "contains_keep", Path: string(p), Fields: map[string]string{"paths": "contains_keep"}}
			case p.IsRoot():
				return nil, &ValidationError{Reason: "not_a_file", Path: string(p), Fields: map[string]string{"paths": "not_a_file"}}
			case seen[p]:
				return nil, &ValidationError{Reason: "duplicate_path", Path: string(p), Fields: map[string]string{"paths": "duplicate_path"}}
			}
			seen[p] = true
			all = append(all, p)
		}
		locked = append(locked, g.Keep)
	}
	if len(all) == 0 {
		return nil, &ValidationError{Reason: "empty", Fields: map[string]string{"paths": "required"}}
	}
	if len(all) > DuplicateLinkMaxItems {
		return nil, &ValidationError{Reason: "too_many", Fields: map[string]string{"paths": fmt.Sprintf("max %d", DuplicateLinkMaxItems)}}
	}
	release, err := s.lock(ctx, j, userID, "symlink", fsjail.Write, append(locked, all...)...)
	if err != nil {
		return nil, err
	}
	defer release()
	s.hook("dedupe_symlink")
	keepOf := map[fsjail.VPath]fsjail.VPath{}
	var budget int64
	for _, g := range groups {
		ki, err := j.Lstat(g.Keep)
		if err != nil {
			return nil, err
		}
		if !ki.IsRegular() {
			return nil, &ValidationError{Reason: "not_a_file", Path: string(g.Keep), Fields: map[string]string{"keep": "not_a_file"}}
		}
		budget += ki.Size * int64(len(g.Paths)+1)
		if budget > DuplicateLinkMaxBytes {
			return nil, &FileTooLargeError{Size: budget, Max: DuplicateLinkMaxBytes}
		}
		want, err := hashCandidate(ctx, j, dupCand{path: g.Keep, size: ki.Size}, false)
		if err != nil {
			return nil, err
		}
		for _, p := range g.Paths {
			if err := s.checkLinkCopy(ctx, j, ki, want, p); err != nil {
				return nil, err
			}
			keepOf[p] = g.Keep
		}
	}
	paths := all
	res, undoItems, err := s.trashLocked(ctx, j, userID, paths, true, "")
	if err != nil {
		return res, err
	}
	links := map[string]string{}
	for i := range res.Results {
		p := fsjail.VPath(res.Results[i].Path)
		target, rerr := filepath.Rel(string(p.Dir()), string(keepOf[p]))
		if rerr == nil {
			rerr = j.Symlink(target, p)
		}
		if rerr != nil {
			// the original is safe in the Recycle Bin; undo still restores it
			s.logger.WarnContext(ctx, "duplicate symlink failed", slog.String("path", string(p)), slog.String("error", rerr.Error()))
			res.Results[i].Status = "skipped"
			res.Results[i].Warnings = append(res.Results[i].Warnings, "symlink_failed")
			continue
		}
		links[string(p)] = target
		s.logActivity(ctx, s.repo, userID, ActSymlink, string(j.Abs(p)), "", map[string]any{"target": target, "dedupe": true}, res.BatchID)
		s.changed(ctx, j, userID, p, false)
	}
	if len(undoItems) > 0 {
		s.withUndo(ctx, j, res, userID, UndoInfo{Kind: "restore", BatchID: res.BatchID, Trash: undoItems, Symlinks: links})
	}
	return res, nil
}

// checkLinkCopy verifies that p may be replaced by a link to the kept file
// described by ki/want.
func (s *Service) checkLinkCopy(ctx context.Context, j *fsjail.Jail, ki fsjail.Info, want string, p fsjail.VPath) error {
	pi, err := j.Lstat(p)
	if err != nil {
		return err
	}
	reason := ""
	switch {
	case !pi.IsRegular():
		reason = "not_a_file"
	case pi.Dev == ki.Dev && pi.Ino == ki.Ino:
		reason = "same_file"
	case pi.Size != ki.Size:
		reason = "size_mismatch"
	}
	if reason == "" {
		sum, herr := hashCandidate(ctx, j, dupCand{path: p, size: pi.Size}, false)
		if herr != nil {
			return herr
		}
		if sum != want {
			reason = "hash_mismatch"
		}
	}
	if reason != "" {
		return &ValidationError{Reason: reason, Path: string(p), Fields: map[string]string{"paths": reason}}
	}
	return nil
}

// dropReplacementLink removes the symlink ReplaceWithSymlinks wrote at p
// before undo restores the original there. Anything else at p (a new file,
// a link to elsewhere) is left alone and the restore reports the conflict.
func (s *Service) dropReplacementLink(j *fsjail.Jail, info UndoInfo, p fsjail.VPath) error {
	target, ok := info.Symlinks[string(p)]
	if !ok {
		return nil
	}
	li, err := j.Lstat(p)
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return err
	}
	if !li.IsSymlink || li.SymlinkTarget != target {
		return nil
	}
	return j.Remove(p)
}
