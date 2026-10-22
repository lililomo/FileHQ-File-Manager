package fsjail

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Default modes (SPEC §3, §8.11): files 0640, directories 0750 (umask 0027
// narrows anything wider).
const (
	FileMode fs.FileMode = 0o640
	DirMode  fs.FileMode = 0o750
)

var usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{2,31}$`)

// Jail wraps one *os.Root: a user's home or a sub-root of it (shares).
type Jail struct {
	root     *os.Root
	base     VPath  // for sub-roots: where this jail sits inside the parent (informational)
	userID   string // owner of the files (empty for generic Open)
	readOnly bool
	shared   bool   // root is owned by a parent jail (SubRoot("/")); Close must not close it
	hostPath string // absolute host directory; only ever exposed by AbsForCLI
	renameFn func(oldpath, newpath string) error
}

// Open opens an arbitrary directory as a jail. Production code uses OpenUser;
// Open exists for tests and tooling.
func Open(absRoot string) (*Jail, error) {
	absRoot = filepath.Clean(absRoot)
	r, err := os.OpenRoot(absRoot)
	if err != nil {
		return nil, wrap("open_root", Root, err)
	}
	return newJail(r, Root, "", false, absRoot), nil
}

// OpenUser opens STORAGE_ROOT/<username> (SPEC §5.3). The home must already
// exist (ErrHomeMissing otherwise) and must be a real directory, never a
// symlink (ErrHomeNotDir): a link in STORAGE_ROOT could otherwise alias
// another user's home.
func OpenUser(storageRoot, username, userID string) (*Jail, error) {
	if !usernameRe.MatchString(username) {
		return nil, newErr(CodePathInvalid, "open_user", Root, "username", ErrInvalidPath)
	}
	storageRoot = filepath.Clean(storageRoot)
	sr, err := os.OpenRoot(storageRoot)
	if err != nil {
		return nil, wrap("open_storage_root", Root, err)
	}
	defer func() { _ = sr.Close() }()
	info, err := sr.Lstat(username)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil, ErrHomeMissing
	case err != nil:
		return nil, wrap("stat_home", Root, err)
	case !info.IsDir():
		return nil, ErrHomeNotDir
	}
	r, err := sr.OpenRoot(username)
	if err != nil {
		return nil, wrap("open_home", Root, err)
	}
	return newJail(r, Root, userID, false, filepath.Join(storageRoot, username)), nil
}

func newJail(r *os.Root, base VPath, userID string, readOnly bool, hostPath string) *Jail {
	return &Jail{root: r, base: base, userID: userID, readOnly: readOnly, hostPath: hostPath, renameFn: r.Rename}
}

// SubRoot opens dir as its own jail (share folders, SPEC §5.4). Paths given
// to the sub-jail are relative to dir; symlinks that leave dir are escapes
// even when they stay inside the parent root.
func (j *Jail) SubRoot(dir VPath, readOnly bool) (*Jail, error) {
	if err := j.check(dir); err != nil {
		return nil, err
	}
	if dir.IsRoot() {
		sub := newJail(j.root, j.base, j.userID, readOnly || j.readOnly, j.hostPath)
		sub.shared = true
		return sub, nil
	}
	info, err := j.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if info.IsSymlink {
		return nil, newErr(CodeSymlinkEscape, "sub_root", dir, "symlink", ErrEscape)
	}
	if !info.IsDir {
		return nil, newErr(CodeNotDir, "sub_root", dir, "not_dir", ErrNotDir)
	}
	r, err := j.root.OpenRoot(dir.Rel())
	if err != nil {
		return nil, wrap("sub_root", dir, err)
	}
	base := dir
	if !j.base.IsRoot() {
		base = VPath(string(j.base) + string(dir))
	}
	return newJail(r, base, j.userID, readOnly || j.readOnly, filepath.Join(j.hostPath, filepath.FromSlash(dir.Rel()))), nil
}

// Sub is SubRoot(dir, false) (name used by PROMPT Step 4).
func (j *Jail) Sub(dir VPath) (*Jail, error) { return j.SubRoot(dir, false) }

// Close releases the directory handle. A sub-root that shares the parent's
// handle (SubRoot("/")) only forgets it: the parent stays usable.
func (j *Jail) Close() error {
	if j.root == nil {
		return nil
	}
	if j.shared {
		j.root = nil
		return nil
	}
	err := j.root.Close()
	j.root = nil
	if err != nil && !errors.Is(err, fs.ErrClosed) {
		return wrap("close", Root, err)
	}
	return nil
}

// UserID returns the owner id this jail was opened for.
func (j *Jail) UserID() string { return j.userID }

// ReadOnly reports whether mutating operations are refused.
func (j *Jail) ReadOnly() bool { return j.readOnly }

// Base returns the position of this jail inside the user's root ("/" for the
// home itself).
func (j *Jail) Base() VPath { return j.base }

// Abs maps a path of this jail to the owner's root: identity for the home
// jail, base + p for a sub-root (Step 18b: bookkeeping keyed by the owner's
// absolute path — trash, versions, activity, locks, thumbnails — while a
// recipient works inside a share sub-root).
func (j *Jail) Abs(p VPath) VPath {
	if j.base.IsRoot() {
		if p.IsRoot() {
			return Root
		}
		return p
	}
	if p.IsRoot() {
		return j.base
	}
	return VPath(string(j.base) + string(p))
}

// RelOf is the inverse of Abs: the position of an owner-absolute path inside
// this jail. ok=false when abs lies outside the sub-root.
func (j *Jail) RelOf(abs VPath) (VPath, bool) {
	if j.base.IsRoot() {
		return abs, true
	}
	if abs == j.base {
		return Root, true
	}
	if !abs.HasPrefix(j.base) {
		return "", false
	}
	return VPath(strings.TrimPrefix(string(abs), string(j.base))), true
}

// check validates p (see checkVPath) and refuses use after Close with
// ErrClosed instead of dereferencing a nil root (review A).
func (j *Jail) check(p VPath) error {
	if j.root == nil {
		return newErr(CodeInternal, "jail", p, "closed", ErrClosed)
	}
	return checkVPath(p)
}

func (j *Jail) mutable(op string, p VPath) error {
	if j.readOnly {
		return newErr(CodeForbidden, op, p, "read_only", ErrReadOnly)
	}
	return nil
}

// Open opens p read-only. Symlinks are followed only inside the root; an
// escaping link yields SYMLINK_ESCAPE.
func (j *Jail) Open(p VPath) (*os.File, error) {
	if err := j.check(p); err != nil {
		return nil, err
	}
	f, err := j.root.Open(p.Rel())
	if err != nil {
		return nil, wrap("open", p, err)
	}
	return f, nil
}

const writeFlags = os.O_WRONLY | os.O_RDWR | os.O_CREATE | os.O_TRUNC | os.O_APPEND

// OpenFile is the general form. Write flags are refused on read-only jails.
// perm is restricted to rwx bits (never setuid/setgid/sticky).
func (j *Jail) OpenFile(p VPath, flag int, perm fs.FileMode) (*os.File, error) {
	if err := j.check(p); err != nil {
		return nil, err
	}
	if flag&writeFlags != 0 {
		if err := j.mutable("open_file", p); err != nil {
			return nil, err
		}
	}
	if p.IsRoot() {
		return nil, newErr(CodeIsDir, "open_file", p, "is_dir", ErrIsDir)
	}
	f, err := j.root.OpenFile(p.Rel(), flag, perm&0o777)
	if err != nil {
		return nil, wrap("open_file", p, err)
	}
	return f, nil
}

// Create creates (or truncates when excl is false) a regular file with mode
// 0640. The final component is never followed through a symlink
// (O_NOFOLLOW), so a link can not redirect a write.
func (j *Jail) Create(p VPath, excl bool) (*os.File, error) {
	if err := j.check(p); err != nil {
		return nil, err
	}
	if err := j.mutable("create", p); err != nil {
		return nil, err
	}
	if p.IsRoot() {
		return nil, newErr(CodeIsDir, "create", p, "is_dir", ErrIsDir)
	}
	flag := unix.O_WRONLY | unix.O_CREAT | unix.O_TRUNC | unix.O_NOFOLLOW | unix.O_CLOEXEC
	if excl {
		flag |= unix.O_EXCL
	}
	// os.Root resolves the final symlink itself, so O_NOFOLLOW must be
	// applied by a single-component openat on the parent handle (which the
	// root opened, hence inside the jail): race-free and never through a link.
	df, err := j.root.Open(p.Dir().Rel())
	if err != nil {
		return nil, wrap("create", p, err)
	}
	defer func() { _ = df.Close() }()
	var fd int
	for {
		fd, err = unix.Openat(int(df.Fd()), p.Base(), flag, uint32(FileMode))
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}
	if err != nil {
		perr := &fs.PathError{Op: "openat", Path: p.Rel(), Err: err}
		if errors.Is(err, unix.ELOOP) {
			return nil, newErr(CodeSymlinkEscape, "create", p, "symlink", perr)
		}
		return nil, wrap("create", p, perr)
	}
	return os.NewFile(uintptr(fd), p.Rel()), nil
}

// Mkdir creates one directory (0750). The parent must exist.
func (j *Jail) Mkdir(p VPath) error {
	if err := j.check(p); err != nil {
		return err
	}
	if err := j.mutable("mkdir", p); err != nil {
		return err
	}
	if p.IsRoot() {
		return newErr(CodeAlreadyExists, "mkdir", p, "", ErrExists)
	}
	return wrap("mkdir", p, j.root.Mkdir(p.Rel(), DirMode))
}

// MkdirAll creates p and any missing parents (0750); existing dirs are kept.
func (j *Jail) MkdirAll(p VPath) error {
	if err := j.check(p); err != nil {
		return err
	}
	if err := j.mutable("mkdir_all", p); err != nil {
		return err
	}
	if p.IsRoot() {
		return nil
	}
	return wrap("mkdir_all", p, j.root.MkdirAll(p.Rel(), DirMode))
}

// Rename moves old to new atomically inside the root (rename(2) semantics:
// an existing empty directory or file at new is replaced).
func (j *Jail) Rename(oldp, newp VPath) error {
	if err := j.check(oldp); err != nil {
		return err
	}
	if err := j.check(newp); err != nil {
		return err
	}
	if err := j.mutable("rename", oldp); err != nil {
		return err
	}
	if oldp.IsRoot() || newp.IsRoot() {
		return newErr(CodePathInvalid, "rename", oldp, "root", ErrInvalidPath)
	}
	if newp.HasPrefix(oldp) && newp != oldp {
		return newErr(CodePathInvalid, "rename", oldp, "into_itself", ErrInvalidPath)
	}
	return wrap("rename", oldp, j.renameFn(oldp.Rel(), newp.Rel()))
}

// Remove deletes a file, symlink (the link itself) or empty directory.
func (j *Jail) Remove(p VPath) error {
	if err := j.check(p); err != nil {
		return err
	}
	if err := j.mutable("remove", p); err != nil {
		return err
	}
	if p.IsRoot() {
		return newErr(CodePathInvalid, "remove", p, "root", ErrInvalidPath)
	}
	return wrap("remove", p, j.root.Remove(p.Rel()))
}

// RemoveAll deletes p recursively. A symlink is removed as a link; its target
// is never entered.
func (j *Jail) RemoveAll(p VPath) error {
	if err := j.check(p); err != nil {
		return err
	}
	if err := j.mutable("remove_all", p); err != nil {
		return err
	}
	if p.IsRoot() {
		return newErr(CodePathInvalid, "remove_all", p, "root", ErrInvalidPath)
	}
	return wrap("remove_all", p, j.root.RemoveAll(p.Rel()))
}

// Symlink creates link -> target. target must be relative and resolve inside
// the root when computed from link's directory (SPEC §5.4); dangling targets
// are allowed.
func (j *Jail) Symlink(target string, link VPath) error {
	if err := j.check(link); err != nil {
		return err
	}
	if err := j.mutable("symlink", link); err != nil {
		return err
	}
	if link.IsRoot() {
		return newErr(CodePathInvalid, "symlink", link, "root", ErrInvalidPath)
	}
	if target == "" || strings.ContainsRune(target, 0) || len(target) > MaxPathBytes {
		return newErr(CodePathInvalid, "symlink", link, "target", ErrInvalidPath)
	}
	for _, r := range target {
		if r < 0x20 || r == 0x7f {
			return newErr(CodePathInvalid, "symlink", link, "target", ErrInvalidPath)
		}
	}
	if _, ok := resolveLink(link.Dir(), target); !ok {
		return newErr(CodeSymlinkEscape, "symlink", link, "escape", ErrEscape)
	}
	return wrap("symlink", link, j.root.Symlink(target, link.Rel()))
}

// Readlink returns the raw target of a symlink.
func (j *Jail) Readlink(p VPath) (string, error) {
	if err := j.check(p); err != nil {
		return "", err
	}
	t, err := j.root.Readlink(p.Rel())
	if err != nil {
		return "", wrap("readlink", p, err)
	}
	return t, nil
}

// Chmod sets the rwx bits of p (setuid/setgid/sticky are always dropped).
// Symlinks are never followed (CVE-2026-32282 class): chmod on a link is a
// no-op. Uses fchmodat2 with AT_SYMLINK_NOFOLLOW (kernel >= 6.6); older
// kernels fall back to lstat + chmod.
func (j *Jail) Chmod(p VPath, mode fs.FileMode) error {
	if err := j.check(p); err != nil {
		return err
	}
	if err := j.mutable("chmod", p); err != nil {
		return err
	}
	mode &= 0o777
	if p.IsRoot() {
		f, err := j.root.Open(".")
		if err != nil {
			return wrap("chmod", p, err)
		}
		defer func() { _ = f.Close() }()
		return wrap("chmod", p, f.Chmod(mode))
	}
	df, err := j.root.Open(p.Dir().Rel())
	if err != nil {
		return wrap("chmod", p, err)
	}
	defer func() { _ = df.Close() }()
	err = unix.Fchmodat(int(df.Fd()), p.Base(), uint32(mode), unix.AT_SYMLINK_NOFOLLOW)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, unix.EOPNOTSUPP), errors.Is(err, unix.ENOTSUP):
		return nil // p is a symlink: policy says do not follow
	case errors.Is(err, unix.ENOSYS), errors.Is(err, unix.EINVAL):
		info, lerr := j.Lstat(p)
		if lerr != nil {
			return lerr
		}
		if info.IsSymlink {
			return nil
		}
		return wrap("chmod", p, j.root.Chmod(p.Rel(), mode))
	}
	return wrap("chmod", p, &fs.PathError{Op: "fchmodat", Path: p.Rel(), Err: err})
}

// ChmodRecursive applies fileMode to files and dirMode to directories under
// p (including p). Symlinks are skipped. progress receives the running count.
func (j *Jail) ChmodRecursive(ctx context.Context, p VPath, fileMode, dirMode fs.FileMode, progress func(n int)) error {
	if err := j.check(p); err != nil {
		return err
	}
	if err := j.mutable("chmod_recursive", p); err != nil {
		return err
	}
	n := 0
	return j.WalkDir(ctx, p, func(vp string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		mode := fileMode
		if d.IsDir() {
			mode = dirMode
		}
		if cerr := j.Chmod(VPath(vp), mode); cerr != nil {
			return cerr
		}
		n++
		if progress != nil && n%200 == 0 {
			progress(n)
		}
		return nil
	})
}

// Chtimes sets access and modification times.
func (j *Jail) Chtimes(p VPath, atime, mtime time.Time) error {
	if err := j.check(p); err != nil {
		return err
	}
	if err := j.mutable("chtimes", p); err != nil {
		return err
	}
	return wrap("chtimes", p, j.root.Chtimes(p.Rel(), atime, mtime))
}

// WalkDir walks p depth-first through os.Root.FS. Directory symlinks are not
// followed (they appear as symlink entries). fn receives virtual paths. The
// walk stops when ctx is done or fn returns an error (fs.SkipDir honored).
func (j *Jail) WalkDir(ctx context.Context, p VPath, fn fs.WalkDirFunc) error {
	if err := j.check(p); err != nil {
		return err
	}
	fsys := j.root.FS()
	start := p.Rel()
	err := fs.WalkDir(fsys, start, func(rel string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		vp := relToVPath(rel)
		if err != nil {
			err = wrap("walk", vp, err)
		}
		return fn(string(vp), d, err)
	})
	if err != nil && !errors.Is(err, fs.SkipDir) && !errors.Is(err, fs.SkipAll) {
		return wrap("walk", p, err)
	}
	return nil
}

func relToVPath(rel string) VPath {
	rel = path.Clean(rel)
	if rel == "." || rel == "" {
		return Root
	}
	return VPath("/" + rel)
}

// DirStats is the result of DirSize.
type DirStats struct {
	Files int64
	Dirs  int64 // excluding the top directory
	Bytes int64 // regular files + symlink sizes, not what links point to
}

// DirSize sums sizes below p without following symlinks (SPEC §5.4).
func (j *Jail) DirSize(ctx context.Context, p VPath, progress func(files, dirs int64, bytes int64)) (DirStats, error) {
	if err := j.check(p); err != nil {
		return DirStats{}, err
	}
	var st DirStats
	n := 0
	err := j.WalkDir(ctx, p, func(vp string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if VPath(vp) == p {
			return nil
		}
		if d.IsDir() {
			st.Dirs++
		} else {
			info, ierr := d.Info()
			if ierr != nil {
				if errors.Is(ierr, fs.ErrNotExist) {
					return nil // vanished during the walk
				}
				return wrap("dir_size", VPath(vp), ierr)
			}
			st.Files++
			st.Bytes += info.Size()
		}
		n++
		if progress != nil && n%500 == 0 {
			progress(st.Files, st.Dirs, st.Bytes)
		}
		return nil
	})
	return st, err
}

// FS exposes the root as an fs.FS (name search, ripgrep fallback). It is
// read-only and symlink-escape safe by construction. A closed jail yields
// an empty filesystem.
func (j *Jail) FS() fs.FS {
	if j.root == nil {
		return closedFS{}
	}
	return j.root.FS()
}

type closedFS struct{}

func (closedFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrClosed}
}

// Statfs reports free and total bytes of the filesystem holding the root.
func (j *Jail) Statfs() (free, total uint64, err error) {
	if err := j.check(Root); err != nil {
		return 0, 0, err
	}
	f, err := j.root.Open(".")
	if err != nil {
		return 0, 0, wrap("statfs", Root, err)
	}
	defer func() { _ = f.Close() }()
	var st unix.Statfs_t
	if err := unix.Fstatfs(int(f.Fd()), &st); err != nil {
		return 0, 0, wrap("statfs", Root, &fs.PathError{Op: "fstatfs", Path: ".", Err: err})
	}
	bs := uint64(max(st.Bsize, 0)) //nolint:gosec,unconvert // block size is non-negative; int64 on linux/amd64
	return st.Bavail * bs, st.Blocks * bs, nil
}

// AbsForCLI returns the host path of p for an external tool (ffmpeg, 7zz).
// It is only valid while the caller holds a lock on p, and only after this
// check: no component of p may be a symlink, otherwise the tool (which does
// not use the root) could be redirected. The result must never reach a
// client.
func (j *Jail) AbsForCLI(p VPath) (string, error) {
	if err := j.check(p); err != nil {
		return "", err
	}
	segs := p.Segments()
	cur := Root
	for _, s := range segs {
		cur = VPath(strings.TrimSuffix(string(cur), "/") + "/" + s)
		info, err := j.Lstat(cur)
		if err != nil {
			return "", err
		}
		if info.IsSymlink {
			return "", newErr(CodeSymlinkEscape, "abs_for_cli", cur, "symlink_component", ErrEscape)
		}
	}
	return filepath.Join(j.hostPath, filepath.FromSlash(p.Rel())), nil
}

// Redact replaces the jail's host directory in s (error text from an
// external tool, which prints the absolute path it was given) with "<root>"
// so stored or logged messages never carry STORAGE_ROOT (review B, G-1).
func (j *Jail) Redact(s string) string {
	if j == nil || j.hostPath == "" || s == "" {
		return s
	}
	return strings.ReplaceAll(s, j.hostPath, "<root>")
}

// HostPath is an alias of AbsForCLI (name used in SPEC §8.4).
func (j *Jail) HostPath(p VPath) (string, error) { return j.AbsForCLI(p) }
