package fsjail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// testEnv holds a jail on tmp/home and an outside directory with a canary.
type testEnv struct {
	j       *Jail
	home    string
	outside string
	canary  []byte
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	tmp := t.TempDir()
	home := filepath.Join(tmp, "home")
	outside := filepath.Join(tmp, "outside")
	if err := os.MkdirAll(home, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o750); err != nil {
		t.Fatal(err)
	}
	canary := []byte("CANARY-OUTSIDE-ROOT-" + t.Name())
	if err := os.WriteFile(filepath.Join(outside, "canary"), canary, 0o640); err != nil {
		t.Fatal(err)
	}
	j, err := Open(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	return &testEnv{j: j, home: home, outside: outside, canary: canary}
}

func (e *testEnv) write(t *testing.T, p VPath, content string) {
	t.Helper()
	f, err := e.j.Create(p, true)
	if err != nil {
		t.Fatalf("create %s: %v", p, err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func (e *testEnv) read(t *testing.T, p VPath) (string, error) {
	t.Helper()
	f, err := e.j.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	return string(b), err
}

// hostSymlink creates a symlink directly on the host (what an attacker with
// shell access, or an old link, would leave behind).
func (e *testEnv) hostSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, filepath.Join(e.home, link)); err != nil {
		t.Fatal(err)
	}
}

var goVersionRe = regexp.MustCompile(`^go(\d+)\.(\d+)(?:\.(\d+))?`)

// TestRuntimeVersion fails hard on toolchains with known os.Root escapes
// (CVE-2026-32282 fixed in 1.25.9, CVE-2026-39822 fixed in 1.25.12).
func TestRuntimeVersion(t *testing.T) {
	v := runtime.Version()
	m := goVersionRe.FindStringSubmatch(v)
	if m == nil {
		t.Skipf("non-release toolchain %q; cannot verify patch level", v)
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	patch := 0
	if m[3] != "" {
		patch, _ = strconv.Atoi(m[3])
	}
	ok := major > 1 || minor > 25 || (minor == 25 && patch >= 12)
	if !ok {
		t.Fatalf("Go %s is older than 1.25.12: os.Root symlink escapes (CVE-2026-32282, CVE-2026-39822) are unfixed", v)
	}
}

func TestOpenUser(t *testing.T) {
	storage := t.TempDir()
	if err := os.Mkdir(filepath.Join(storage, "budi"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("budi", filepath.Join(storage, "sari")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(storage, "file1"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	j, err := OpenUser(storage, "budi", "u1")
	if err != nil || j.UserID() != "u1" || j.ReadOnly() || !j.Base().IsRoot() {
		t.Fatalf("OpenUser: %v", err)
	}
	defer j.Close()
	if _, err := OpenUser(storage, "nobody-yet", "u2"); !errors.Is(err, ErrHomeMissing) {
		t.Errorf("missing home: %v", err)
	}
	if _, err := OpenUser(storage, "sari", "u3"); !errors.Is(err, ErrHomeNotDir) {
		t.Errorf("symlinked home must be rejected: %v", err)
	}
	if _, err := OpenUser(storage, "file1", "u4"); !errors.Is(err, ErrHomeNotDir) {
		t.Errorf("file home: %v", err)
	}
	for _, bad := range []string{"../etc", "Budi", "a", "with/slash", ".system", ""} {
		if _, err := OpenUser(storage, bad, "u5"); !errors.Is(err, ErrInvalidPath) {
			t.Errorf("username %q: %v", bad, err)
		}
	}
	if _, err := OpenUser(filepath.Join(storage, "nope"), "budi", "u6"); err == nil {
		t.Error("missing storage root must fail")
	}
}

func TestCreateStatReadDir(t *testing.T) {
	e := newEnv(t)
	j := e.j
	if err := j.Mkdir("/docs"); err != nil {
		t.Fatal(err)
	}
	if err := j.Mkdir("/docs"); !errors.Is(err, ErrExists) {
		t.Fatalf("mkdir existing: %v", err)
	}
	if err := j.Mkdir("/a/b/c"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("mkdir without parent: %v", err)
	}
	if err := j.MkdirAll("/a/b/c"); err != nil {
		t.Fatal(err)
	}
	if err := j.MkdirAll("/a/b/c"); err != nil {
		t.Fatal("MkdirAll must be idempotent", err)
	}
	e.write(t, "/docs/réport.txt", "hello")
	if _, err := j.Create("/docs/réport.txt", true); !errors.Is(err, ErrExists) {
		t.Fatalf("excl create: %v", err)
	}
	// NFD spelling refers to the same file after normalization.
	nfd, _ := Clean("/docs/réport.txt")
	if got, err := e.read(t, nfd); err != nil || got != "hello" {
		t.Fatalf("NFD path: %q %v", got, err)
	}
	info, err := j.Lstat("/docs/réport.txt")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "réport.txt" || info.Path != "/docs/réport.txt" || info.IsDir || info.Size != 5 || info.Mode.Perm() != FileMode || !info.IsRegular() || info.Nlink != 1 {
		t.Fatalf("info: %+v", info)
	}
	if time.Since(info.ModTime) > time.Minute || info.ChangeTime.IsZero() || info.AccessTime.IsZero() {
		t.Fatalf("times: %+v", info)
	}
	root, err := j.Lstat(Root)
	if err != nil || !root.IsDir || root.Path != Root || root.Name != "" {
		t.Fatalf("root lstat: %+v %v", root, err)
	}
	entries, err := j.ReadDir(Root)
	if err != nil || len(entries) != 2 {
		t.Fatalf("readdir root: %d %v", len(entries), err)
	}
	for _, en := range entries {
		if !en.IsDir || !strings.HasPrefix(string(en.Path), "/") || en.Path.Depth() != 1 {
			t.Fatalf("entry: %+v", en)
		}
	}
	docs, _ := j.ReadDir("/docs")
	if len(docs) != 1 || docs[0].Path != "/docs/réport.txt" || docs[0].Size != 5 {
		t.Fatalf("readdir docs: %+v", docs)
	}
	if _, err := j.ReadDir("/docs/réport.txt"); !errors.Is(err, ErrNotDir) {
		t.Fatalf("readdir on file: %v", err)
	}
	if _, err := j.ReadDir("/missing"); !errors.Is(err, ErrNotFound) || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("readdir missing: %v", err)
	}
	if _, err := j.Lstat("/missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("lstat missing: %v", err)
	}
	if _, err := j.Lstat("/docs/réport.txt/x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("lstat through file: %v", err)
	}
	when := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := j.Chtimes("/docs/réport.txt", when, when); err != nil {
		t.Fatal(err)
	}
	if info, _ := j.Lstat("/docs/réport.txt"); !info.ModTime.Equal(when) {
		t.Fatalf("chtimes: %v", info.ModTime)
	}
	if _, err := j.Create(Root, false); !errors.Is(err, ErrIsDir) {
		t.Fatalf("create root: %v", err)
	}
	if _, err := j.OpenFile(Root, os.O_RDONLY, 0); !errors.Is(err, ErrIsDir) {
		t.Fatalf("openfile root: %v", err)
	}
	f, err := j.OpenFile("/docs/new.bin", os.O_WRONLY|os.O_CREATE, 0o4777)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if info, _ := j.Lstat("/docs/new.bin"); info.Mode&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0 {
		t.Fatalf("OpenFile must strip setuid: %v", info.Mode)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal("double close", err)
	}
}

func TestSymlinkPolicy(t *testing.T) {
	e := newEnv(t)
	j := e.j
	e.write(t, "/secret.txt", "s")
	if err := j.MkdirAll("/sub/deep"); err != nil {
		t.Fatal(err)
	}
	// User-created links: relative and inside only.
	for _, c := range []struct{ target, link string }{
		{"../../etc/passwd", "/esc1"}, {"/etc/passwd", "/esc2"}, {"../..", "/sub/esc3"}, {"..", "/esc4"},
		{"", "/esc5"}, {"a\x00b", "/esc6"},
	} {
		err := j.Symlink(c.target, VPath(c.link))
		if !errors.Is(err, ErrEscape) && !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Symlink(%q): %v", c.target, err)
		}
	}
	if err := j.Symlink("../secret.txt", "/sub/ok"); err != nil {
		t.Fatal(err)
	}
	if got, err := e.read(t, "/sub/ok"); err != nil || got != "s" {
		t.Fatalf("follow in-root link: %q %v", got, err)
	}
	if err := j.Symlink("nothere", "/dangling"); err != nil {
		t.Fatal(err)
	}
	if info, _ := j.Lstat("/dangling"); !info.IsSymlink || !info.SymlinkBroken || info.SymlinkReason != "missing" || info.SymlinkTarget != "nothere" {
		t.Fatalf("dangling: %+v", info)
	}
	if target, err := j.Readlink("/sub/ok"); err != nil || target != "../secret.txt" {
		t.Fatal(target, err)
	}
	// Pre-existing escaping links (absolute and relative).
	e.hostSymlink(t, filepath.Join(e.outside, "canary"), "abs-escape")
	e.hostSymlink(t, "../../outside/canary", "sub/rel-escape")
	e.hostSymlink(t, "../outside", "dir-escape")
	for _, p := range []VPath{"/abs-escape", "/sub/rel-escape", "/dir-escape"} {
		info, err := j.Lstat(p)
		if err != nil || !info.IsSymlink || !info.SymlinkBroken || info.SymlinkReason != "escape" {
			t.Errorf("Lstat(%s): %+v %v", p, info, err)
		}
		if _, err := e.read(t, p); !errors.Is(err, ErrEscape) {
			t.Errorf("Open(%s) must be SYMLINK_ESCAPE, got %v", p, err)
		}
		if _, err := j.Stat(p); !errors.Is(err, ErrEscape) {
			t.Errorf("Stat(%s): %v", p, err)
		}
	}
	if _, err := e.read(t, "/dir-escape/canary"); !errors.Is(err, ErrEscape) {
		t.Errorf("open through escaping dir link: %v", err)
	}
	if _, err := j.ReadDir("/dir-escape"); !errors.Is(err, ErrEscape) {
		t.Errorf("readdir through escaping dir link: %v", err)
	}
	entries, _ := j.ReadDir("/sub")
	seen := map[string]Info{}
	for _, en := range entries {
		seen[en.Name] = en
	}
	if !seen["rel-escape"].SymlinkBroken || seen["rel-escape"].SymlinkReason != "escape" || seen["ok"].SymlinkBroken {
		t.Fatalf("readdir classification: %+v", seen)
	}
	// Chains and loops.
	if err := j.Symlink("secret.txt", "/chain1"); err != nil {
		t.Fatal(err)
	}
	if err := j.Symlink("chain1", "/chain2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.read(t, "/chain2"); got != "s" {
		t.Fatal("chain")
	}
	if st, err := j.Stat("/chain2"); err != nil || st.IsSymlink || st.Size != 1 {
		t.Fatalf("Stat follows: %+v %v", st, err)
	}
	if err := j.Symlink("loopb", "/loopa"); err != nil {
		t.Fatal(err)
	}
	if err := j.Symlink("loopa", "/loopb"); err != nil {
		t.Fatal(err)
	}
	if info, _ := j.Lstat("/loopa"); !info.SymlinkBroken || info.SymlinkReason != "loop" {
		t.Fatalf("loop: %+v", info)
	}
	if _, err := e.read(t, "/loopa"); err == nil {
		t.Fatal("loop open must fail")
	}
	// Hardlinks are allowed.
	if err := os.Link(filepath.Join(e.home, "secret.txt"), filepath.Join(e.home, "hard")); err != nil {
		t.Fatal(err)
	}
	if info, _ := j.Lstat("/hard"); info.Nlink != 2 || info.IsSymlink {
		t.Fatalf("hardlink: %+v", info)
	}
	// Create never writes through a symlink (O_NOFOLLOW on the last hop).
	if _, err := j.Create("/chain1", false); !errors.Is(err, ErrEscape) {
		t.Fatalf("create through symlink: %v", err)
	}
	if got, _ := e.read(t, "/secret.txt"); got != "s" {
		t.Fatal("target must be untouched")
	}
	// Canary never touched.
	if b, _ := os.ReadFile(filepath.Join(e.outside, "canary")); !bytes.Equal(b, e.canary) {
		t.Fatal("canary modified")
	}
}

// TestTrailingSlashSymlink covers CVE-2026-39822: a final-component symlink
// with a trailing slash used to be followed out of the root.
func TestTrailingSlashSymlink(t *testing.T) {
	e := newEnv(t)
	e.hostSymlink(t, "../outside", "link")
	v, err := Clean("/link/")
	if err != nil || v != "/link" {
		t.Fatalf("Clean strips the trailing slash: %q %v", v, err)
	}
	if _, err := e.j.Open(v); !errors.Is(err, ErrEscape) {
		t.Fatalf("Open(link): %v", err)
	}
	if _, err := e.j.ReadDir(v); !errors.Is(err, ErrEscape) {
		t.Fatalf("ReadDir(link): %v", err)
	}
	// Toolchain canary: os.Root itself must refuse "link/".
	for _, raw := range []string{"link/", "link/.", "link/canary"} {
		f, err := e.j.root.Open(raw)
		if err == nil {
			f.Close()
			t.Fatalf("os.Root.Open(%q) followed a symlink out of the root: toolchain %s is vulnerable", raw, runtime.Version())
		}
	}
}

// TestChmodSymlinkNotFollowed covers the CVE-2026-32282 class: chmod on a
// link must never change the target.
func TestChmodSymlinkNotFollowed(t *testing.T) {
	e := newEnv(t)
	j := e.j
	e.write(t, "/f", "x")
	if err := j.Symlink("f", "/l"); err != nil {
		t.Fatal(err)
	}
	e.hostSymlink(t, "../outside/canary", "lout")
	before, _ := os.Lstat(filepath.Join(e.outside, "canary"))
	if err := j.Chmod("/l", 0o777); err != nil {
		t.Fatalf("chmod on link: %v", err)
	}
	if err := j.Chmod("/lout", 0o777); err != nil {
		t.Fatalf("chmod on escaping link: %v", err)
	}
	if info, _ := j.Lstat("/f"); info.Mode.Perm() != FileMode {
		t.Fatalf("target changed: %v", info.Mode)
	}
	after, _ := os.Lstat(filepath.Join(e.outside, "canary"))
	if before.Mode() != after.Mode() {
		t.Fatal("outside canary mode changed")
	}
	if err := j.Chmod("/f", 0o4755); err != nil {
		t.Fatal(err)
	}
	if info, _ := j.Lstat("/f"); info.Mode.Perm() != 0o755 || info.Mode&fs.ModeSetuid != 0 {
		t.Fatalf("setuid must be dropped: %v", info.Mode)
	}
	if err := j.Chmod(Root, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := j.Chmod("/missing", 0o640); !errors.Is(err, ErrNotFound) {
		t.Fatalf("chmod missing: %v", err)
	}
	if err := j.MkdirAll("/t/d"); err != nil {
		t.Fatal(err)
	}
	e.write(t, "/t/d/file", "1")
	if err := j.Symlink("../../f", "/t/d/link"); err != nil {
		t.Fatal(err)
	}
	n := 0
	if err := j.ChmodRecursive(context.Background(), "/t", 0o600, 0o700, func(c int) { n = c }); err != nil {
		t.Fatal(err)
	}
	if info, _ := j.Lstat("/t/d"); info.Mode.Perm() != 0o700 {
		t.Fatalf("dir mode: %v", info.Mode)
	}
	if info, _ := j.Lstat("/t/d/file"); info.Mode.Perm() != 0o600 {
		t.Fatalf("file mode: %v", info.Mode)
	}
	if info, _ := j.Lstat("/f"); info.Mode.Perm() != 0o755 {
		t.Fatal("recursive chmod followed a symlink")
	}
	_ = n
}

func TestRemoveRename(t *testing.T) {
	e := newEnv(t)
	j := e.j
	if err := j.MkdirAll("/d/inner"); err != nil {
		t.Fatal(err)
	}
	e.write(t, "/d/inner/f", "1")
	if err := j.Symlink("d", "/ld"); err != nil {
		t.Fatal(err)
	}
	e.hostSymlink(t, "../outside", "lout")
	if err := j.Remove("/d"); !errors.Is(err, ErrNotEmpty) {
		t.Fatalf("remove non-empty: %v", err)
	}
	if err := j.RemoveAll("/ld"); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Lstat("/ld"); !errors.Is(err, ErrNotFound) {
		t.Fatal("link must be gone")
	}
	if _, err := j.Lstat("/d/inner/f"); err != nil {
		t.Fatal("RemoveAll on a dir link must not enter the target")
	}
	if err := j.RemoveAll("/lout"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(e.outside, "canary")); err != nil {
		t.Fatal("RemoveAll on an escaping link must not touch outside")
	}
	for _, p := range []VPath{Root, ""} {
		if err := j.Remove(p); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("remove root: %v", err)
		}
		if err := j.RemoveAll(p); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("removeall root: %v", err)
		}
	}
	if err := j.Rename("/d", "/d/inner/x"); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("rename into itself: %v", err)
	}
	if err := j.Rename("/d", Root); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("rename to root: %v", err)
	}
	if err := j.Rename("/missing", "/x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("rename missing: %v", err)
	}
	if err := j.Rename("/d", "/e"); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.read(t, "/e/inner/f"); got != "1" {
		t.Fatal("renamed content")
	}
	if err := j.Remove("/e/inner/f"); err != nil {
		t.Fatal(err)
	}
	if err := j.RemoveAll("/e"); err != nil {
		t.Fatal(err)
	}
	if err := j.Remove("/e"); !errors.Is(err, ErrNotFound) {
		t.Fatal("removed")
	}
}

func TestReadOnlyAndSubRoot(t *testing.T) {
	e := newEnv(t)
	j := e.j
	if err := j.MkdirAll("/share/in"); err != nil {
		t.Fatal(err)
	}
	e.write(t, "/share/in/f", "shared")
	e.write(t, "/private", "mine")
	if err := j.Symlink("../private", "/share/leak"); err != nil {
		t.Fatal(err) // valid for the owner: stays inside the home
	}
	if got, _ := e.read(t, "/share/leak"); got != "mine" {
		t.Fatal("owner can follow")
	}
	ro, err := j.SubRoot("/share", true)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	if !ro.ReadOnly() || ro.Base() != "/share" || ro.UserID() != j.UserID() {
		t.Fatalf("sub-root fields: %v %v", ro.ReadOnly(), ro.Base())
	}
	if _, err := ro.Open("/leak"); !errors.Is(err, ErrEscape) {
		t.Fatalf("recipient must not follow a link out of the sub-root: %v", err)
	}
	entries, _ := ro.ReadDir(Root)
	for _, en := range entries {
		if en.Name == "leak" && (!en.SymlinkBroken || en.SymlinkReason != "escape") {
			t.Fatalf("leak must show as broken escape: %+v", en)
		}
	}
	f, err := ro.Open("/in/f")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err := ro.Create("/new", true); !errors.Is(err, ErrReadOnly) || !errors.Is(err, ErrForbidden) {
		t.Fatalf("read-only create: %v", err)
	}
	if _, err := ro.OpenFile("/in/f", os.O_WRONLY, 0); !errors.Is(err, ErrReadOnly) {
		t.Fatal("read-only openfile")
	}
	for _, err := range []error{ro.Mkdir("/x"), ro.MkdirAll("/x/y"), ro.Rename("/in", "/z"), ro.Remove("/in/f"), ro.RemoveAll("/in"),
		ro.Symlink("in", "/l"), ro.Chmod("/in/f", 0o600), ro.Chtimes("/in/f", time.Now(), time.Now()),
		ro.CopyFile(context.Background(), "/in/f", "/g", nil), ro.CopyTree(context.Background(), "/in", "/h", nil),
		ro.MoveTree(context.Background(), "/in", "/k", nil), ro.ChmodRecursive(context.Background(), "/in", 0o600, 0o700, nil)} {
		if !errors.Is(err, ErrReadOnly) {
			t.Fatalf("read-only jail must refuse writes: %v", err)
		}
	}
	rw, err := j.SubRoot("/share", false)
	if err != nil {
		t.Fatal(err)
	}
	defer rw.Close()
	if _, err := rw.Create("/new", true); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Lstat("/share/new"); err != nil {
		t.Fatal("sub-root write must land inside the share")
	}
	nested, err := rw.SubRoot("/in", true)
	if err != nil || nested.Base() != "/share/in" {
		t.Fatalf("nested base: %v %v", nested.Base(), err)
	}
	nested.Close()
	same, err := j.SubRoot(Root, true)
	if err != nil || !same.ReadOnly() {
		t.Fatal(err)
	}
	if _, err := j.SubRoot("/share/in/f", false); !errors.Is(err, ErrNotDir) {
		t.Fatalf("sub-root on file: %v", err)
	}
	if err := j.Symlink("share", "/lshare"); err != nil {
		t.Fatal(err)
	}
	if _, err := j.SubRoot("/lshare", false); !errors.Is(err, ErrEscape) {
		t.Fatalf("sub-root on symlink: %v", err)
	}
	if _, err := j.SubRoot("/missing", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("sub-root missing: %v", err)
	}
}

func TestAbsForCLI(t *testing.T) {
	e := newEnv(t)
	j := e.j
	if err := j.MkdirAll("/a/b"); err != nil {
		t.Fatal(err)
	}
	e.write(t, "/a/b/f", "x")
	got, err := j.AbsForCLI("/a/b/f")
	if err != nil || got != filepath.Join(e.home, "a", "b", "f") {
		t.Fatalf("AbsForCLI: %q %v", got, err)
	}
	if got, err := j.HostPath(Root); err != nil || got != e.home {
		t.Fatalf("root host path: %q %v", got, err)
	}
	if err := j.Symlink("b", "/a/lb"); err != nil {
		t.Fatal(err)
	}
	if _, err := j.AbsForCLI("/a/lb/f"); !errors.Is(err, ErrEscape) {
		t.Fatalf("symlink component: %v", err)
	}
	if _, err := j.AbsForCLI("/a/lb"); !errors.Is(err, ErrEscape) {
		t.Fatalf("symlink leaf: %v", err)
	}
	if _, err := j.AbsForCLI("/a/missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestWalkDirSizeStatfs(t *testing.T) {
	e := newEnv(t)
	j := e.j
	if err := j.MkdirAll("/big/sub"); err != nil {
		t.Fatal(err)
	}
	e.write(t, "/big/sub/f1", strings.Repeat("a", 100))
	e.write(t, "/big/f2", strings.Repeat("b", 50))
	if err := j.MkdirAll("/other"); err != nil {
		t.Fatal(err)
	}
	e.write(t, "/other/huge", strings.Repeat("c", 10000))
	if err := j.Symlink("../other", "/big/lother"); err != nil {
		t.Fatal(err)
	}
	if err := j.Symlink("f2", "/big/lf2"); err != nil {
		t.Fatal(err)
	}
	st, err := j.DirSize(context.Background(), "/big", nil)
	if err != nil {
		t.Fatal(err)
	}
	// 2 regular files + 2 links counted as links (2 and 6 bytes), 1 dir; target content never counted.
	if st.Dirs != 1 || st.Files != 4 || st.Bytes != int64(150+len("../other")+len("f2")) {
		t.Fatalf("dir size: %+v", st)
	}
	var walked []string
	err = j.WalkDir(context.Background(), "/big", func(vp string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		walked = append(walked, vp)
		return nil
	})
	if err != nil || len(walked) != 6 || walked[0] != "/big" {
		t.Fatalf("walk: %v %v", walked, err)
	}
	for _, w := range walked {
		if strings.HasPrefix(w, "/big/lother/") {
			t.Fatal("walk followed a directory symlink")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := j.WalkDir(ctx, "/big", func(string, fs.DirEntry, error) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if err := j.WalkDir(context.Background(), "/missing", func(vp string, d fs.DirEntry, err error) error { return err }); !errors.Is(err, ErrNotFound) {
		t.Fatalf("walk missing: %v", err)
	}
	skip := j.WalkDir(context.Background(), "/big", func(vp string, d fs.DirEntry, err error) error {
		if vp == "/big/sub" {
			return fs.SkipDir
		}
		return err
	})
	if skip != nil {
		t.Fatal(skip)
	}
	free, total, err := j.Statfs()
	if err != nil || free == 0 || total == 0 || free > total {
		t.Fatalf("statfs: %d %d %v", free, total, err)
	}
	if _, err := fs.Stat(j.FS(), "big/f2"); err != nil {
		t.Fatal(err)
	}
}

// TestTOCTOUOpen swaps a directory for an escaping symlink while another
// goroutine keeps opening a file inside it. Reads must always be either the
// in-root content or SYMLINK_ESCAPE, never the outside canary.
func TestTOCTOUOpen(t *testing.T) {
	e := newEnv(t)
	j := e.j
	if err := j.Mkdir("/dir"); err != nil {
		t.Fatal(err)
	}
	e.write(t, "/dir/canary", "inside")
	dir := filepath.Join(e.home, "dir")
	aside := filepath.Join(e.home, "dir.aside")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				// leave the real directory in place
				_ = os.Remove(dir)
				_ = os.Rename(aside, dir)
				return
			default:
			}
			_ = os.Rename(dir, aside)
			_ = os.Symlink("../outside", dir)
			_ = os.Remove(dir)
			_ = os.Rename(aside, dir)
		}
	}()
	deadline := time.Now().Add(200 * time.Millisecond)
	reads, escapes := 0, 0
	for time.Now().Before(deadline) {
		got, err := e.read(t, "/dir/canary")
		switch {
		case err == nil:
			reads++
			if got != "inside" {
				close(stop)
				wg.Wait()
				t.Fatalf("read outside content %q through a swapped symlink", got)
			}
		case errors.Is(err, ErrEscape):
			escapes++
		case errors.Is(err, ErrNotFound):
			// window between rename and symlink creation
		default:
			close(stop)
			wg.Wait()
			t.Fatalf("unexpected error: %v", err)
		}
	}
	close(stop)
	wg.Wait()
	t.Logf("reads=%d escapes=%d", reads, escapes)
	if reads == 0 {
		t.Fatal("no successful read; test did not exercise the race")
	}
}

// TestTOCTOUCopyTree swaps a subdirectory for an escaping symlink during
// CopyTree: the copy either completes with in-root data or fails with
// SYMLINK_ESCAPE; the destination never receives outside content.
func TestTOCTOUCopyTree(t *testing.T) {
	e := newEnv(t)
	j := e.j
	if err := j.MkdirAll("/src/sub"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 60; i++ {
		e.write(t, VPath(fmt.Sprintf("/src/sub/f%02d", i)), "inside")
	}
	sub := filepath.Join(e.home, "src", "sub")
	aside := filepath.Join(e.home, "src", "sub.aside")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				_ = os.Remove(sub)
				_ = os.Rename(aside, sub)
				return
			default:
			}
			_ = os.Rename(sub, aside)
			_ = os.Symlink("../../outside", sub)
			time.Sleep(50 * time.Microsecond)
			_ = os.Remove(sub)
			_ = os.Rename(aside, sub)
			time.Sleep(50 * time.Microsecond)
		}
	}()
	deadline := time.Now().Add(250 * time.Millisecond)
	okCopies, escapes := 0, 0
	for i := 0; time.Now().Before(deadline); i++ {
		dst := VPath(fmt.Sprintf("/dst%d", i))
		err := j.CopyTree(context.Background(), "/src", dst, nil)
		switch {
		case err == nil:
			okCopies++
		case errors.Is(err, ErrEscape):
			escapes++
		case errors.Is(err, ErrNotFound):
		default:
			close(stop)
			wg.Wait()
			t.Fatalf("unexpected error: %v", err)
		}
		// Whatever landed in dst must be in-root content.
		_ = j.WalkDir(context.Background(), dst, func(vp string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || d.Type()&fs.ModeSymlink != 0 {
				return nil
			}
			if got, rerr := e.read(t, VPath(vp)); rerr == nil && got != "inside" {
				t.Errorf("outside content copied into %s: %q", vp, got)
			}
			return nil
		})
		_ = j.RemoveAll(dst)
	}
	close(stop)
	wg.Wait()
	t.Logf("copies=%d escapes=%d", okCopies, escapes)
	if b, _ := os.ReadFile(filepath.Join(e.outside, "canary")); !bytes.Equal(b, e.canary) {
		t.Fatal("canary modified")
	}
	if okCopies+escapes == 0 {
		t.Fatal("race not exercised")
	}
}

func BenchmarkReadDir10k(b *testing.B) {
	tmp := b.TempDir()
	for i := 0; i < 10000; i++ {
		if err := os.WriteFile(filepath.Join(tmp, fmt.Sprintf("file%05d.txt", i)), nil, 0o640); err != nil {
			b.Fatal(err)
		}
	}
	j, err := Open(tmp)
	if err != nil {
		b.Fatal(err)
	}
	defer j.Close()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		entries, err := j.ReadDir(Root)
		if err != nil || len(entries) != 10000 {
			b.Fatal(len(entries), err)
		}
	}
}
