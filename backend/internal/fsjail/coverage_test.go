package fsjail

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestWrapClassification(t *testing.T) {
	pe := func(e error) error { return &fs.PathError{Op: "x", Path: "p", Err: e} }
	cases := []struct {
		err  error
		code Code
	}{
		{pe(syscall.ENOENT), CodeNotFound},
		{pe(syscall.ENOTDIR), CodeNotFound},
		{pe(syscall.EEXIST), CodeAlreadyExists},
		{pe(syscall.ENOTEMPTY), CodeNotEmpty},
		{pe(syscall.EISDIR), CodeIsDir},
		{pe(syscall.EACCES), CodeForbidden},
		{pe(syscall.EPERM), CodeForbidden},
		{pe(syscall.EROFS), CodeForbidden},
		{pe(syscall.ENAMETOOLONG), CodePathInvalid},
		{pe(syscall.ELOOP), CodeSymlinkEscape},
		{pe(errors.New("openat a/b: path escapes from parent")), CodeSymlinkEscape},
		{errors.New("path escapes"), CodeSymlinkEscape},
		{pe(syscall.EIO), CodeInternal},
	}
	for _, c := range cases {
		err := wrap("op", "/p", c.err)
		var fe *Error
		if !errors.As(err, &fe) || fe.Code != c.code {
			t.Errorf("wrap(%v) = %v; want %s", c.err, err, c.code)
		}
	}
	if wrap("op", "/p", nil) != nil {
		t.Error("nil passthrough")
	}
	already := newErr(CodeNotFound, "a", "/p", "", nil)
	if !errors.Is(wrap("op", "/p", already), already) {
		t.Error("already wrapped errors pass through")
	}
	if !isEscape(pe(syscall.ELOOP)) || isEscape(nil) || isEscape(pe(syscall.ENOENT)) {
		t.Error("isEscape")
	}
	e := newErr(CodeAlreadyExists, "a", "/p", "", pe(syscall.EEXIST))
	if !errors.Is(e, fs.ErrExist) || errors.Is(e, fs.ErrNotExist) {
		t.Error("Is fs.ErrExist")
	}
	f := newErr(CodeForbidden, "a", "/p", "", nil)
	if !errors.Is(f, fs.ErrPermission) || errors.Is(f, errors.New("x")) {
		t.Error("Is fs.ErrPermission")
	}
	if (&Error{Code: CodeInternal}).Error() != "fsjail: INTERNAL" {
		t.Error("minimal Error()", (&Error{Code: CodeInternal}).Error())
	}
}

func TestModeFromUnix(t *testing.T) {
	cases := map[uint32]fs.FileMode{
		unix.S_IFREG | 0o640:                 0o640,
		unix.S_IFDIR | 0o750:                 fs.ModeDir | 0o750,
		unix.S_IFLNK | 0o777:                 fs.ModeSymlink | 0o777,
		unix.S_IFCHR | 0o600:                 fs.ModeDevice | fs.ModeCharDevice | 0o600,
		unix.S_IFBLK | 0o600:                 fs.ModeDevice | 0o600,
		unix.S_IFIFO | 0o600:                 fs.ModeNamedPipe | 0o600,
		unix.S_IFSOCK | 0o600:                fs.ModeSocket | 0o600,
		unix.S_IFREG | unix.S_ISUID | 0o755:  fs.ModeSetuid | 0o755,
		unix.S_IFREG | unix.S_ISGID | 0o755:  fs.ModeSetgid | 0o755,
		unix.S_IFDIR | unix.S_ISVTX | 0o1777: fs.ModeDir | fs.ModeSticky | 0o777,
	}
	for in, want := range cases {
		if got := modeFromUnix(in); got != want {
			t.Errorf("modeFromUnix(%o) = %v want %v", in, got, want)
		}
	}
}

func TestFromStatAndSameDevice(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "f")
	if err := os.WriteFile(f, []byte("abc"), 0o640); err != nil {
		t.Fatal(err)
	}
	var st unix.Stat_t
	if err := unix.Lstat(f, &st); err != nil {
		t.Fatal(err)
	}
	info := fromStat(&st)
	if info.Size != 3 || info.IsDir || info.IsSymlink || info.Mode.Perm() != 0o640 || info.Nlink != 1 || info.ModTime.IsZero() || info.BirthTime != nil {
		t.Fatalf("fromStat: %+v", info)
	}
	var dst unix.Stat_t
	if err := unix.Lstat(tmp, &dst); err != nil {
		t.Fatal(err)
	}
	if !SameDevice(info, fromStat(&dst)) {
		t.Fatal("same device")
	}
	a, _ := os.Lstat(f)
	b, _ := os.Lstat(tmp)
	if !sameDeviceFileInfo(a, b) {
		t.Fatal("sameDeviceFileInfo")
	}
	if sameDeviceFileInfo(fakeInfo{}, b) {
		t.Fatal("non-unix Sys must be false")
	}
	// statAt via a real directory handle, following and not following.
	d, err := os.Open(tmp)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := os.Symlink("f", filepath.Join(tmp, "l")); err != nil {
		t.Fatal(err)
	}
	li, err := statAt(int(d.Fd()), "l", false)
	if err != nil || !li.IsSymlink {
		t.Fatalf("nofollow: %+v %v", li, err)
	}
	fi, err := statAt(int(d.Fd()), "l", true)
	if err != nil || fi.IsSymlink || fi.Size != 3 {
		t.Fatalf("follow: %+v %v", fi, err)
	}
	if _, err := statAt(int(d.Fd()), "missing", false); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing: %v", err)
	}
}

type fakeInfo struct{ os.FileInfo }

func (fakeInfo) Sys() any { return nil }

func TestCopyContentFallback(t *testing.T) {
	e := newEnv(t)
	e.write(t, "/src", "fallback-data")
	in, err := e.j.Open("/src")
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan []byte, 1)
	go func() {
		b, _ := io.ReadAll(pr)
		got <- b
	}()
	// copy_file_range refuses pipes (EINVAL) → io.Copy fallback.
	if err := copyContent(context.Background(), in, pw, 13, nil); err != nil {
		t.Fatal(err)
	}
	pw.Close()
	if string(<-got) != "fallback-data" {
		t.Fatal("fallback copy content")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	in2, _ := e.j.Open("/src")
	defer in2.Close()
	out, _ := e.j.Create("/dst", true)
	defer out.Close()
	if err := copyContent(ctx, in2, out, 13, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled before start: %v", err)
	}
}

func TestMiscBranches(t *testing.T) {
	e := newEnv(t)
	j := e.j
	if err := j.Mkdir("/d"); err != nil {
		t.Fatal(err)
	}
	sub, err := j.Sub("/d")
	if err != nil || sub.Base() != "/d" {
		t.Fatal(err)
	}
	sub.Close()
	if _, err := j.Readlink("/missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("readlink missing: %v", err)
	}
	if _, err := j.Readlink("/d"); err == nil {
		t.Fatal("readlink on dir must fail")
	}
	if relToVPath("") != Root || relToVPath(".") != Root || relToVPath("a/b") != "/a/b" || childPath(Root, "x") != "/x" || childPath("/a", "x") != "/a/x" {
		t.Fatal("path helpers")
	}
	if err := j.Symlink("x", Root); !errors.Is(err, ErrInvalidPath) {
		t.Fatal("symlink at root")
	}
	if err := j.Mkdir(Root); !errors.Is(err, ErrExists) {
		t.Fatal("mkdir root")
	}
	if err := j.MkdirAll(Root); err != nil {
		t.Fatal("mkdirall root is a no-op")
	}
	if err := j.Chtimes("/missing", time0(), time0()); !errors.Is(err, ErrNotFound) {
		t.Fatal("chtimes missing")
	}
	if _, err := j.Lstat("/d/.."); err == nil {
		t.Fatal("unclean VPath must not resolve")
	}
	// DirSize progress callback and a vanished entry.
	for i := 0; i < 520; i++ {
		e.write(t, VPath(fmt.Sprintf("/d/f%03d", i)), "x")
	}
	calls := 0
	st, err := j.DirSize(context.Background(), "/d", func(files, dirs, bytes int64) { calls++ })
	if err != nil || st.Files != 520 || st.Bytes != 520 || calls == 0 {
		t.Fatalf("dirsize: %+v %d %v", st, calls, err)
	}
	if _, err := j.DirSize(context.Background(), "/missing", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("dirsize missing: %v", err)
	}
	walkErr := j.WalkDir(context.Background(), "/d", func(vp string, d fs.DirEntry, err error) error {
		if vp == "/d" {
			return fs.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	// SysDir invalid names on every primitive.
	sys, err := OpenSys(t.TempDir(), "tmp")
	if err != nil {
		t.Fatal(err)
	}
	defer sys.Close()
	bad := "../x"
	if _, err := sys.Open(bad); !errors.Is(err, ErrInvalidPath) {
		t.Error("sys open")
	}
	if _, err := sys.Lstat(bad); !errors.Is(err, ErrInvalidPath) {
		t.Error("sys lstat")
	}
	if err := sys.MkdirAll(bad); !errors.Is(err, ErrInvalidPath) {
		t.Error("sys mkdirall")
	}
	if err := sys.Remove(bad); !errors.Is(err, ErrInvalidPath) {
		t.Error("sys remove")
	}
	if err := sys.RemoveAll(bad); !errors.Is(err, ErrInvalidPath) {
		t.Error("sys removeall")
	}
	if err := sys.Rename(bad, "y"); !errors.Is(err, ErrInvalidPath) {
		t.Error("sys rename src")
	}
	if err := sys.Rename("y", bad); !errors.Is(err, ErrInvalidPath) {
		t.Error("sys rename dst")
	}
	if _, err := sys.ReadDirNames(bad); !errors.Is(err, ErrInvalidPath) {
		t.Error("sys readdir")
	}
	if _, err := sys.ReadDirNames("missing"); !errors.Is(err, ErrNotFound) {
		t.Error("sys readdir missing")
	}
	if _, err := sys.Create("."); !errors.Is(err, ErrInvalidPath) {
		t.Error("sys create root")
	}
	if _, err := sys.Open("missing"); !errors.Is(err, ErrNotFound) {
		t.Error("sys open missing")
	}
	if err := sys.Rename("missing", "y"); !errors.Is(err, ErrNotFound) {
		t.Error("sys rename missing")
	}
	if _, err := sys.HostPath("missing"); !errors.Is(err, ErrNotFound) {
		t.Error("sys hostpath missing")
	}
	if err := j.MoveOut(context.Background(), "/d", sys, "."); !errors.Is(err, ErrInvalidPath) {
		t.Error("moveout root name")
	}
	if err := j.MoveIn(context.Background(), sys, "..", "/z"); !errors.Is(err, ErrInvalidPath) {
		t.Error("movein bad name")
	}
	// moveAcross: copy failure after EXDEV leaves the source intact.
	orig := renameAcrossFn
	renameAcrossFn = func(*os.Root, string, *os.Root, string) error { return syscall.EXDEV }
	defer func() { renameAcrossFn = orig }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := j.MoveOut(ctx, "/d", sys, "t"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled move out: %v", err)
	}
	if _, err := j.Lstat("/d/f000"); err != nil {
		t.Fatal("source must survive a failed cross-device move")
	}
	if names, _ := sys.ReadDirNames("."); len(names) != 0 {
		t.Fatalf("partial left in sys dir: %v", names)
	}
	renameAcrossFn = orig
	if err := renameAcross(j.root, ".", sys.root, "x"); err == nil {
		t.Fatal("renameAcross must refuse the root itself")
	}
}

func time0() time.Time { return time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC) }

func TestUncleanVPathRejectedEverywhere(t *testing.T) {
	e := newEnv(t)
	j := e.j
	ctx := context.Background()
	bad := VPath("/a/..")
	sys, _ := OpenSys(t.TempDir(), "tmp")
	defer sys.Close()
	checks := []error{
		func() error { _, err := j.Open(bad); return err }(),
		func() error { _, err := j.OpenFile(bad, os.O_RDONLY, 0); return err }(),
		func() error { _, err := j.Create(bad, true); return err }(),
		j.Mkdir(bad), j.MkdirAll(bad), j.Rename(bad, "/x"), j.Rename("/x", bad), j.Remove(bad), j.RemoveAll(bad),
		j.Symlink("x", bad),
		func() error { _, err := j.Readlink(bad); return err }(),
		j.Chmod(bad, 0o640), j.ChmodRecursive(ctx, bad, 0o640, 0o750, nil), j.Chtimes(bad, time0(), time0()),
		j.WalkDir(ctx, bad, func(string, fs.DirEntry, error) error { return nil }),
		func() error { _, err := j.DirSize(ctx, bad, nil); return err }(),
		func() error { _, err := j.AbsForCLI(bad); return err }(),
		func() error { _, err := j.Lstat(bad); return err }(),
		func() error { _, err := j.Stat(bad); return err }(),
		func() error { _, err := j.ReadDir(bad); return err }(),
		j.CopyFile(ctx, bad, "/x", nil), j.CopyFile(ctx, "/x", bad, nil),
		j.CopyTree(ctx, bad, "/x", nil), j.CopyTree(ctx, "/x", bad, nil),
		j.MoveTree(ctx, bad, "/x", nil), j.MoveTree(ctx, "/x", bad, nil),
		j.MoveOut(ctx, bad, sys, "n"), j.MoveIn(ctx, sys, "n", bad),
		func() error { _, err := j.SubRoot(bad, false); return err }(),
	}
	for i, err := range checks {
		var fe *Error
		if !errors.As(err, &fe) || fe.Code != CodePathInvalid {
			t.Errorf("check %d: unclean VPath must be PATH_INVALID, got %v", i, err)
		}
	}
	if err := checkVPath(""); err != nil {
		t.Fatal("empty is root")
	}
	if err := checkVPath("relative"); err == nil {
		t.Fatal("missing leading slash")
	}
	if err := checkVPath("/a/b/"); err == nil {
		t.Fatal("trailing slash is unclean")
	}
}

func TestProvisionHome(t *testing.T) {
	storage := t.TempDir()
	res, err := ProvisionHome(storage, "budi", false)
	if err != nil || !res.Created || res.Reused || res.Recreated {
		t.Fatalf("first: %+v %v", res, err)
	}
	info, _ := os.Stat(filepath.Join(storage, "budi"))
	if info.Mode().Perm() != DirMode {
		t.Fatalf("mode %v", info.Mode())
	}
	if res, err := ProvisionHome(storage, "budi", true); err != nil || res.Created || res.Reused {
		t.Fatalf("steady: %+v %v", res, err)
	}
	if res, err := ProvisionHome(storage, "budi", false); err != nil || !res.Reused {
		t.Fatalf("reuse: %+v %v", res, err)
	}
	os.RemoveAll(filepath.Join(storage, "budi"))
	if res, err := ProvisionHome(storage, "budi", true); err != nil || !res.Created || !res.Recreated {
		t.Fatalf("recreate: %+v %v", res, err)
	}
	os.WriteFile(filepath.Join(storage, "filey"), []byte("x"), 0o640)
	if _, err := ProvisionHome(storage, "filey", false); !errors.Is(err, ErrHomeNotDir) {
		t.Fatalf("file: %v", err)
	}
	os.Symlink("budi", filepath.Join(storage, "linky"))
	if _, err := ProvisionHome(storage, "linky", false); !errors.Is(err, ErrHomeNotDir) {
		t.Fatalf("symlink: %v", err)
	}
	if _, err := ProvisionHome(storage, "../escape", false); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("username: %v", err)
	}
	if _, err := ProvisionHome(filepath.Join(storage, "nope"), "budi", false); err == nil {
		t.Fatal("missing storage root")
	}
}
