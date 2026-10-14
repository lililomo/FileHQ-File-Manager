package archive

import (
	"archive/zip"
	"compress/flate"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"

	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/tasks"
)

// Zip editing (SPEC §8.11 "supportsModify", PROMPT Step 14b): add or
// remove members of an unencrypted zip by rewriting it in the task
// workspace. Kept members are copied raw (CreateRaw/OpenRaw: compressed
// bytes, CRC and sizes untouched, no re-compression), new files are
// deflated, then the new file replaces the original atomically. The
// original is never modified in place, so a failure leaves it intact.

// Zip edit modes.
const (
	ZipEditAdd    = "add"
	ZipEditRemove = "remove"
)

// ZipEditPayload is the zip_edit task payload.
type ZipEditPayload struct {
	Archive string   `json:"archive"`
	Mode    string   `json:"mode"`
	Sources []string `json:"sources,omitempty"`
	Into    string   `json:"into,omitempty"`
	Entries []string `json:"entries,omitempty"`
}

// ZipEditResult is the zip_edit task result.
type ZipEditResult struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Entries  int    `json:"entries"`
	Added    int    `json:"added"`
	Removed  int    `json:"removed"`
	Replaced int    `json:"replaced"`
	Skipped  []Skip `json:"skipped,omitempty"`
}

// ZipAddRequest is the validated body of POST /archives/add.
type ZipAddRequest struct {
	UserID  string
	Archive string
	Sources []string
	Into    string
}

// ZipRemoveRequest is the validated body of POST /archives/remove-entries.
type ZipRemoveRequest struct {
	UserID  string
	Archive string
	Entries []string
}

func zipEditLink(t tasks.Task) string {
	var p ZipEditPayload
	if json.Unmarshal(t.Payload, &p) != nil || p.Archive == "" {
		return ""
	}
	return "/files" + path.Dir(p.Archive) + "?archive=" + p.Archive
}

// checkModifiable validates the archive for in-place editing.
func (s *Service) checkModifiable(ctx context.Context, j *fsjail.Jail, userID, raw string) (*opened, error) {
	op, err := s.openArchive(j, raw)
	if err != nil {
		return nil, err
	}
	if !op.format.IsZip() {
		op.close()
		return nil, unsupportedErr("only zip archives can be modified", map[string]any{"reason": "format", "format": string(op.format)})
	}
	if op.info.Size > MaxModifySize {
		op.close()
		return nil, unsupportedErr("archive is too large to modify", map[string]any{"reason": "too_large_to_modify", "limit": MaxModifySize})
	}
	ix, err := s.indexFor(ctx, j, userID, op, "", "auto")
	if err != nil {
		op.close()
		return nil, err
	}
	if ix.Encrypted {
		op.close()
		return nil, unsupportedErr("encrypted zip archives cannot be modified", map[string]any{"reason": "encrypted"})
	}
	return op, nil
}

// ZipAdd validates and enqueues an add task.
func (s *Service) ZipAdd(ctx context.Context, j *fsjail.Jail, req ZipAddRequest) (tasks.Task, error) {
	if s.o.Tasks == nil {
		return tasks.Task{}, newErr(CodeInternal, "task engine unavailable", http.StatusServiceUnavailable, nil)
	}
	if len(req.Sources) == 0 || len(req.Sources) > MaxSources {
		return tasks.Task{}, validationErr("1..1000 sources per request", map[string]any{"fields": map[string]any{"sources": "1..1000"}})
	}
	into, ok := NormalizeInner(req.Into)
	if !ok {
		return tasks.Task{}, validationErr("target folder inside the archive is not valid", map[string]any{"fields": map[string]any{"into": "invalid"}})
	}
	for _, seg := range strings.Split(into, "/") {
		if seg == "" {
			continue
		}
		if _, err := fsjail.ValidateName(seg); err != nil {
			return tasks.Task{}, validationErr("target folder inside the archive is not valid", map[string]any{"fields": map[string]any{"into": "invalid"}, "segment": seg})
		}
	}
	op, err := s.checkModifiable(ctx, j, req.UserID, req.Archive)
	if err != nil {
		return tasks.Task{}, err
	}
	op.close()
	paths := make([]fsjail.VPath, 0, len(req.Sources))
	seen := map[fsjail.VPath]struct{}{}
	for i, raw := range req.Sources {
		vp, err := fsjail.Clean(raw)
		if err != nil || vp.IsRoot() {
			return tasks.Task{}, validationErr("source path is not valid", map[string]any{"fields": map[string]any{"sources": "invalid"}, "index": i})
		}
		if _, dup := seen[vp]; dup {
			continue
		}
		seen[vp] = struct{}{}
		if vp == op.archive || op.archive.HasPrefix(vp) {
			return tasks.Task{}, validationErr("an archive cannot be added to itself", map[string]any{"reason": "into_itself", "path": string(vp)})
		}
		info, err := j.Lstat(vp)
		if err != nil {
			return tasks.Task{}, newErr(CodeNotFound, "source not found", http.StatusNotFound, map[string]any{"path": string(vp), "index": i})
		}
		if !info.IsDir && !info.IsRegular() {
			return tasks.Task{}, validationErr("source must be a file or a folder", map[string]any{"fields": map[string]any{"sources": "not_a_file"}, "path": string(vp)})
		}
		if !info.IsDir && s.o.Files != nil && s.o.Files.Blocked(vp.Base()) {
			return tasks.Task{}, unsupportedErr("file extension is blocked", map[string]any{"reason": "blocked_extension", "name": vp.Base()})
		}
		paths = append(paths, vp)
	}
	strPaths := make([]string, len(paths))
	for i, p := range paths {
		strPaths[i] = string(p)
	}
	payload := ZipEditPayload{Archive: string(op.archive), Mode: ZipEditAdd, Sources: strPaths, Into: into}
	locks := append([]string{string(op.archive)}, strPaths...)
	title := "Tambah " + itoa(len(paths)) + " item ke " + op.archive.Base()
	return s.o.Tasks.Enqueue(ctx, tasks.EnqueueRequest{UserID: req.UserID, Type: TaskZipEdit, Title: title, Payload: payload, Locks: locks})
}

// ZipRemove validates and enqueues a remove task.
func (s *Service) ZipRemove(ctx context.Context, j *fsjail.Jail, req ZipRemoveRequest) (tasks.Task, error) {
	if s.o.Tasks == nil {
		return tasks.Task{}, newErr(CodeInternal, "task engine unavailable", http.StatusServiceUnavailable, nil)
	}
	if len(req.Entries) == 0 || len(req.Entries) > MaxEntryFilters {
		return tasks.Task{}, validationErr("1..1000 entries per request", map[string]any{"fields": map[string]any{"entries": "1..1000"}})
	}
	op, err := s.checkModifiable(ctx, j, req.UserID, req.Archive)
	if err != nil {
		return tasks.Task{}, err
	}
	defer op.close()
	ix, err := s.indexFor(ctx, j, req.UserID, op, "", "auto")
	if err != nil {
		return tasks.Task{}, err
	}
	entries := make([]string, 0, len(req.Entries))
	for _, raw := range req.Entries {
		n, ok := NormalizeInner(raw)
		if !ok || n == "" {
			return tasks.Task{}, validationErr("entry name is not valid", map[string]any{"fields": map[string]any{"entries": "invalid"}, "entry": raw})
		}
		if _, found := ix.Lookup(n); !found {
			return tasks.Task{}, newErr(CodeNotFound, "entry not found in archive", http.StatusNotFound, map[string]any{"entry": n})
		}
		entries = append(entries, n)
	}
	payload := ZipEditPayload{Archive: string(op.archive), Mode: ZipEditRemove, Entries: entries}
	title := "Hapus " + itoa(len(entries)) + " entri dari " + op.archive.Base()
	return s.o.Tasks.Enqueue(ctx, tasks.EnqueueRequest{UserID: req.UserID, Type: TaskZipEdit, Title: title, Payload: payload, Locks: []string{string(op.archive)}})
}

// zipRemoveSet matches member names against the removal list (a folder
// name removes everything below it).
func zipRemoveSet(entries []string) func(name string) bool {
	exact := make(map[string]struct{}, len(entries))
	for _, e := range entries {
		exact[e] = struct{}{}
	}
	return func(name string) bool {
		n, ok := NormalizeInner(name)
		if !ok {
			return false
		}
		if _, hit := exact[n]; hit {
			return true
		}
		for e := range exact {
			if strings.HasPrefix(n, e+"/") {
				return true
			}
		}
		return false
	}
}

// stripExtra removes the zip64 extra field (0x0001): the writer adds its
// own when the sizes need it, and two copies confuse readers.
func stripExtra(extra []byte) []byte {
	var out []byte
	for len(extra) >= 4 {
		id := binary.LittleEndian.Uint16(extra[0:2])
		size := int(binary.LittleEndian.Uint16(extra[2:4]))
		if 4+size > len(extra) {
			break
		}
		if id != 0x0001 {
			out = append(out, extra[:4+size]...)
		}
		extra = extra[4+size:]
	}
	return out
}

// runZipEdit is the zip_edit task runner.
func (s *Service) runZipEdit(ctx context.Context, rt *tasks.Runtime) (any, error) {
	var p ZipEditPayload
	if err := json.Unmarshal(rt.Task.Payload, &p); err != nil {
		return nil, validationErr("bad payload", map[string]any{"reason": "bad_payload"})
	}
	archive, err := fsjail.Clean(p.Archive)
	if err != nil {
		return nil, err
	}
	j, release, err := rt.Jail(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	sources := make([]fsjail.VPath, 0, len(p.Sources))
	for _, raw := range p.Sources {
		vp, err := fsjail.Clean(raw)
		if err != nil {
			return nil, err
		}
		sources = append(sources, vp)
	}
	unlockW, err := s.acquire(ctx, rt.Task.UserID, fsjail.Write, "zipedit:"+rt.Task.ID, archive)
	if err != nil {
		return nil, err
	}
	defer unlockW()
	if len(sources) > 0 {
		unlockR, err := s.acquire(ctx, rt.Task.UserID, fsjail.Read, "zipedit:"+rt.Task.ID, sources...)
		if err != nil {
			return nil, err
		}
		defer unlockR()
	}
	info, err := j.Lstat(archive)
	if err != nil || !info.IsRegular() {
		return nil, newErr(CodeNotFound, "archive not found", http.StatusNotFound, map[string]any{"path": string(archive)})
	}
	if info.Size > MaxModifySize {
		return nil, unsupportedErr("archive is too large to modify", map[string]any{"reason": "too_large_to_modify", "limit": MaxModifySize})
	}
	f, err := j.Open(archive)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	zr, err := zip.NewReader(f, info.Size)
	if err != nil {
		return nil, corruptErr("not a valid zip file")
	}
	for _, zf := range zr.File {
		if zf.Flags&0x1 != 0 {
			return nil, unsupportedErr("encrypted zip archives cannot be modified", map[string]any{"reason": "encrypted"})
		}
	}
	c := &compression{s: s, rt: rt, j: j, seen: map[string]bool{}}
	res := ZipEditResult{Path: string(archive)}
	// pre-scan the sources: names, sizes (progress + quota estimate)
	type newFile struct {
		name string
		vp   fsjail.VPath
		info fsjail.Info
	}
	var (
		add      []newFile
		addDirs  []newFile
		addTotal int64
	)
	if p.Mode == ZipEditAdd {
		err = c.walkSources(ctx, sources, func(rel string, vp fsjail.VPath, fi fsjail.Info) error {
			name := rel
			if p.Into != "" {
				name = p.Into + "/" + rel
			}
			switch {
			case fi.IsDir:
				addDirs = append(addDirs, newFile{name: name + "/", vp: vp, info: fi})
				return nil
			case fi.IsSymlink:
				c.skip(rel, "symlink")
				return nil
			case !fi.IsRegular():
				c.skip(rel, ReasonSpecial)
				return nil
			}
			if s.o.Files != nil && s.o.Files.Blocked(fi.Name) {
				c.skip(rel, ReasonBlockedExt)
				return nil
			}
			if !c.dedupe(name) {
				return nil
			}
			add = append(add, newFile{name: name, vp: vp, info: fi})
			addTotal += fi.Size
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	rt.SetTotal(info.Size + addTotal)
	var resv *quota.Reservation
	if s.o.Quota != nil {
		r, err := s.o.Quota.Reserve(ctx, j, rt.Task.UserID, addTotal)
		if err != nil {
			return nil, err
		}
		resv = r
	}
	fail := func(err error) (any, error) {
		if resv != nil {
			resv.Release()
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	ws, err := s.openWork(rt.Task.ID)
	if err != nil {
		return fail(err)
	}
	defer ws.discard()
	if err := ws.sys.MkdirAll(outDir); err != nil {
		return fail(err)
	}
	outName := path.Join(outDir, archive.Base())
	out, err := ws.sys.Create(outName)
	if err != nil {
		return fail(err)
	}
	zw := zip.NewWriter(out)
	zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) { return flate.NewWriter(w, flate.DefaultCompression) })
	remove := zipRemoveSet(p.Entries)
	replaced := map[string]bool{}
	for _, nf := range add {
		replaced[nf.name] = true
	}
	for _, nd := range addDirs {
		replaced[nd.name] = true
	}
	rt.Log("info", "rewriting %s (%d bytes, %d members)", archive.Base(), info.Size, len(zr.File))
	buf := make([]byte, 128<<10)
	var since int64
	for _, zf := range zr.File {
		if err := ctx.Err(); err != nil {
			_ = out.Close()
			return fail(err)
		}
		norm, ok := NormalizeInner(zf.Name)
		switch {
		case p.Mode == ZipEditRemove && ok && remove(zf.Name):
			res.Removed++
			continue
		case p.Mode == ZipEditAdd && (replaced[zf.Name] || (ok && (replaced[norm] || replaced[norm+"/"]))):
			// a new file/folder with the same name wins (overwrite)
			if !strings.HasSuffix(zf.Name, "/") {
				res.Replaced++
			}
			continue
		}
		fh := zf.FileHeader
		fh.Extra = stripExtra(fh.Extra)
		w, err := zw.CreateRaw(&fh)
		if err != nil {
			_ = out.Close()
			return fail(err)
		}
		raw, err := zf.OpenRaw()
		if err != nil {
			_ = out.Close()
			return fail(corruptErr("cannot read member " + truncateName(zf.Name)))
		}
		for {
			n, rerr := raw.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					_ = out.Close()
					return fail(werr)
				}
				rt.Add(int64(n))
				since += int64(n)
				if since >= s.step {
					since = 0
					if rt.Cancelled() || ctx.Err() != nil {
						_ = out.Close()
						return fail(context.Canceled)
					}
				}
			}
			if rerr == io.EOF {
				break
			}
			if rerr != nil {
				_ = out.Close()
				return fail(corruptErr("cannot read member " + truncateName(zf.Name)))
			}
		}
		res.Entries++
	}
	for _, nd := range addDirs {
		if !c.dedupe(nd.name) {
			continue
		}
		fh := &zip.FileHeader{Name: nd.name, Method: zip.Store, Modified: nd.info.ModTime}
		fh.SetMode(0o755 | fs.ModeDir)
		if _, err := zw.CreateHeader(fh); err != nil {
			_ = out.Close()
			return fail(err)
		}
		res.Entries++
	}
	for _, nf := range add {
		fh := &zip.FileHeader{Name: nf.name, Method: zip.Deflate, Modified: nf.info.ModTime}
		fh.SetMode(0o644)
		w, err := zw.CreateHeader(fh)
		if err != nil {
			_ = out.Close()
			return fail(err)
		}
		if _, err := c.copyFrom(ctx, w, nf.vp); err != nil {
			_ = out.Close()
			return fail(err)
		}
		res.Added++
		res.Entries++
	}
	if err := zw.Close(); err != nil {
		_ = out.Close()
		return fail(err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		return fail(err)
	}
	if err := out.Close(); err != nil {
		return fail(err)
	}
	// verify the result before it replaces anything
	rf, err := ws.sys.Open(outName)
	if err != nil {
		return fail(err)
	}
	st, err := rf.Stat()
	if err != nil {
		_ = rf.Close()
		return fail(err)
	}
	_, verr := zip.NewReader(rf, st.Size())
	_ = rf.Close()
	if verr != nil {
		return fail(corruptErr("rewritten zip does not verify"))
	}
	_ = f.Close()
	// atomic replace: move next to the archive, then rename over it
	staging, err := fsjail.Join(archive.Dir(), ".fm-zipedit-"+rt.Task.ID+"-"+archive.Base())
	if err != nil {
		return fail(err)
	}
	if err := j.MoveIn(ctx, ws.sys, outName, staging); err != nil {
		return fail(err)
	}
	if err := j.Rename(staging, archive); err != nil {
		_ = j.Remove(staging)
		return fail(err)
	}
	res.Size = st.Size()
	res.Skipped = c.res.Skipped
	delta := res.Size - info.Size
	if resv != nil && s.o.Repo != nil {
		if err := resv.Commit(ctx, s.o.Repo, delta); err != nil {
			s.logger.ErrorContext(ctx, "zip edit: quota commit failed", slog.String("task", rt.Task.ID), slog.String("error", err.Error()))
			_ = s.o.Quota.MarkDirty(ctx, rt.Task.UserID)
		}
	} else if resv != nil {
		resv.Release()
	}
	s.index.invalidate(rt.Task.UserID, string(archive))
	s.sessions.Drop(rt.Task.UserID, string(archive))
	s.activity(ctx, rt.Task.UserID, ActZipEdit, string(archive), "", map[string]any{"mode": p.Mode, "added": res.Added, "removed": res.Removed, "replaced": res.Replaced, "size": res.Size})
	s.changed(ctx, rt.Task.UserID, archive, false)
	s.invalidate(rt.Task.UserID, archive.Dir())
	rt.Emit(tasks.EventChanged, files.ChangedEvent{Dir: string(archive.Dir()), Reason: "archive_edit", Paths: []string{string(archive)}})
	rt.Progress(info.Size+addTotal, max(info.Size+addTotal, 1))
	rt.Log("info", "%s rewritten: %d members (+%d, -%d, %d replaced), %d bytes", archive.Base(), res.Entries, res.Added, res.Removed, res.Replaced, res.Size)
	return res, nil
}
