package archive

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/tasks"
)

const rootDir = "root"

// extraction carries the state of one extract task.
type extraction struct {
	s       *Service
	rt      *tasks.Runtime
	j       *fsjail.Jail
	tmp     *fsjail.Jail
	ws      *workspace
	limits  Limits
	budget  budget
	res     ExtractResult
	filters []string
	// links are created after every regular file (SPEC §8.11 step 2)
	links   []pendingLink
	created map[fsjail.VPath]bool
	warn    int
	step    int64
	since   int64
	known   bool
}

type pendingLink struct {
	link   fsjail.VPath
	target string
	name   string
}

func (x *extraction) skip(name, reason string) {
	if len(x.res.Skipped) < MaxSkipsReported {
		x.res.Skipped = append(x.res.Skipped, Skip{Name: name, Reason: reason})
	}
	x.warn++
	if x.warn <= MaxSkipsReported {
		x.rt.Log("warn", "skipped %q: %s", name, reason)
	}
}

func (x *extraction) included(name string) bool {
	if len(x.filters) == 0 {
		return true
	}
	n := strings.Trim(strings.ReplaceAll(name, "\\", "/"), "/")
	for _, f := range x.filters {
		if n == f || strings.HasPrefix(n, f+"/") {
			return true
		}
	}
	return false
}

// limitsFor computes the byte budget: min(ARCHIVE_MAX_EXTRACT_SIZE, quota
// free, disk free − reserve) (SPEC §8.11 step 3).
func (s *Service) limitsFor(ctx context.Context, j *fsjail.Jail, userID string) (Limits, string) {
	l := Limits{MaxEntries: s.o.MaxEntries, MaxBytes: s.o.MaxExtractSize, Ratio: s.o.Ratio, RatioMinBytes: s.o.RatioMinBytes}
	reason := "max_extract_size"
	if l.MaxBytes <= 0 {
		l.MaxBytes = 1 << 50
	}
	if s.o.Quota != nil {
		if u, err := s.o.Quota.Usage(ctx, userID); err == nil && u.Limit > 0 {
			free := max(u.Free-u.Reserved, 0)
			if free < l.MaxBytes {
				l.MaxBytes, reason = free, "quota"
			}
		}
		if free, _, err := j.Statfs(); err == nil {
			avail := max(int64(free)-s.o.Quota.MinDiskFree, 0) //nolint:gosec // free fits int64
			if avail < l.MaxBytes {
				l.MaxBytes, reason = avail, "disk"
			}
		}
	}
	return l, reason
}

// runExtract is the extract task runner (SPEC §8.11 "Extract").
func (s *Service) runExtract(ctx context.Context, rt *tasks.Runtime) (any, error) {
	var p ExtractPayload
	if err := json.Unmarshal(rt.Task.Payload, &p); err != nil {
		return nil, validationErr("bad payload", map[string]any{"reason": "bad_payload"})
	}
	password := rt.Secret("password")
	if p.HasPassword && password == "" {
		// retry of a password task: the secret only lives in memory
		return nil, ErrPasswordRequired
	}
	archive, err := fsjail.Clean(p.Archive)
	if err != nil {
		return nil, err
	}
	policy, ok := files.ParsePolicy(p.Policy)
	if !ok {
		return nil, validationErr("unknown conflict policy", map[string]any{"reason": "bad_policy"})
	}
	j, release, err := rt.Jail(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	info, err := j.Lstat(archive)
	if err != nil || !info.IsRegular() {
		return nil, newErr(CodeNotFound, "archive not found", http.StatusNotFound, map[string]any{"path": string(archive)})
	}
	dest, destExists, err := s.resolveDest(j, archive, p.Dest)
	if err != nil {
		return nil, err
	}
	unlock, err := s.acquire(ctx, rt.Task.UserID, fsjail.Read, "extract:"+rt.Task.ID, archive)
	if err != nil {
		return nil, err
	}
	defer unlock()
	f, err := j.Open(archive)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	format, err := Detect(archive.Base(), f, info.Size)
	if err != nil {
		return nil, unsupportedErr("file is not a supported archive", map[string]any{"path": string(archive), "reason": "unknown_format"})
	}
	ws, err := s.openWork(rt.Task.ID)
	if err != nil {
		return nil, err
	}
	defer ws.discard()
	tmp, err := ws.jailOn(rootDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tmp.Close() }()
	limits, reason := s.limitsFor(ctx, j, rt.Task.UserID)
	x := &extraction{s: s, rt: rt, j: j, tmp: tmp, ws: ws, limits: limits, filters: p.Entries, created: map[fsjail.VPath]bool{}, step: s.step,
		budget: budget{limits: limits, archiveSize: info.Size, reason: reason}, res: ExtractResult{Format: format, Dest: string(dest)}}
	rt.Log("info", "extracting %s (%s) to %s", archive.Base(), format, string(dest))

	var res *quota.Reservation
	var src Source
	if format == FormatSevenZip && strings.HasSuffix(archive.Base(), ".001") && s.SevenZipAvailable() {
		// multi-volume 7z: only the CLI follows the volumes
		err = ErrCLIOnly
	} else {
		src, err = Open(f, info.Size, format, OpenOptions{Name: archive.Base(), Password: password, Encoding: p.Encoding})
		if err != nil && format == FormatSevenZip && IsCode(err, CodeCorrupt) && s.SevenZipAvailable() {
			// SPEC §8.11: `7z x` is the fallback for 7z variants the library refuses
			rt.Log("warn", "7z library could not open the archive; falling back to the 7-Zip CLI")
			err = ErrCLIOnly
		}
	}
	switch {
	case errors.Is(err, ErrCLIOnly):
		err = x.extractCLI(ctx, j, archive, password)
	case err != nil:
		return nil, err
	default:
		err = x.extractGo(ctx, src, password, &res)
	}
	if err != nil {
		if res != nil {
			res.Release()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	// the archive is fully read: drop its read lock before taking the write
	// lock on the destination (here = the archive's own folder, an ancestor)
	unlock()
	// place the tree, then account the quota with the real byte count
	replaced, err := x.place(ctx, dest, destExists, policy)
	if err != nil {
		if res != nil {
			res.Release()
		}
		return nil, err
	}
	actual := x.budget.written - replaced
	if s.o.Quota != nil && s.o.Repo != nil {
		if res != nil {
			if err := res.Commit(ctx, s.o.Repo, actual); err != nil {
				s.logger.ErrorContext(ctx, "extract: quota commit failed", slog.String("task", rt.Task.ID), slog.String("error", err.Error()))
				_ = s.o.Quota.MarkDirty(ctx, rt.Task.UserID)
			}
		} else if err := s.o.Quota.Apply(ctx, s.o.Repo, rt.Task.UserID, actual, 0, 0); err != nil {
			_ = s.o.Quota.MarkDirty(ctx, rt.Task.UserID)
		}
	}
	x.res.Bytes = x.budget.written
	x.res.Warnings = x.warn
	s.activity(ctx, rt.Task.UserID, ActExtract, string(archive), string(dest), map[string]any{"format": string(format), "files": x.res.Files, "bytes": x.res.Bytes, "skipped": x.warn})
	s.changed(ctx, rt.Task.UserID, dest, true)
	s.invalidate(rt.Task.UserID, dest)
	s.invalidate(rt.Task.UserID, dest.Dir())
	rt.Emit(tasks.EventChanged, files.ChangedEvent{Dir: string(dest.Dir()), Reason: "extract", Paths: []string{string(dest)}})
	rt.Emit(tasks.EventChanged, files.ChangedEvent{Dir: string(dest), Reason: "extract"})
	rt.Progress(x.budget.written, max(x.budget.written, 1))
	rt.Log("info", "extracted %d files, %d folders, %d bytes (%d skipped)", x.res.Files, x.res.Dirs, x.res.Bytes, x.warn)
	return x.res, nil
}

// resolveDest turns here|new|<path> into the destination folder.
func (s *Service) resolveDest(j *fsjail.Jail, archive fsjail.VPath, dest string) (fsjail.VPath, bool, error) {
	switch dest {
	case DestHere:
		return archive.Dir(), true, nil
	case "", DestNew:
		stem := Stem(archive.Base())
		if stem == "" || stem == archive.Base() {
			stem = archive.Base() + "-extracted"
		}
		vp, err := fsjail.Join(archive.Dir(), stem)
		if err != nil {
			return "", false, err
		}
		_, lerr := j.Lstat(vp)
		return vp, lerr == nil, nil
	}
	vp, err := fsjail.Clean(dest)
	if err != nil {
		return "", false, err
	}
	info, lerr := j.Lstat(vp)
	if lerr == nil {
		if !info.IsDir {
			return "", false, validationErr("destination is not a folder", map[string]any{"reason": "not_a_folder", "path": string(vp)})
		}
		return vp, true, nil
	}
	if pi, perr := j.Lstat(vp.Dir()); perr != nil || !pi.IsDir {
		return "", false, newErr(CodeNotFound, "destination parent not found", http.StatusNotFound, map[string]any{"path": string(vp.Dir())})
	}
	return vp, false, nil
}

// extractGo drives a Source: pre-scan when the container has an index,
// then stream every entry into the temporary root.
func (x *extraction) extractGo(ctx context.Context, src Source, password string, res **quota.Reservation) error {
	rt := x.rt
	if src.Encrypted() && password == "" {
		return ErrPasswordRequired
	}
	index, hasIndex, err := src.Index(ctx)
	if err != nil {
		return err
	}
	if hasIndex {
		var total int64
		n := 0
		for i := range index {
			e := &index[i]
			if !x.included(e.Name) {
				continue
			}
			n++
			if e.Size > 0 && !e.IsDir && !e.IsSymlink {
				total += e.Size
			}
		}
		if err := x.limits.checkIndex(n, total, x.budget.archiveSize, x.budget.reason); err != nil {
			return err
		}
		if x.s.o.Quota != nil && total > 0 {
			r, err := x.s.o.Quota.Reserve(ctx, x.j, rt.Task.UserID, total)
			if err != nil {
				return err
			}
			*res = r
		}
		x.known = true
		rt.SetTotal(total)
	} else {
		rt.SetTotal(0)
	}
	if err := src.Walk(ctx, x.handle); err != nil {
		return err
	}
	x.createLinks()
	if d, ok := src.(interface{ Detected() string }); ok {
		x.res.Encoding = d.Detected()
	}
	return nil
}

// handle extracts one entry into the temporary root.
func (x *extraction) handle(e *Entry) error {
	if !x.included(e.Name) {
		return nil
	}
	if err := x.budget.addEntry(); err != nil {
		return err
	}
	vp, reason, warnings := SafePath(e.Name)
	if reason != "" {
		if reason == ReasonAbsolute || reason == ReasonDotDot || reason == ReasonDrive || reason == ReasonNUL {
			// a hostile name fails the whole extraction (SPEC §8.11 step 2)
			return unsafeErr("archive contains an unsafe entry name", map[string]any{"reason": reason, "entry": truncateName(e.Name)})
		}
		x.skip(e.Name, reason)
		return nil
	}
	if len(warnings) > 0 {
		x.warn++
	}
	switch {
	case e.IsDir:
		if err := x.tmp.MkdirAll(vp); err != nil {
			x.skip(e.Name, ReasonTypeClash)
			return nil
		}
		x.res.Dirs++
		x.setTime(vp, e.ModTime)
		return nil
	case e.IsSymlink:
		x.links = append(x.links, pendingLink{link: vp, target: e.LinkTarget, name: e.Name})
		return nil
	case e.Hardlink != "":
		return x.hardlink(e, vp)
	case e.Mode&(fs.ModeDevice|fs.ModeNamedPipe|fs.ModeSocket|fs.ModeCharDevice) != 0:
		x.skip(e.Name, ReasonSpecial)
		return nil
	case e.Open == nil:
		x.skip(e.Name, ReasonSpecial)
		return nil
	}
	if x.s.o.Files != nil && x.s.o.Files.Blocked(vp.Base()) {
		x.skip(e.Name, ReasonBlockedExt)
		if e.Size > 0 && x.known {
			x.rt.Add(e.Size)
		}
		return nil
	}
	if err := x.tmp.MkdirAll(vp.Dir()); err != nil {
		x.skip(e.Name, ReasonTypeClash)
		return nil
	}
	rc, err := e.Open()
	if err != nil {
		if errors.Is(err, ErrPasswordRequired) || errors.Is(err, ErrPasswordWrong) {
			return err
		}
		var ae *Error
		if errors.As(err, &ae) {
			return err
		}
		return corruptErr("cannot read entry " + truncateName(e.Name))
	}
	defer func() { _ = rc.Close() }()
	f, err := x.tmp.Create(vp, false)
	if err != nil {
		if errors.Is(err, fsjail.ErrIsDir) || errors.Is(err, fs.ErrExist) {
			x.skip(e.Name, ReasonTypeClash)
			return nil
		}
		return err
	}
	n, cerr := x.copyBounded(f, rc)
	if err := f.Close(); err != nil && cerr == nil {
		cerr = err
	}
	if cerr != nil {
		return cerr
	}
	if e.Size >= 0 && x.known && n < e.Size {
		// header claimed more than the stream held: keep progress honest
		x.rt.Add(e.Size - n)
	}
	x.created[vp] = true
	x.res.Files++
	x.setTime(vp, e.ModTime)
	return nil
}

func truncateName(n string) string {
	if len(n) > 200 {
		return n[:200] + "…"
	}
	return n
}

// copyBounded streams body into f while charging the budget and reporting
// progress; a lying header is stopped at the limit (+ one chunk).
func (x *extraction) copyBounded(f *os.File, body io.Reader) (int64, error) {
	buf := make([]byte, 128<<10)
	var written int64
	for {
		n, rerr := body.Read(buf)
		if n > 0 {
			if err := x.budget.add(int64(n)); err != nil {
				return written, err
			}
			if _, werr := f.Write(buf[:n]); werr != nil {
				return written, werr
			}
			written += int64(n)
			x.since += int64(n)
			x.rt.Add(int64(n))
			if x.since >= x.step {
				x.since = 0
				if err := x.ctxErr(); err != nil {
					return written, err
				}
			}
		}
		if rerr == io.EOF {
			return written, nil
		}
		if rerr != nil {
			if errors.Is(rerr, ErrPasswordWrong) || errors.Is(rerr, ErrPasswordRequired) {
				return written, rerr
			}
			var ae *Error
			if errors.As(rerr, &ae) {
				return written, rerr
			}
			return written, corruptErr("cannot read entry: " + tail(rerr.Error(), 120))
		}
	}
}

func (x *extraction) ctxErr() error {
	if x.rt.Cancelled() {
		return context.Canceled
	}
	return nil
}

func (x *extraction) setTime(vp fsjail.VPath, t time.Time) {
	if !t.IsZero() && t.Unix() > 0 {
		_ = x.tmp.Chtimes(vp, t, t)
	}
}

// hardlink copies the already extracted target (never a real link).
func (x *extraction) hardlink(e *Entry, vp fsjail.VPath) error {
	tvp, reason, _ := SafePath(e.Hardlink)
	if reason != "" || !x.created[tvp] {
		x.skip(e.Name, ReasonHardlink)
		return nil
	}
	if x.s.o.Files != nil && x.s.o.Files.Blocked(vp.Base()) {
		x.skip(e.Name, ReasonBlockedExt)
		return nil
	}
	if err := x.tmp.MkdirAll(vp.Dir()); err != nil {
		x.skip(e.Name, ReasonTypeClash)
		return nil
	}
	info, err := x.tmp.Lstat(tvp)
	if err != nil || !info.IsRegular() {
		x.skip(e.Name, ReasonHardlink)
		return nil
	}
	if err := x.budget.add(info.Size); err != nil {
		return err
	}
	if err := x.tmp.CopyFile(context.Background(), tvp, vp, nil); err != nil {
		x.skip(e.Name, ReasonHardlink)
		return nil
	}
	x.created[vp] = true
	x.res.Files++
	x.rt.Add(info.Size)
	return nil
}

// createLinks creates the symlinks last; targets must resolve inside the
// extraction root (Jail.Symlink enforces it).
func (x *extraction) createLinks() {
	for _, l := range x.links {
		if !LinkInside(l.link, l.target) {
			x.skip(l.name, ReasonLinkEscape)
			continue
		}
		_ = x.tmp.MkdirAll(l.link.Dir())
		if err := x.tmp.Symlink(l.target, l.link); err != nil {
			if errors.Is(err, fsjail.ErrEscape) {
				x.skip(l.name, ReasonLinkEscape)
			} else {
				x.skip(l.name, ReasonTypeClash)
			}
			continue
		}
		x.res.Links++
	}
}

// extractCLI handles iso (and 7z archives the library refuses) through
// `7z x` into the temporary root: pre-scan with `l -slt` for limits and
// name safety, extract, then drop links/special files and apply the blocked
// extensions (SPEC §8.11; password via stdin, see cli.go).
func (x *extraction) extractCLI(ctx context.Context, j *fsjail.Jail, archive fsjail.VPath, password string) error {
	host, err := j.AbsForCLI(archive)
	if err != nil {
		return err
	}
	entries, err := x.s.list7z(ctx, host, password)
	if err != nil {
		return err
	}
	var total int64
	n := 0
	for _, e := range entries {
		if !x.included(e.Path) {
			continue
		}
		if _, reason, _ := SafePath(e.Path); reason == ReasonAbsolute || reason == ReasonDotDot || reason == ReasonDrive || reason == ReasonNUL {
			return unsafeErr("archive contains an unsafe entry name", map[string]any{"reason": reason, "entry": truncateName(e.Path)})
		}
		n++
		if !e.IsDir {
			total += e.Size
		}
	}
	if err := x.limits.checkIndex(n, total, x.budget.archiveSize, x.budget.reason); err != nil {
		return err
	}
	if x.s.o.Quota != nil {
		r, err := x.s.o.Quota.Reserve(ctx, j, x.rt.Task.UserID, total)
		if err != nil {
			return err
		}
		defer r.Release() // committed by the caller through Apply (budget.written)
	}
	x.rt.SetTotal(total)
	out, err := x.ws.sys.HostPath(rootDir)
	if err != nil {
		return err
	}
	args := []string{"x", "-y", "-bd", "-bsp1", "-bso0", "-o" + out}
	for _, f := range x.filters {
		args = append(args, "-i!"+f, "-i!"+f+"/*")
	}
	args = append(args, "--", host)
	if _, err := x.s.run7z(ctx, args, password, out, func(pct int) { x.rt.Progress(total*int64(pct)/100, total) }); err != nil {
		return err
	}
	return x.sanitizeTree(ctx)
}

// sanitizeTree walks the CLI output: links and special files are removed,
// blocked extensions dropped, bytes counted against the budget.
func (x *extraction) sanitizeTree(ctx context.Context) error {
	var remove []fsjail.VPath
	err := x.tmp.WalkDir(ctx, fsjail.Root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		vp := fsjail.VPath(p)
		if vp.IsRoot() {
			return nil
		}
		if err := x.budget.addEntry(); err != nil {
			return err
		}
		switch {
		case d.IsDir():
			x.res.Dirs++
		case d.Type()&fs.ModeSymlink != 0:
			x.skip(vp.Rel(), ReasonLinkEscape)
			remove = append(remove, vp)
		case !d.Type().IsRegular():
			x.skip(vp.Rel(), ReasonSpecial)
			remove = append(remove, vp)
		case x.s.o.Files != nil && x.s.o.Files.Blocked(vp.Base()):
			x.skip(vp.Rel(), ReasonBlockedExt)
			remove = append(remove, vp)
		default:
			info, err := x.tmp.Lstat(vp)
			if err != nil {
				return nil
			}
			if err := x.budget.add(info.Size); err != nil {
				return err
			}
			x.res.Files++
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, vp := range remove {
		_ = x.tmp.Remove(vp)
	}
	x.rt.Progress(x.budget.written, max(x.budget.written, 1))
	return nil
}

// place moves the extracted tree into the destination (SPEC §8.11 step 8).
// Returns the bytes of files replaced by overwrite.
func (x *extraction) place(ctx context.Context, dest fsjail.VPath, destExists bool, policy files.Policy) (int64, error) {
	unlock, err := x.s.acquire(ctx, x.rt.Task.UserID, fsjail.Write, "extract:"+x.rt.Task.ID, dest)
	if err != nil {
		return 0, err
	}
	defer unlock()
	if !destExists {
		if err := x.j.MoveIn(ctx, x.ws.sys, rootDir, dest); err != nil {
			if errors.Is(err, fsjail.ErrExists) {
				// created meanwhile: fall through to the merge path
				return x.merge(ctx, dest, policy)
			}
			return 0, err
		}
		return 0, nil
	}
	return x.merge(ctx, dest, policy)
}

// merge places every top-level entry into an existing folder with the
// conflict policy; folders merge per file under overwrite.
func (x *extraction) merge(ctx context.Context, dest fsjail.VPath, policy files.Policy) (int64, error) {
	tops, err := x.tmp.ReadDir(fsjail.Root)
	if err != nil {
		return 0, err
	}
	if policy == files.PolicyFail {
		var conflicts []map[string]any
		for _, t := range tops {
			target, err := fsjail.Join(dest, t.Name)
			if err != nil {
				continue
			}
			if existing, err := x.j.Lstat(target); err == nil {
				conflicts = append(conflicts, map[string]any{"path": string(target), "isDir": existing.IsDir, "existing": map[string]any{"size": existing.Size, "mtime": existing.ModTime.UTC().Format(time.RFC3339Nano)}})
			}
		}
		if len(conflicts) > 0 {
			return 0, newErr(CodeAlreadyExists, "destination already contains some of the entries", http.StatusConflict, map[string]any{"conflicts": conflicts, "dest": string(dest)})
		}
	}
	var replaced int64
	for _, t := range tops {
		if err := ctx.Err(); err != nil {
			return replaced, err
		}
		r, err := x.placeOne(ctx, rootDir+"/"+t.Name, fsjail.VPath("/"+t.Name), dest, t.Name, policy)
		if err != nil {
			return replaced, err
		}
		replaced += r
	}
	return replaced, nil
}

// placeOne moves tmp/<sysRel> (tmpVP in the tmp jail) to dest/<name>.
func (x *extraction) placeOne(ctx context.Context, sysRel string, tmpVP, dest fsjail.VPath, name string, policy files.Policy) (int64, error) {
	target, err := fsjail.Join(dest, name)
	if err != nil {
		x.skip(name, ReasonInvalid)
		return 0, nil
	}
	existing, lerr := x.j.Lstat(target)
	if lerr != nil {
		if !errors.Is(lerr, fsjail.ErrNotFound) {
			return 0, lerr
		}
		return 0, x.j.MoveIn(ctx, x.ws.sys, sysRel, target)
	}
	src, err := x.tmp.Lstat(tmpVP)
	if err != nil {
		return 0, err
	}
	switch policy {
	case files.PolicySkip:
		x.res.SkippedDest++
		return 0, nil
	case files.PolicyRename:
		unique, err := files.UniqueName(x.j, dest, name, src.IsDir)
		if err != nil {
			return 0, err
		}
		x.res.Renamed++
		return 0, x.j.MoveIn(ctx, x.ws.sys, sysRel, unique)
	case files.PolicyOverwrite:
		switch {
		case src.IsDir && existing.IsDir:
			return x.mergeDir(ctx, sysRel, tmpVP, target, policy)
		case src.IsDir != existing.IsDir:
			x.skip(name, ReasonTypeClash)
			x.res.SkippedDest++
			return 0, nil
		}
		// file over file: move next to it, then rename over (atomic replace)
		staging, err := fsjail.Join(dest, ".fm-extract-"+x.rt.Task.ID+"-"+name)
		if err != nil {
			return 0, err
		}
		if err := x.j.MoveIn(ctx, x.ws.sys, sysRel, staging); err != nil {
			return 0, err
		}
		if err := x.j.Rename(staging, target); err != nil {
			_ = x.j.Remove(staging)
			return 0, err
		}
		x.res.Overwritten++
		x.s.changed(ctx, x.rt.Task.UserID, target, false)
		return existing.Size, nil
	}
	return 0, newErr(CodeAlreadyExists, "destination already exists", http.StatusConflict, map[string]any{"path": string(target)})
}

// mergeDir merges tmp folder into an existing destination folder.
func (x *extraction) mergeDir(ctx context.Context, sysRel string, tmpVP, target fsjail.VPath, policy files.Policy) (int64, error) {
	children, err := x.tmp.ReadDir(tmpVP)
	if err != nil {
		return 0, err
	}
	var replaced int64
	for _, c := range children {
		if err := ctx.Err(); err != nil {
			return replaced, err
		}
		childTmp, err := fsjail.Join(tmpVP, c.Name)
		if err != nil {
			continue
		}
		r, err := x.placeOne(ctx, path.Join(sysRel, c.Name), childTmp, target, c.Name, policy)
		if err != nil {
			return replaced, err
		}
		replaced += r
	}
	return replaced, nil
}
