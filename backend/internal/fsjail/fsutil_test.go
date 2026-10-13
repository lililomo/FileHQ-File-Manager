package fsjail

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestCopyFile(t *testing.T) {
	e := newEnv(t)
	j := e.j
	content := strings.Repeat("0123456789", 100_000) // 1 MB
	e.write(t, "/src.bin", content)
	if err := j.Chmod("/src.bin", 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Date(2021, 5, 6, 7, 8, 9, 0, time.UTC)
	if err := j.Chtimes("/src.bin", when, when); err != nil {
		t.Fatal(err)
	}
	var last int64
	if err := j.CopyFile(context.Background(), "/src.bin", "/dst.bin", func(b int64) { last = b }); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.read(t, "/dst.bin"); got != content {
		t.Fatal("content differs")
	}
	info, _ := j.Lstat("/dst.bin")
	if info.Mode.Perm() != 0o600 || !info.ModTime.Equal(when) || last != int64(len(content)) {
		t.Fatalf("mode/mtime/progress: %v %v %d", info.Mode, info.ModTime, last)
	}
	if err := j.CopyFile(context.Background(), "/src.bin", "/dst.bin", nil); !errors.Is(err, ErrExists) {
		t.Fatalf("existing dst: %v", err)
	}
	if err := j.Mkdir("/d"); err != nil {
		t.Fatal(err)
	}
	if err := j.CopyFile(context.Background(), "/d", "/d2", nil); !errors.Is(err, ErrIsDir) {
		t.Fatalf("dir src: %v", err)
	}
	if err := j.CopyFile(context.Background(), Root, "/x", nil); !errors.Is(err, ErrIsDir) {
		t.Fatalf("root src: %v", err)
	}
	if err := j.CopyFile(context.Background(), "/missing", "/x", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing src: %v", err)
	}
	if err := j.Symlink("src.bin", "/lsrc"); err != nil {
		t.Fatal(err)
	}
	if err := j.CopyFile(context.Background(), "/src.bin", "/lsrc", nil); !errors.Is(err, ErrExists) && !errors.Is(err, ErrEscape) {
		t.Fatalf("dst symlink must not be followed: %v", err)
	}
	// Cancellation mid-copy removes the partial destination.
	ctx, cancel := context.WithCancel(context.Background())
	err := j.CopyFile(ctx, "/src.bin", "/partial.bin", func(int64) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := j.Lstat("/partial.bin"); !errors.Is(err, ErrNotFound) {
		t.Fatal("partial destination must be removed")
	}
	// fallback path (io.Copy) when copy_file_range is unavailable
	e.write(t, "/small", "abc")
	in, _ := j.Open("/small")
	out, _ := j.Create("/small2", true)
	if err := copyContent(context.Background(), in, out, 3, nil); err != nil {
		t.Fatal(err)
	}
	in.Close()
	out.Close()
	if got, _ := e.read(t, "/small2"); got != "abc" {
		t.Fatal("copyContent")
	}
}

func TestCopyTreeAndMoveTree(t *testing.T) {
	e := newEnv(t)
	j := e.j
	if err := j.MkdirAll("/tree/sub/deep"); err != nil {
		t.Fatal(err)
	}
	e.write(t, "/tree/a.txt", "A")
	e.write(t, "/tree/sub/b.txt", "BB")
	e.write(t, "/tree/sub/deep/c.txt", "CCC")
	if err := j.Symlink("../a.txt", "/tree/sub/la"); err != nil {
		t.Fatal(err)
	}
	if err := j.Chmod("/tree/sub", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(filepath.Join(e.home, "tree", "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	var total int64
	if err := j.CopyTree(context.Background(), "/tree", "/copy", func(b int64) { total = b }); err != nil {
		t.Fatal(err)
	}
	if total != 6 {
		t.Fatalf("progress total %d", total)
	}
	for p, want := range map[VPath]string{"/copy/a.txt": "A", "/copy/sub/b.txt": "BB", "/copy/sub/deep/c.txt": "CCC", "/copy/sub/la": "A"} {
		if got, err := e.read(t, p); err != nil || got != want {
			t.Fatalf("%s: %q %v", p, got, err)
		}
	}
	if info, _ := j.Lstat("/copy/sub/la"); !info.IsSymlink || info.SymlinkTarget != "../a.txt" {
		t.Fatalf("symlink copied as link: %+v", info)
	}
	if info, _ := j.Lstat("/copy/sub"); info.Mode.Perm() != 0o700 {
		t.Fatalf("dir mode: %v", info.Mode)
	}
	if _, err := j.Lstat("/copy/fifo"); !errors.Is(err, ErrNotFound) {
		t.Fatal("fifo must be skipped")
	}
	if err := j.CopyTree(context.Background(), "/tree", "/tree/sub/inside", nil); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("into itself: %v", err)
	}
	if err := j.CopyTree(context.Background(), "/tree", "/copy", nil); !errors.Is(err, ErrExists) {
		t.Fatalf("existing dst: %v", err)
	}
	if err := j.CopyTree(context.Background(), "/tree", Root, nil); !errors.Is(err, ErrExists) {
		t.Fatalf("dst root: %v", err)
	}
	if err := j.CopyTree(context.Background(), "/missing", "/x", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing src: %v", err)
	}
	if err := j.CopyTree(context.Background(), "/tree/a.txt", "/single", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.read(t, "/single"); got != "A" {
		t.Fatal("single file tree copy")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := j.CopyTree(ctx, "/tree", "/cancelled", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}

	// MoveTree: plain rename.
	if err := j.MoveTree(context.Background(), "/copy", "/moved", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Lstat("/copy"); !errors.Is(err, ErrNotFound) {
		t.Fatal("src must be gone")
	}
	if got, _ := e.read(t, "/moved/sub/deep/c.txt"); got != "CCC" {
		t.Fatal("moved content")
	}
	for _, c := range []struct{ src, dst VPath }{{"/moved", "/moved/sub/x"}, {Root, "/x"}, {"/moved", Root}} {
		if err := j.MoveTree(context.Background(), c.src, c.dst, nil); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("MoveTree(%s,%s): %v", c.src, c.dst, err)
		}
	}
	if err := j.MoveTree(context.Background(), "/missing", "/x", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("move missing: %v", err)
	}

	// MoveTree across filesystems: simulate EXDEV on the first rename.
	calls := 0
	real := j.renameFn
	j.renameFn = func(o, n string) error {
		calls++
		if calls == 1 {
			return syscall.EXDEV
		}
		return real(o, n)
	}
	if err := j.MoveTree(context.Background(), "/moved", "/xdev", nil); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("expected copy + rename-into-place, calls=%d", calls)
	}
	if _, err := j.Lstat("/moved"); !errors.Is(err, ErrNotFound) {
		t.Fatal("src must be removed after cross-device move")
	}
	if got, _ := e.read(t, "/xdev/sub/b.txt"); got != "BB" {
		t.Fatal("cross-device content")
	}
	entries, _ := j.ReadDir(Root)
	for _, en := range entries {
		if strings.Contains(en.Name, ".fm-partial-") {
			t.Fatal("partial directory left behind")
		}
	}
	// Failure mid-copy: source intact, partial removed.
	calls = 0
	j.renameFn = func(o, n string) error { return syscall.EXDEV }
	ctx2, cancel2 := context.WithCancel(context.Background())
	err := j.MoveTree(ctx2, "/xdev", "/fail", func(int64) { cancel2() })
	cancel2()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if got, _ := e.read(t, "/xdev/sub/b.txt"); got != "BB" {
		t.Fatal("source must be intact after failed move")
	}
	entries, _ = j.ReadDir(Root)
	for _, en := range entries {
		if strings.Contains(en.Name, ".fm-partial-") || en.Name == "fail" {
			t.Fatalf("leftover %s", en.Name)
		}
	}
	j.renameFn = real
	if _, err := os.Stat(filepath.Join(e.outside, "canary")); err != nil {
		t.Fatal(err)
	}
}

func TestSysDirAndMoveOutIn(t *testing.T) {
	e := newEnv(t)
	j := e.j
	data := t.TempDir()
	trash, err := OpenSys(data, "trash/u1")
	if err != nil {
		t.Fatal(err)
	}
	defer trash.Close()
	if _, err := os.Stat(filepath.Join(data, "trash", "u1")); err != nil {
		t.Fatal("OpenSys must create the subfolder")
	}
	for _, bad := range []string{"", ".", "..", "../x", "a/../b"} {
		if _, err := OpenSys(data, bad); err == nil {
			t.Errorf("OpenSys(%q) must fail", bad)
		}
	}
	if _, err := OpenSys(filepath.Join(data, "missing-root"), "x"); err == nil {
		t.Error("missing data dir must fail")
	}
	if err := j.MkdirAll("/docs/sub"); err != nil {
		t.Fatal(err)
	}
	e.write(t, "/docs/sub/f", "F")
	e.write(t, "/single", "S")
	if err := j.MoveOut(context.Background(), "/docs", trash, "t1"); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Lstat("/docs"); !errors.Is(err, ErrNotFound) {
		t.Fatal("moved out")
	}
	if b, err := os.ReadFile(filepath.Join(data, "trash", "u1", "t1", "sub", "f")); err != nil || string(b) != "F" {
		t.Fatalf("trash content: %q %v", b, err)
	}
	if err := j.MoveOut(context.Background(), "/single", trash, "t1"); !errors.Is(err, ErrExists) {
		t.Fatalf("name taken must not replace: %v", err)
	}
	if err := j.MoveOut(context.Background(), Root, trash, "r"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("move out root: %v", err)
	}
	if err := j.MoveOut(context.Background(), "/missing", trash, "m"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("move out missing: %v", err)
	}
	if err := j.MoveOut(context.Background(), "/single", trash, "../escape"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("sys name validation: %v", err)
	}
	if err := j.MoveIn(context.Background(), trash, "t1", "/restored"); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.read(t, "/restored/sub/f"); got != "F" {
		t.Fatal("restored content")
	}
	if names, _ := trash.ReadDirNames("."); len(names) != 0 {
		t.Fatalf("trash should be empty: %v", names)
	}
	if err := j.MoveIn(context.Background(), trash, "nope", "/x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("move in missing: %v", err)
	}
	if err := j.MoveIn(context.Background(), trash, "x", Root); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("move in to root: %v", err)
	}
	// Cross-device simulation for MoveOut/MoveIn.
	orig := renameAcrossFn
	renameAcrossFn = func(*os.Root, string, *os.Root, string) error { return syscall.EXDEV }
	defer func() { renameAcrossFn = orig }()
	if err := j.MoveOut(context.Background(), "/restored", trash, "t2"); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Lstat("/restored"); !errors.Is(err, ErrNotFound) {
		t.Fatal("source removed after copy")
	}
	if err := j.MoveIn(context.Background(), trash, "t2", "/back"); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.read(t, "/back/sub/f"); got != "F" {
		t.Fatal("cross-device restore")
	}
	names, _ := trash.ReadDirNames(".")
	for _, n := range names {
		if strings.Contains(n, ".fm-partial-") || n == "t2" {
			t.Fatalf("leftover in trash: %v", names)
		}
	}
	renameAcrossFn = orig

	// SysDir primitives.
	_, err = trash.Create("meta/info.json")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("create without parent: %v", err)
	}
	if err := trash.MkdirAll("meta"); err != nil {
		t.Fatal(err)
	}
	f, err := trash.Create("meta/info.json")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("{}")
	_ = f.Close()
	if _, err := trash.Create("meta/info.json"); !errors.Is(err, ErrExists) {
		t.Fatal("excl")
	}
	rf, err := trash.Open("meta/info.json")
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 2)
	rf.Read(b)
	rf.Close()
	if !bytes.Equal(b, []byte("{}")) {
		t.Fatal("read back")
	}
	if info, err := trash.Lstat("meta/info.json"); err != nil || info.Size() != 2 {
		t.Fatal(err)
	}
	if err := trash.Rename("meta/info.json", "meta/info2.json"); err != nil {
		t.Fatal(err)
	}
	names, _ = trash.ReadDirNames("meta")
	if len(names) != 1 || names[0] != "info2.json" {
		t.Fatalf("names: %v", names)
	}
	hp, err := trash.HostPath("meta/info2.json")
	if err != nil || hp != filepath.Join(data, "trash", "u1", "meta", "info2.json") {
		t.Fatalf("host path: %q %v", hp, err)
	}
	if hp, _ := trash.HostPath("."); hp != filepath.Join(data, "trash", "u1") {
		t.Fatal("host path root")
	}
	if err := os.Symlink("/etc", filepath.Join(data, "trash", "u1", "lnk")); err != nil {
		t.Fatal(err)
	}
	if _, err := trash.HostPath("lnk/passwd"); !errors.Is(err, ErrEscape) {
		t.Fatalf("host path through symlink: %v", err)
	}
	if _, err := trash.HostPath("../x"); !errors.Is(err, ErrInvalidPath) {
		t.Fatal("host path validation")
	}
	if _, err := fs.Stat(trash.FS(), "meta/info2.json"); err != nil {
		t.Fatal(err)
	}
	if err := trash.Remove("meta/info2.json"); err != nil {
		t.Fatal(err)
	}
	if err := trash.RemoveAll("meta"); err != nil {
		t.Fatal(err)
	}
	if err := trash.Remove("lnk"); err != nil {
		t.Fatal(err)
	}
	if _, err := trash.Lstat("meta"); !errors.Is(err, ErrNotFound) {
		t.Fatal("removed")
	}
	if err := trash.Close(); err != nil {
		t.Fatal(err)
	}
	if err := trash.Close(); err != nil {
		t.Fatal("double close")
	}
}
