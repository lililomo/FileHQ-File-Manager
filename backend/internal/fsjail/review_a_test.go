package fsjail

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Review A (independent review of Steps 1–5): new symlink / TOCTOU fixtures
// beyond the Step 4 suite, plus regressions for the findings.

// swapForEscape replaces home/<rel> (a real directory) with a symlink to the
// outside directory, the way an attacker with shell access would.
func (e *testEnv) swapForEscape(t *testing.T, rel string) {
	t.Helper()
	p := filepath.Join(e.home, rel)
	if err := os.RemoveAll(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(e.outside, p); err != nil {
		t.Fatal(err)
	}
}

func outsideEntries(t *testing.T, e *testEnv) []string {
	t.Helper()
	ents, err := os.ReadDir(e.outside)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, d := range ents {
		names = append(names, d.Name())
	}
	return names
}

// TestReviewAWritesThroughEscapingParent: every mutating operation whose
// parent component is a symlink out of the root must fail with
// SYMLINK_ESCAPE and leave the outside directory untouched.
func TestReviewAWritesThroughEscapingParent(t *testing.T) {
	e := newEnv(t)
	j := e.j
	if err := j.Mkdir("/dir"); err != nil {
		t.Fatal(err)
	}
	e.write(t, "/victim", "x")
	e.swapForEscape(t, "dir")
	ctx := context.Background()
	ops := map[string]func() error{
		"create": func() error { f, err := j.Create("/dir/evil", true); closeIf(f); return err },
		"create-trunc": func() error {
			f, err := j.Create("/dir/canary", false) // would truncate the outside canary
			closeIf(f)
			return err
		},
		"openfile": func() error {
			f, err := j.OpenFile("/dir/evil2", os.O_WRONLY|os.O_CREATE, 0o640)
			closeIf(f)
			return err
		},
		"mkdir":    func() error { return j.Mkdir("/dir/sub") },
		"mkdirall": func() error { return j.MkdirAll("/dir/a/b") },
		"rename":   func() error { return j.Rename("/victim", "/dir/victim") },
		"symlink":  func() error { return j.Symlink("victim", "/dir/l") },
		"chmod":    func() error { return j.Chmod("/dir/canary", 0o777) },
		"chtimes":  func() error { return j.Chtimes("/dir/canary", time.Unix(0, 0), time.Unix(0, 0)) },
		"remove":   func() error { return j.Remove("/dir/canary") },
		"copyfile": func() error { return j.CopyFile(ctx, "/victim", "/dir/copy", nil) },
		"copytree": func() error { return j.CopyTree(ctx, "/victim", "/dir/copy2", nil) },
		"movetree": func() error { return j.MoveTree(ctx, "/victim", "/dir/moved", nil) },
	}
	for name, op := range ops {
		err := op()
		if err == nil || !errors.Is(err, ErrEscape) {
			t.Errorf("%s through escaping parent: got %v, want SYMLINK_ESCAPE", name, err)
		}
		if err != nil && strings.Contains(err.Error(), e.outside) {
			t.Errorf("%s: error leaks host path: %v", name, err)
		}
	}
	// RemoveAll on the link removes the link only.
	if err := j.RemoveAll("/dir"); err != nil {
		t.Fatalf("remove link: %v", err)
	}
	if got := outsideEntries(t, e); len(got) != 1 || got[0] != "canary" {
		t.Fatalf("outside directory modified: %v", got)
	}
	b, err := os.ReadFile(filepath.Join(e.outside, "canary"))
	if err != nil || !bytes.Equal(b, e.canary) {
		t.Fatalf("canary modified: %q %v", b, err)
	}
	if st, _ := os.Lstat(filepath.Join(e.outside, "canary")); st.Mode().Perm() != 0o640 {
		t.Fatalf("canary mode changed: %v", st.Mode())
	}
	if _, err := j.Lstat("/victim"); err != nil {
		t.Fatalf("victim moved away: %v", err)
	}
}

func closeIf(f *os.File) {
	if f != nil {
		_ = f.Close()
	}
}

// TestReviewASymlinkLoopAndChain: a→b→a loops and deep chains are refused,
// classified as broken and never crash the listing.
func TestReviewASymlinkLoopAndChain(t *testing.T) {
	e := newEnv(t)
	j := e.j
	e.hostSymlink(t, "b", "a")
	e.hostSymlink(t, "a", "b")
	if _, err := j.Open("/a"); err == nil || !errors.Is(err, ErrEscape) {
		t.Fatalf("open loop: %v", err)
	}
	if _, err := j.Stat("/a"); err == nil {
		t.Fatal("stat loop succeeded")
	}
	info, err := j.Lstat("/a")
	if err != nil || !info.IsSymlink || !info.SymlinkBroken || info.SymlinkReason != "loop" {
		t.Fatalf("lstat loop: %+v %v", info, err)
	}
	infos, err := j.ReadDir("/")
	if err != nil {
		t.Fatal(err)
	}
	for _, in := range infos {
		if !in.SymlinkBroken || in.SymlinkReason != "loop" {
			t.Fatalf("readdir classification: %+v", in)
		}
	}
	// A relative chain that climbs out at the end is an escape, not a loop.
	if err := j.Mkdir("/d"); err != nil {
		t.Fatal(err)
	}
	e.hostSymlink(t, "d/l2", "l1")
	e.hostSymlink(t, "../../outside/canary", "d/l2")
	if _, err := j.Open("/l1"); err == nil || !errors.Is(err, ErrEscape) {
		t.Fatalf("chain escape: %v", err)
	}
	info, _ = j.Lstat("/d/l2")
	if !info.SymlinkBroken || info.SymlinkReason != "escape" {
		t.Fatalf("chain classification: %+v", info)
	}
}

// TestReviewATOCTOUCreate races Create against a goroutine swapping the
// parent directory with a symlink to the outside: no file may ever be
// created outside the root.
func TestReviewATOCTOUCreate(t *testing.T) {
	e := newEnv(t)
	j := e.j
	if err := j.Mkdir("/dir"); err != nil {
		t.Fatal(err)
	}
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
				_ = os.Remove(dir)
				_ = os.Rename(aside, dir)
				return
			default:
			}
			_ = os.Rename(dir, aside)
			_ = os.Symlink(e.outside, dir)
			_ = os.Remove(dir)
			_ = os.Rename(aside, dir)
		}
	}()
	deadline := time.Now().Add(250 * time.Millisecond)
	created, escapes := 0, 0
	for i := 0; time.Now().Before(deadline); i++ {
		p := VPath(fmt.Sprintf("/dir/f%d", i))
		f, err := j.Create(p, true)
		switch {
		case err == nil:
			created++
			_, _ = f.WriteString("inside")
			_ = f.Close()
		case errors.Is(err, ErrEscape), errors.Is(err, ErrNotFound):
			escapes++
		default:
			close(stop)
			wg.Wait()
			t.Fatalf("unexpected error: %v", err)
		}
		if err := j.MkdirAll(VPath(fmt.Sprintf("/dir/d%d", i))); err != nil && !errors.Is(err, ErrEscape) && !errors.Is(err, ErrNotFound) {
			close(stop)
			wg.Wait()
			t.Fatalf("mkdirall unexpected error: %v", err)
		}
	}
	close(stop)
	wg.Wait()
	t.Logf("created=%d refused=%d", created, escapes)
	if got := outsideEntries(t, e); len(got) != 1 || got[0] != "canary" {
		t.Fatalf("files created outside the root: %v", got)
	}
	if created == 0 {
		t.Fatal("race not exercised")
	}
}

// TestReviewASubRootCloseKeepsParentHandle: SubRoot("/") shares the parent
// handle; closing the sub-jail must not close the parent (finding M-05).
func TestReviewASubRootCloseKeepsParentHandle(t *testing.T) {
	e := newEnv(t)
	sub, err := e.j.SubRoot(Root, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := sub.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := e.j.Lstat(Root); err != nil {
		t.Fatalf("parent handle closed by sub-root Close: %v", err)
	}
	if _, err := e.j.ReadDir(Root); err != nil {
		t.Fatalf("parent unusable after sub-root Close: %v", err)
	}
	// Closing the sub-jail twice is harmless and the sub-jail is unusable.
	if err := sub.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := sub.Lstat(Root); err == nil {
		t.Fatal("closed sub-jail still works")
	}
	// A real sub-root owns its own handle: closing it leaves the parent alone.
	if err := e.j.Mkdir("/share"); err != nil {
		t.Fatal(err)
	}
	real, err := e.j.SubRoot("/share", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := real.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := e.j.Lstat("/share"); err != nil {
		t.Fatalf("parent broken after real sub-root Close: %v", err)
	}
}

// TestReviewASubRootCannotReachParent: links inside a share that point to
// the owner's private files (inside the user root, outside the share) are
// escapes for the sub-jail.
func TestReviewASubRootCannotReachParent(t *testing.T) {
	e := newEnv(t)
	j := e.j
	if err := j.MkdirAll("/private"); err != nil {
		t.Fatal(err)
	}
	e.write(t, "/private/secret", "top-secret")
	if err := j.Mkdir("/share"); err != nil {
		t.Fatal(err)
	}
	if err := j.Symlink("../private/secret", "/share/leak"); err != nil {
		t.Fatalf("link inside user root must be allowed: %v", err)
	}
	if got, err := e.read(t, "/share/leak"); err != nil || got != "top-secret" {
		t.Fatalf("owner follows own link: %q %v", got, err)
	}
	sub, err := j.SubRoot("/share", true)
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	if _, err := sub.Open("/leak"); err == nil || !errors.Is(err, ErrEscape) {
		t.Fatalf("recipient followed link out of the share: %v", err)
	}
	info, err := sub.Lstat("/leak")
	if err != nil || !info.SymlinkBroken || info.SymlinkReason != "escape" {
		t.Fatalf("share lstat: %+v %v", info, err)
	}
	if _, err := sub.Open("/../private/secret"); err == nil || !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("traversal in sub-root: %v", err)
	}
	f, err := sub.Create("/x", true)
	closeIf(f)
	if err == nil || !errors.Is(err, ErrReadOnly) {
		t.Fatalf("read-only share allowed create: %v", err)
	}
}

// TestReviewACleanEdgeCases adds encodings the Step 4 table does not cover.
func TestReviewACleanEdgeCases(t *testing.T) {
	cases := []struct {
		raw, want, reason string
	}{
		{"//a//b//", "/a/b", ""},
		{"a/b/", "/a/b", ""},
		{"/a/é", "/a/é", ""},
		{"/a/é", "/a/é", ""}, // NFD → NFC
		{"/\xc0\xae\xc0\xae/x", "", "invalid_utf8"},
		{"/a\x7f", "", "control_char"},
		{"/a\x1b[31m", "", "control_char"},
		{"/.system", "", "reserved"},
		{"/.system/x", "", "reserved"},
		{"/x/.system", "/x/.system", ""}, // reserved only at the root
		{"/.systemx", "/.systemx", ""},
		{"/.System", "/.System", ""}, // case-sensitive filesystem
		{"/..\\x", "/..\\x", ""},     // backslash is a plain byte in a path segment
		{"/a/.../b", "/a/.../b", ""},
		{"/" + strings.Repeat("ab/", 2048), "", "path_too_long"},
	}
	for _, c := range cases {
		got, err := Clean(c.raw)
		if c.reason == "" {
			if err != nil || got != VPath(c.want) {
				t.Errorf("Clean(%q) = %q, %v; want %q", c.raw, got, err, c.want)
			}
			if err := checkVPath(got); err != nil {
				t.Errorf("Clean(%q) result fails checkVPath: %v", c.raw, err)
			}
			continue
		}
		var fe *Error
		if err == nil || !errors.As(err, &fe) || fe.Reason != c.reason {
			t.Errorf("Clean(%q): got %q, %v; want reason %s", c.raw, got, err, c.reason)
		}
	}
	// Names: the full-path forms above must agree with ValidateName.
	for _, bad := range []string{"a\\b", "a/b", "..", ".", "", "x\x00", "x\x1f", strings.Repeat("é", 128)} {
		if _, err := ValidateName(bad); err == nil {
			t.Errorf("ValidateName(%q) accepted", bad)
		}
	}
}
