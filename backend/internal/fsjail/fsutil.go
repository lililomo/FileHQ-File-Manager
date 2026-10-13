package fsjail

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"syscall"

	"golang.org/x/sys/unix"
)

// ProgressFn receives the number of bytes copied so far by one operation.
type ProgressFn func(bytes int64)

const (
	copyChunk      = 4 << 20 // copy_file_range request size
	fallbackBuffer = 1 << 20 // io.CopyBuffer size when copy_file_range is unavailable
)

// CopyFile copies a regular file src to a new file dst (fails with
// ALREADY_EXISTS if dst exists). Mode is src's rwx bits (never setuid),
// mtime is preserved. Uses copy_file_range with an io.Copy fallback.
func (j *Jail) CopyFile(ctx context.Context, src, dst VPath, progress ProgressFn) error {
	if err := j.check(src); err != nil {
		return err
	}
	if err := j.check(dst); err != nil {
		return err
	}
	if err := j.mutable("copy_file", dst); err != nil {
		return err
	}
	if src.IsRoot() || dst.IsRoot() {
		return newErr(CodeIsDir, "copy_file", src, "is_dir", ErrIsDir)
	}
	return copyFileRoots(ctx, j.root, src.Rel(), j.root, dst.Rel(), src, dst, progress)
}

func copyFileRoots(ctx context.Context, sr *os.Root, srel string, dr *os.Root, drel string, sv, dv VPath, progress ProgressFn) error {
	in, err := sr.Open(srel)
	if err != nil {
		return wrap("copy_file", sv, err)
	}
	defer func() { _ = in.Close() }()
	st, err := in.Stat()
	if err != nil {
		return wrap("copy_file", sv, err)
	}
	if st.IsDir() {
		return newErr(CodeIsDir, "copy_file", sv, "is_dir", ErrIsDir)
	}
	if !st.Mode().IsRegular() {
		return newErr(CodePathInvalid, "copy_file", sv, "not_regular", ErrInvalidPath)
	}
	mode := st.Mode().Perm() & 0o777
	if mode == 0 {
		mode = FileMode
	}
	out, err := dr.OpenFile(drel, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, mode)
	if err != nil {
		return wrap("copy_file", dv, err)
	}
	cleanup := func(cause error) error {
		_ = out.Close()
		_ = dr.Remove(drel)
		return cause
	}
	if err := copyContent(ctx, in, out, st.Size(), progress); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return cleanup(err)
		}
		return cleanup(wrap("copy_file", dv, err))
	}
	if err := out.Close(); err != nil {
		_ = dr.Remove(drel)
		return wrap("copy_file", dv, err)
	}
	_ = dr.Chtimes(drel, st.ModTime(), st.ModTime())
	return nil
}

// copyContent streams in → out, honoring ctx between chunks.
func copyContent(ctx context.Context, in, out *os.File, size int64, progress ProgressFn) error {
	var done int64
	report := func() {
		if progress != nil {
			progress(done)
		}
	}
	useCFR := true
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if useCFR {
			n, err := unix.CopyFileRange(int(in.Fd()), nil, int(out.Fd()), nil, copyChunk, 0)
			switch {
			case err == nil && n == 0:
				report()
				return nil
			case err == nil:
				done += int64(n)
				report()
				continue
			case errors.Is(err, unix.EINTR):
				continue
			case errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EXDEV), errors.Is(err, unix.EINVAL),
				errors.Is(err, unix.EOPNOTSUPP), errors.Is(err, unix.EPERM):
				if done != 0 {
					return err // partial copy then failure: do not silently switch mid-stream
				}
				useCFR = false
				continue
			default:
				return err
			}
		}
		buf := make([]byte, fallbackBuffer)
		for {
			if err := ctx.Err(); err != nil {
				return err
			}
			n, rerr := in.Read(buf)
			if n > 0 {
				if _, werr := out.Write(buf[:n]); werr != nil {
					return werr
				}
				done += int64(n)
				report()
			}
			if rerr == io.EOF {
				return nil
			}
			if rerr != nil {
				return rerr
			}
		}
	}
}

// CopyTree copies src (file, symlink or directory) to dst, which must not
// exist. Symlinks are copied as links with their raw target (SPEC §5.4);
// devices, FIFOs and sockets are skipped. Everything goes through the root,
// so a directory swapped for an escaping symlink mid-copy fails with
// SYMLINK_ESCAPE and nothing outside the root is read.
func (j *Jail) CopyTree(ctx context.Context, src, dst VPath, progress ProgressFn) error {
	if err := j.check(src); err != nil {
		return err
	}
	if err := j.check(dst); err != nil {
		return err
	}
	if err := j.mutable("copy_tree", dst); err != nil {
		return err
	}
	if dst.HasPrefix(src) {
		return newErr(CodePathInvalid, "copy_tree", dst, "into_itself", ErrInvalidPath)
	}
	if dst.IsRoot() {
		return newErr(CodeAlreadyExists, "copy_tree", dst, "", ErrExists)
	}
	var total int64
	return copyTreeRoots(ctx, j.root, src.Rel(), j.root, dst.Rel(), src, dst, &total, progress)
}

func copyTreeRoots(ctx context.Context, sr *os.Root, srel string, dr *os.Root, drel string, sv, dv VPath, total *int64, progress ProgressFn) error {
	// An entry can change type between Lstat and the next call when the
	// tree is modified concurrently (Readlink on something that became a
	// directory returns EINVAL). Re-stat a few times; escapes are still
	// caught by the root on every access.
	for attempt := 0; ; attempt++ {
		err := copyEntry(ctx, sr, srel, dr, drel, sv, dv, total, progress)
		if err != nil && attempt < 3 && errors.Is(err, unix.EINVAL) {
			continue
		}
		return err
	}
}

func copyEntry(ctx context.Context, sr *os.Root, srel string, dr *os.Root, drel string, sv, dv VPath, total *int64, progress ProgressFn) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	info, err := sr.Lstat(srel)
	if err != nil {
		return wrap("copy_tree", sv, err)
	}
	mode := info.Mode()
	switch {
	case mode&fs.ModeSymlink != 0:
		target, err := sr.Readlink(srel)
		if err != nil {
			return wrap("copy_tree", sv, err)
		}
		return wrap("copy_tree", dv, dr.Symlink(target, drel))
	case mode.IsRegular():
		base := *total
		err := copyFileRoots(ctx, sr, srel, dr, drel, sv, dv, func(b int64) {
			*total = base + b
			if progress != nil {
				progress(*total)
			}
		})
		if err != nil {
			return err
		}
		*total = base + info.Size()
		return nil
	case mode.IsDir():
		if err := dr.Mkdir(drel, (mode.Perm()&0o777)|0o700); err != nil {
			return wrap("copy_tree", dv, err)
		}
		names, err := readDirNames(sr, srel)
		if err != nil {
			return wrap("copy_tree", sv, err)
		}
		for _, name := range names {
			if err := copyTreeRoots(ctx, sr, path.Join(srel, name), dr, path.Join(drel, name),
				childPath(sv, name), childPath(dv, name), total, progress); err != nil {
				return err
			}
		}
		_ = dr.Chtimes(drel, info.ModTime(), info.ModTime())
		return nil
	default:
		return nil // device, fifo, socket: skipped by policy
	}
}

func childPath(p VPath, name string) VPath {
	if p.IsRoot() {
		return VPath("/" + name)
	}
	return VPath(string(p) + "/" + name)
}

func readDirNames(r *os.Root, rel string) ([]string, error) {
	f, err := r.Open(rel)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	return f.Readdirnames(-1)
}

// MoveTree renames src to dst; when the rename crosses a filesystem (EXDEV)
// it copies to dst + ".fm-partial-<id>", renames into place and removes src.
// On failure the partial copy is removed and src is left untouched.
func (j *Jail) MoveTree(ctx context.Context, src, dst VPath, progress ProgressFn) error {
	if err := j.check(src); err != nil {
		return err
	}
	if err := j.check(dst); err != nil {
		return err
	}
	if err := j.mutable("move_tree", dst); err != nil {
		return err
	}
	if src.IsRoot() || dst.IsRoot() {
		return newErr(CodePathInvalid, "move_tree", src, "root", ErrInvalidPath)
	}
	if dst.HasPrefix(src) {
		return newErr(CodePathInvalid, "move_tree", dst, "into_itself", ErrInvalidPath)
	}
	err := j.renameFn(src.Rel(), dst.Rel())
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.EXDEV) {
		return wrap("move_tree", src, err)
	}
	partial := VPath(string(dst) + ".fm-partial-" + randomSuffix())
	var total int64
	if cerr := copyTreeRoots(ctx, j.root, src.Rel(), j.root, partial.Rel(), src, partial, &total, progress); cerr != nil {
		_ = j.root.RemoveAll(partial.Rel())
		return cerr
	}
	if rerr := j.renameFn(partial.Rel(), dst.Rel()); rerr != nil {
		_ = j.root.RemoveAll(partial.Rel())
		return wrap("move_tree", dst, rerr)
	}
	return wrap("move_tree", src, j.root.RemoveAll(src.Rel()))
}

func randomSuffix() string {
	var b [6]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// splitRel returns the directory and base of a root-relative path.
func splitRel(rel string) (dir, base string) {
	dir, base = path.Split(rel)
	dir = path.Clean(dir)
	if dir == "" {
		dir = "."
	}
	return dir, base
}

// renameAcross renames sr/srel to dr/drel with renameat(2) on directory
// handles obtained from both roots (single-component names, no traversal).
func renameAcross(sr *os.Root, srel string, dr *os.Root, drel string) error {
	sdir, sbase := splitRel(srel)
	ddir, dbase := splitRel(drel)
	if sbase == "" || dbase == "" || sbase == "." || dbase == "." {
		return &fs.PathError{Op: "renameat", Path: srel, Err: syscall.EINVAL}
	}
	sf, err := sr.Open(sdir)
	if err != nil {
		return err
	}
	defer func() { _ = sf.Close() }()
	df, err := dr.Open(ddir)
	if err != nil {
		return err
	}
	defer func() { _ = df.Close() }()
	if err := unix.Renameat2(int(sf.Fd()), sbase, int(df.Fd()), dbase, unix.RENAME_NOREPLACE); err != nil {
		return &fs.PathError{Op: "renameat", Path: srel, Err: err}
	}
	return nil
}

// renameAcrossFn is swapped by tests to simulate EXDEV.
var renameAcrossFn = renameAcross

// moveAcross moves between two roots: renameat, EXDEV → copy + remove.
func moveAcross(ctx context.Context, sr *os.Root, srel string, dr *os.Root, drel string, sv, dv VPath, progress ProgressFn) error {
	err := renameAcrossFn(sr, srel, dr, drel)
	if err == nil {
		return nil
	}
	if !errors.Is(err, syscall.EXDEV) {
		return wrap("move", sv, err)
	}
	partial := drel + ".fm-partial-" + randomSuffix()
	var total int64
	if cerr := copyTreeRoots(ctx, sr, srel, dr, partial, sv, dv, &total, progress); cerr != nil {
		_ = dr.RemoveAll(partial)
		return cerr
	}
	if rerr := dr.Rename(partial, drel); rerr != nil {
		_ = dr.RemoveAll(partial)
		return wrap("move", dv, rerr)
	}
	return wrap("move", sv, sr.RemoveAll(srel))
}

// MoveOut moves src from the jail into sys under name (trash, versions).
func (j *Jail) MoveOut(ctx context.Context, src VPath, sys *SysDir, name string) error {
	if err := j.check(src); err != nil {
		return err
	}
	if err := j.mutable("move_out", src); err != nil {
		return err
	}
	if src.IsRoot() {
		return newErr(CodePathInvalid, "move_out", src, "root", ErrInvalidPath)
	}
	rel, err := sys.rel(name)
	if err != nil {
		return err
	}
	return moveAcross(ctx, j.root, src.Rel(), sys.root, rel, src, VPath("/"+rel), nil)
}

// MoveIn moves sys/name into the jail at dst (restore, upload finalize,
// extract). dst must not exist.
func (j *Jail) MoveIn(ctx context.Context, sys *SysDir, name string, dst VPath) error {
	if err := j.check(dst); err != nil {
		return err
	}
	if err := j.mutable("move_in", dst); err != nil {
		return err
	}
	if dst.IsRoot() {
		return newErr(CodePathInvalid, "move_in", dst, "root", ErrInvalidPath)
	}
	rel, err := sys.rel(name)
	if err != nil {
		return err
	}
	return moveAcross(ctx, sys.root, rel, j.root, dst.Rel(), VPath("/"+rel), dst, nil)
}
