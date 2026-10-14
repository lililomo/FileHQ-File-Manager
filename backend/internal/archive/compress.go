package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/flate"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/dsnet/compress/bzip2"
	"github.com/klauspost/compress/zstd"
	"github.com/klauspost/pgzip"
	"github.com/ulikunitz/xz"

	"kppbwi.app/filemanager/internal/archive/zipaes"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/tasks"
)

const outDir = "out"

// compression carries the state of one compress task.
type compression struct {
	s     *Service
	rt    *tasks.Runtime
	j     *fsjail.Jail
	total int64
	read  int64
	since int64
	res   CompressResult
	warn  int
	seen  map[string]bool
}

func (c *compression) skip(name, reason string) {
	if len(c.res.Skipped) < MaxSkipsReported {
		c.res.Skipped = append(c.res.Skipped, Skip{Name: name, Reason: reason})
	}
	c.warn++
	if c.warn <= MaxSkipsReported {
		c.rt.Log("warn", "skipped %q: %s", name, reason)
	}
}

// runCompress is the compress task runner (SPEC §8.11 "Compress").
func (s *Service) runCompress(ctx context.Context, rt *tasks.Runtime) (any, error) {
	var p CompressPayload
	if err := json.Unmarshal(rt.Task.Payload, &p); err != nil {
		return nil, validationErr("bad payload", map[string]any{"reason": "bad_payload"})
	}
	password := rt.Secret("password")
	if p.HasPassword && password == "" {
		return nil, ErrPasswordRequired
	}
	format, ok := ParseFormat(string(p.Format))
	if !ok {
		return nil, validationErr("unknown archive format", map[string]any{"reason": "bad_format"})
	}
	level, ok := ParseLevel(p.Level)
	if !ok {
		return nil, validationErr("unknown level", map[string]any{"reason": "bad_level"})
	}
	dest, err := fsjail.Clean(p.Dest)
	if err != nil {
		return nil, err
	}
	paths := make([]fsjail.VPath, 0, len(p.Paths))
	for _, raw := range p.Paths {
		vp, err := fsjail.Clean(raw)
		if err != nil {
			return nil, err
		}
		paths = append(paths, vp)
	}
	if len(paths) == 0 {
		return nil, validationErr("no sources", map[string]any{"reason": "empty"})
	}
	if format.IsSingle() && len(paths) != 1 {
		return nil, validationErr("this format compresses exactly one file", map[string]any{"reason": "single_file"})
	}
	out, err := fsjail.Join(dest, p.Name)
	if err != nil {
		return nil, err
	}
	j, release, err := rt.Jail(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	unlockR, err := s.acquire(ctx, rt.Task.UserID, fsjail.Read, "compress:"+rt.Task.ID, paths...)
	if err != nil {
		return nil, err
	}
	defer unlockR()
	unlockW, err := s.acquire(ctx, rt.Task.UserID, fsjail.Write, "compress:"+rt.Task.ID, out)
	if err != nil {
		return nil, err
	}
	defer unlockW()
	c := &compression{s: s, rt: rt, j: j, seen: map[string]bool{}}
	// pre-scan: sizes for progress and the quota estimate
	for _, vp := range paths {
		info, err := j.Lstat(vp)
		if err != nil {
			return nil, newErr(CodeNotFound, "source not found", http.StatusNotFound, map[string]any{"path": string(vp)})
		}
		switch {
		case info.IsDir:
			st, err := j.DirSize(ctx, vp, nil)
			if err != nil {
				return nil, err
			}
			c.total += st.Bytes
		case info.IsRegular():
			c.total += info.Size
		}
	}
	rt.SetTotal(c.total)
	var res *quota.Reservation
	if s.o.Quota != nil {
		r, err := s.o.Quota.Reserve(ctx, j, rt.Task.UserID, c.total)
		if err != nil {
			return nil, err
		}
		res = r
	}
	fail := func(err error) (any, error) {
		if res != nil {
			res.Release()
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
	rt.Log("info", "compressing %d item(s) (%d bytes) to %s", len(paths), c.total, p.Name)
	switch {
	case format == FormatSevenZip:
		err = c.write7z(ctx, ws, p, password, paths)
	case format.IsZip():
		err = c.writeZip(ctx, ws, p.Name, level, password, paths)
	case format.IsTar():
		err = c.writeTar(ctx, ws, p.Name, format, level, paths)
	case format.IsSingle():
		err = c.writeSingle(ctx, ws, p.Name, format, level, paths[0])
	default:
		err = unsupportedErr("format cannot be written", map[string]any{"format": string(format)})
	}
	if err != nil {
		return fail(err)
	}
	produced, err := ws.sys.ReadDirNames(outDir)
	if err != nil {
		return fail(err)
	}
	sort.Strings(produced)
	if len(produced) == 0 {
		return fail(newErr(CodeInternal, "no archive produced", http.StatusInternalServerError, nil))
	}
	var size int64
	for _, name := range produced {
		if info, err := ws.sys.Lstat(path.Join(outDir, name)); err == nil {
			size += info.Size()
		}
	}
	// place the volume(s): SPEC §8.11 output conflicts → rename
	c.res.Status = "created"
	for _, name := range produced {
		target, err := fsjail.Join(dest, name)
		if err != nil {
			return fail(err)
		}
		if _, lerr := j.Lstat(target); lerr == nil {
			if target, err = files.UniqueName(j, dest, name, false); err != nil {
				return fail(err)
			}
			c.res.Status = "renamed"
		}
		if err := j.MoveIn(ctx, ws.sys, path.Join(outDir, name), target); err != nil {
			if errors.Is(err, fsjail.ErrExists) {
				if target, err = files.UniqueName(j, dest, name, false); err != nil {
					return fail(err)
				}
				if err := j.MoveIn(ctx, ws.sys, path.Join(outDir, name), target); err != nil {
					return fail(err)
				}
				c.res.Status = "renamed"
			} else {
				return fail(err)
			}
		}
		c.res.Paths = append(c.res.Paths, string(target))
		s.changed(ctx, rt.Task.UserID, target, false)
	}
	c.res.Path = c.res.Paths[0]
	if len(c.res.Paths) == 1 {
		c.res.Paths = nil
	}
	c.res.Size = size
	if res != nil && s.o.Repo != nil {
		if err := res.Commit(ctx, s.o.Repo, size); err != nil {
			s.logger.ErrorContext(ctx, "compress: quota commit failed", slog.String("task", rt.Task.ID), slog.String("error", err.Error()))
			_ = s.o.Quota.MarkDirty(ctx, rt.Task.UserID)
		}
	} else if res != nil {
		res.Release()
	}
	s.activity(ctx, rt.Task.UserID, ActCompress, c.res.Path, "", map[string]any{"format": string(format), "sources": len(paths), "size": size, "skipped": c.warn})
	s.invalidate(rt.Task.UserID, dest)
	rt.Emit(tasks.EventChanged, files.ChangedEvent{Dir: string(dest), Reason: "compress", Paths: []string{c.res.Path}})
	if p.DeleteSource && s.o.Files != nil {
		// to the Recycle Bin, never permanent (SPEC §8.11); the read locks
		// of this task must go first or Delete would wait on itself
		unlockR()
		if r, err := s.o.Files.Delete(ctx, j, rt.Task.UserID, paths); err != nil {
			rt.Log("warn", "sources kept: %s", tail(err.Error(), 160))
		} else {
			c.res.Deleted = len(r.Results)
			dirs := map[string]struct{}{}
			for _, vp := range paths {
				dirs[string(vp.Dir())] = struct{}{}
			}
			for d := range dirs {
				rt.Emit(tasks.EventChanged, files.ChangedEvent{Dir: d, Reason: "delete"})
			}
		}
	}
	rt.Progress(c.total, max(c.total, 1))
	rt.Log("info", "archive %s written (%d bytes, %d files, %d skipped)", c.res.Path, size, c.res.Files, c.warn)
	return c.res, nil
}

// sourceWalker visits the files of every source with their name inside the
// archive (relative to the source's parent folder).
func (c *compression) walkSources(ctx context.Context, paths []fsjail.VPath, fn func(rel string, vp fsjail.VPath, info fsjail.Info) error) error {
	for _, src := range paths {
		info, err := c.j.Lstat(src)
		if err != nil {
			return err
		}
		base := src.Base()
		if !info.IsDir {
			if err := fn(base, src, info); err != nil {
				return err
			}
			continue
		}
		err = c.j.WalkDir(ctx, src, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				c.skip(p, "unreadable")
				return nil
			}
			vp := fsjail.VPath(p)
			rel := base
			if vp != src {
				rel = base + "/" + strings.TrimPrefix(string(vp), string(src)+"/")
			}
			fi, err := c.j.Lstat(vp)
			if err != nil {
				c.skip(p, "unreadable")
				return nil
			}
			return fn(rel, vp, fi)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (c *compression) dedupe(rel string) bool {
	if c.seen[rel] {
		c.skip(rel, ReasonDuplicate)
		return false
	}
	c.seen[rel] = true
	return true
}

// copyFrom streams a source file into w with progress and cancellation.
func (c *compression) copyFrom(ctx context.Context, w io.Writer, vp fsjail.VPath) (int64, error) {
	f, err := c.j.Open(vp)
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 128<<10)
	var n int64
	for {
		m, rerr := f.Read(buf)
		if m > 0 {
			if _, werr := w.Write(buf[:m]); werr != nil {
				return n, werr
			}
			n += int64(m)
			c.read += int64(m)
			c.since += int64(m)
			c.rt.Add(int64(m))
			if c.since >= c.s.step {
				c.since = 0
				if c.rt.Cancelled() {
					return n, context.Canceled
				}
				if err := ctx.Err(); err != nil {
					return n, err
				}
			}
		}
		if rerr == io.EOF {
			return n, nil
		}
		if rerr != nil {
			return n, rerr
		}
	}
}

func flateLevel(level string) int {
	switch level {
	case LevelStore:
		return flate.NoCompression
	case LevelFast:
		return flate.BestSpeed
	case LevelMax:
		return flate.BestCompression
	}
	return flate.DefaultCompression
}

// writeZip writes zip/jar/war with archive/zip; a password switches every
// file to WinZip AES-256 (zipaes) with the real method in the extra field.
func (c *compression) writeZip(ctx context.Context, ws *workspace, name, level, password string, paths []fsjail.VPath) error {
	f, err := ws.sys.Create(path.Join(outDir, name))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	zw := zip.NewWriter(f)
	lvl := flateLevel(level)
	zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) { return flate.NewWriter(w, lvl) })
	method := zip.Deflate
	if level == LevelStore {
		method = zip.Store
	}
	err = c.walkSources(ctx, paths, func(rel string, vp fsjail.VPath, info fsjail.Info) error {
		switch {
		case info.IsDir:
			if !c.dedupe(rel + "/") {
				return nil
			}
			fh := &zip.FileHeader{Name: rel + "/", Method: zip.Store, Modified: info.ModTime}
			fh.SetMode(0o755 | fs.ModeDir)
			if _, err := zw.CreateHeader(fh); err != nil {
				return err
			}
			c.res.Dirs++
			return nil
		case info.IsSymlink:
			c.skip(rel, "symlink")
			return nil
		case !info.IsRegular():
			c.skip(rel, ReasonSpecial)
			return nil
		}
		if !c.dedupe(rel) {
			return nil
		}
		fh := &zip.FileHeader{Name: rel, Method: method, Modified: info.ModTime}
		fh.SetMode(0o644)
		if password == "" {
			w, err := zw.CreateHeader(fh)
			if err != nil {
				return err
			}
			if _, err := c.copyFrom(ctx, w, vp); err != nil {
				return err
			}
			c.res.Files++
			return nil
		}
		fh.Method = zipaes.MethodAES
		fh.Flags |= 0x1 | 0x8 | 0x800
		fh.ReaderVersion = 51
		fh.Extra = zipaes.Extra{Version: zipaes.VersionAE2, Strength: zipaes.AES256, Method: method}.Bytes()
		raw, err := zw.CreateRaw(fh)
		if err != nil {
			return err
		}
		cw := &countWriter{w: raw}
		enc, err := zipaes.NewWriter(cw, password, zipaes.AES256)
		if err != nil {
			return err
		}
		var (
			body io.Writer = enc
			fw   *flate.Writer
		)
		if method == zip.Deflate {
			if fw, err = flate.NewWriter(enc, lvl); err != nil {
				return err
			}
			body = fw
		}
		n, err := c.copyFrom(ctx, body, vp)
		if err != nil {
			return err
		}
		if fw != nil {
			if err := fw.Close(); err != nil {
				return err
			}
		}
		if err := enc.Close(); err != nil {
			return err
		}
		fh.UncompressedSize64 = uint64(n)  //nolint:gosec // n ≥ 0
		fh.CompressedSize64 = uint64(cw.n) //nolint:gosec // n ≥ 0
		fh.CRC32 = 0                       // AE-2
		c.res.Files++
		return nil
	})
	if err != nil {
		return err
	}
	if err := zw.Close(); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	// verify: the central directory must parse (read-only handle)
	rf, err := ws.sys.Open(path.Join(outDir, name))
	if err != nil {
		return err
	}
	defer func() { _ = rf.Close() }()
	st, err := rf.Stat()
	if err != nil {
		return err
	}
	if _, err := zip.NewReader(rf, st.Size()); err != nil {
		return corruptErr("written zip does not verify")
	}
	return nil
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// openCompressor wraps w with the encoder of a compression format.
func openCompressor(c Format, level string, w io.Writer) (io.WriteCloser, error) {
	switch c {
	case FormatGz:
		lvl := flateLevel(level)
		if lvl == flate.NoCompression {
			lvl = flate.BestSpeed
		}
		return pgzip.NewWriterLevel(w, lvl)
	case FormatBz2:
		lvl := 6
		switch level {
		case LevelStore, LevelFast:
			lvl = 1
		case LevelMax:
			lvl = 9
		}
		return bzip2.NewWriter(w, &bzip2.WriterConfig{Level: lvl})
	case FormatXz:
		return xz.NewWriter(w)
	case FormatZst:
		lvl := zstd.SpeedDefault
		switch level {
		case LevelStore, LevelFast:
			lvl = zstd.SpeedFastest
		case LevelMax:
			lvl = zstd.SpeedBestCompression
		}
		return zstd.NewWriter(w, zstd.WithEncoderLevel(lvl), zstd.WithEncoderConcurrency(1))
	}
	return nil, unsupportedErr("unknown compression", map[string]any{"format": string(c)})
}

// writeTar writes tar and tar.* (PAX; symlinks stored as links, never followed).
func (c *compression) writeTar(ctx context.Context, ws *workspace, name string, format Format, level string, paths []fsjail.VPath) error {
	f, err := ws.sys.Create(path.Join(outDir, name))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	var w io.Writer = f
	var comp io.WriteCloser
	if cf := format.Compression(); cf != "" {
		if comp, err = openCompressor(cf, level, f); err != nil {
			return err
		}
		w = comp
	}
	tw := tar.NewWriter(w)
	err = c.walkSources(ctx, paths, func(rel string, vp fsjail.VPath, info fsjail.Info) error {
		hdr := &tar.Header{Name: rel, ModTime: info.ModTime, Format: tar.FormatPAX, Uid: 0, Gid: 0}
		switch {
		case info.IsDir:
			if !c.dedupe(rel + "/") {
				return nil
			}
			hdr.Name, hdr.Typeflag, hdr.Mode = rel+"/", tar.TypeDir, 0o755
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			c.res.Dirs++
			return nil
		case info.IsSymlink:
			if !c.dedupe(rel) {
				return nil
			}
			hdr.Typeflag, hdr.Linkname, hdr.Mode = tar.TypeSymlink, info.SymlinkTarget, 0o777
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			return nil
		case !info.IsRegular():
			c.skip(rel, ReasonSpecial)
			return nil
		}
		if !c.dedupe(rel) {
			return nil
		}
		hdr.Typeflag, hdr.Mode, hdr.Size = tar.TypeReg, 0o644, info.Size
		if info.Mode&0o111 != 0 {
			hdr.Mode = 0o755
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		n, err := c.copyFrom(ctx, tw, vp)
		if err != nil {
			return err
		}
		if n != info.Size {
			// file changed while archiving: pad/truncate to keep the stream valid
			if n < info.Size {
				if _, err := io.CopyN(tw, zeroReader{}, info.Size-n); err != nil {
					return err
				}
			}
			c.skip(rel, "changed_while_reading")
		}
		c.res.Files++
		return nil
	})
	if err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if comp != nil {
		if err := comp.Close(); err != nil {
			return err
		}
	}
	return f.Sync()
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// writeSingle compresses one regular file (gz/bz2/xz/zst).
func (c *compression) writeSingle(ctx context.Context, ws *workspace, name string, format Format, level string, src fsjail.VPath) error {
	info, err := c.j.Lstat(src)
	if err != nil {
		return err
	}
	if !info.IsRegular() {
		return validationErr("this format compresses exactly one file", map[string]any{"reason": "single_file", "path": string(src)})
	}
	f, err := ws.sys.Create(path.Join(outDir, name))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	comp, err := openCompressor(format, level, f)
	if err != nil {
		return err
	}
	if _, err := c.copyFrom(ctx, comp, src); err != nil {
		_ = comp.Close()
		return err
	}
	if err := comp.Close(); err != nil {
		return err
	}
	c.res.Files = 1
	return f.Sync()
}

// write7z creates a 7z archive with the CLI; password via stdin (-p without
// value), -mhe=on encrypts the names, -v splits volumes.
func (c *compression) write7z(ctx context.Context, ws *workspace, p CompressPayload, password string, paths []fsjail.VPath) error {
	outHost, err := ws.sys.HostPath(outDir)
	if err != nil {
		return err
	}
	args := []string{"a", "-t7z", level7z(p.Level), "-bd", "-y", "-bsp1", "-bso0", "-snl"}
	if password != "" {
		args = append(args, "-p")
		if p.EncryptNames {
			args = append(args, "-mhe=on")
		}
	}
	if p.SplitMB > 0 {
		args = append(args, "-v"+strconv.Itoa(p.SplitMB)+"m")
	}
	args = append(args, "--", path.Join(outHost, p.Name))
	for _, vp := range paths {
		host, err := c.j.AbsForCLI(vp)
		if err != nil {
			return err
		}
		args = append(args, host)
	}
	total := c.total
	_, err = c.s.run7z(ctx, args, password, outHost, func(pct int) {
		c.rt.Progress(total*int64(pct)/100, total)
	})
	if err != nil {
		return err
	}
	// counts for the result (7z reports no per-file list in -bso0 mode)
	_ = c.walkSources(ctx, paths, func(rel string, vp fsjail.VPath, info fsjail.Info) error {
		if info.IsDir {
			c.res.Dirs++
		} else {
			c.res.Files++
		}
		return nil
	})
	return nil
}
