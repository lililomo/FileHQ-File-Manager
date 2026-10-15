package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kppbwi.app/filemanager/internal/fsjail"
)

// Review C probes (PROMPT Step R-C checklist f): reviewer fixtures that the
// Step 14a suite did not contain, kept as regression tests.

// rawZip builds a zip whose central directory lies about the sizes of one
// Store member (CreateRaw writes the header fields as given).
func lyingZip(t *testing.T, name string, body []byte, claimedUncompressed uint64) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	fh := &zip.FileHeader{Name: name, Method: zip.Store, CompressedSize64: uint64(len(body)), UncompressedSize64: claimedUncompressed}
	fh.SetMode(0o644)
	w, err := zw.CreateRaw(fh)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// tmpStrays lists everything in DATA_DIR/tmp that is not an ar-* workspace:
// an escaped entry written next to the extraction root would land there and
// snapshotOutside ignores .system.
func (e *env) tmpStrays(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(e.cfg.DataDir, "tmp"))
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range entries {
		if !strings.HasPrefix(d.Name(), TmpPrefix) {
			out = append(out, d.Name())
		}
	}
	return out
}

// TestReviewCHostileNamesAndLyingHeaders: `..\..\evil` (backslash
// traversal), `./../` in tar, a member whose header claims 0 bytes but
// streams 3 MiB (budget must stop it) and one that claims 100 MiB but holds
// nothing (must not reserve/extract anything odd).
func TestReviewCHostileNamesAndLyingHeaders(t *testing.T) {
	e := newEnv(t, withOptions(func(o *Options) { o.MaxExtractSize = 1 << 20 }))
	check := e.snapshotOutside(t)
	before := e.tmpStrays(t)

	e.write(t, "bs.zip", buildZip(t, []zipEntry{{name: `..\..\evil.txt`, content: []byte("x")}}))
	failedWith(t, e.extract(t, ExtractPayload{Archive: "/bs.zip", Dest: "/o1"}, ""), CodeUnsafe)
	e.write(t, "ds.tar", buildTar(t, []tarEntry{{name: "./../evil2.txt", content: []byte("x")}}))
	failedWith(t, e.extract(t, ExtractPayload{Archive: "/ds.tar", Dest: "/o2"}, ""), CodeUnsafe)
	e.write(t, "mid.tar", buildTar(t, []tarEntry{{name: "a/./../../evil3.txt", content: []byte("x")}}))
	failedWith(t, e.extract(t, ExtractPayload{Archive: "/mid.tar", Dest: "/o3"}, ""), CodeUnsafe)
	e.write(t, "drive.zip", buildZip(t, []zipEntry{{name: `C:/evil4.txt`, content: []byte("x")}}))
	failedWith(t, e.extract(t, ExtractPayload{Archive: "/drive.zip", Dest: "/o4"}, ""), CodeUnsafe)

	// header says 0 bytes, stream holds 3 MiB: the streaming budget (1 MiB) wins
	big := bytes.Repeat([]byte{'z'}, 3<<20)
	e.write(t, "lie0.zip", lyingZip(t, "zero.bin", big, 0))
	tk := e.extract(t, ExtractPayload{Archive: "/lie0.zip", Dest: "/o5"}, "")
	if tk.State == "done" {
		t.Fatalf("lying header extracted: %+v", tk)
	}
	if tk.Error == nil || (tk.Error.Code != CodeLimit && tk.Error.Code != CodeCorrupt) {
		t.Fatalf("lying header: %+v", tk.Error)
	}
	if e.exists("o5") {
		t.Fatal("partial destination left behind")
	}
	// header says 100 MiB, stream holds nothing: must fail cleanly (not reserve 100 MiB forever)
	e.write(t, "lie100.zip", lyingZip(t, "empty.bin", nil, 100<<20))
	tk = e.extract(t, ExtractPayload{Archive: "/lie100.zip", Dest: "/o6"}, "")
	if tk.State == "done" {
		t.Fatalf("100 MiB claim extracted: %+v", tk)
	}
	if u, err := e.quota.Usage(context.Background(), e.user.ID); err != nil || u.Reserved != 0 {
		t.Fatalf("reservation left after failed extract: %+v %v", u, err)
	}
	if left := e.tmpDirs(t); len(left) != 0 {
		t.Fatalf("workspaces left: %v", left)
	}
	if after := e.tmpStrays(t); len(after) != len(before) {
		t.Fatalf("stray files in DATA_DIR/tmp: %v", after)
	}
	for _, n := range []string{"evil.txt", "evil2.txt", "evil3.txt", "evil4.txt"} {
		if e.exists(n) {
			t.Fatalf("%s was created inside the home", n)
		}
	}
	check()
}

// TestReviewCSymlinkThenFileSameName: a symlink member followed by a regular
// member of the same name (and a symlinked folder followed by a file below
// it) must never turn the later write into a write through the link.
func TestReviewCSymlinkThenFileSameName(t *testing.T) {
	e := newEnv(t)
	canary := filepath.Join(e.root, "outside.txt")
	if err := os.WriteFile(canary, []byte("canary"), 0o600); err != nil {
		t.Fatal(err)
	}
	check := e.snapshotOutside(t)
	e.write(t, "ln.tar", buildTar(t, []tarEntry{
		{name: "ln", typeflag: tar.TypeSymlink, link: "../../../outside.txt"},
		{name: "ln", content: []byte("regular wins")},
		{name: "d", typeflag: tar.TypeSymlink, link: "../.."},
		{name: "d/evil.txt", content: []byte("through the link?")},
		{name: "e/inner.txt", content: []byte("ok")},
		{name: "e", typeflag: tar.TypeSymlink, link: "../.."},
	}))
	res := resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/ln.tar", Dest: "/out"}, ""))
	if res.Links != 0 {
		t.Fatalf("a link was created: %+v", res)
	}
	for _, rel := range []string{"out/ln", "out/d/evil.txt", "out/e/inner.txt"} {
		fi, err := os.Lstat(filepath.Join(e.home, rel))
		if err != nil || !fi.Mode().IsRegular() {
			t.Fatalf("%s should be a regular file: %v %v", rel, fi, err)
		}
	}
	if string(e.read(t, "out/ln")) != "regular wins" {
		t.Fatal("regular member did not win over the symlink")
	}
	if b, _ := os.ReadFile(canary); string(b) != "canary" {
		t.Fatal("canary outside the home was modified")
	}
	// zip flavour: symlink mode entry then a file of the same name
	e.write(t, "ln.zip", buildZip(t, []zipEntry{
		{name: "s", symlink: "../../../outside.txt"},
		{name: "s", content: []byte("file")},
		{name: "sd", symlink: "../.."},
		{name: "sd/x.txt", content: []byte("x")},
	}))
	res = resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/ln.zip", Dest: "/out2"}, ""))
	if res.Links != 0 || string(e.read(t, "out2/s")) != "file" || string(e.read(t, "out2/sd/x.txt")) != "x" {
		t.Fatalf("zip symlink clash: %+v", res)
	}
	if b, _ := os.ReadFile(canary); string(b) != "canary" {
		t.Fatal("canary outside the home was modified (zip)")
	}
	check()
}

// TestReviewCSevenZipPasswordSession: the archive browser must hand out a
// session id for password-protected 7z archives too (the index cache key
// has no password, and bodgit reports "encrypted" only through failures).
func TestReviewCSevenZipPasswordSession(t *testing.T) {
	have7z(t)
	e := newEnv(t)
	j := e.jail(t)
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		mhe  bool
	}{{"plain-hdr.7z", false}, {"enc-hdr.7z", true}} {
		e.write(t, tc.name, sevenZipFixture(t, tc.name, "pw-7", tc.mhe, map[string][]byte{"a.txt": []byte("AAA")}))
		resp, err := e.svc.Entries(ctx, j, EntriesRequest{UserID: e.user.ID, Archive: "/" + tc.name, Password: "pw-7"})
		if err != nil {
			t.Fatalf("%s entries: %v", tc.name, err)
		}
		if !resp.Encrypted || len(resp.Session) != 32 {
			t.Fatalf("%s: encrypted=%v session=%q (browser cannot preview without a session)", tc.name, resp.Encrypted, resp.Session)
		}
		sess, ok := e.svc.Session(resp.Session, e.user.ID, "/"+tc.name)
		if !ok || sess.Password != "pw-7" {
			t.Fatalf("%s: session lookup failed", tc.name)
		}
		es, err := e.svc.OpenEntry(ctx, j, e.user.ID, "/"+tc.name, "a.txt", sess.Password, sess.Encoding)
		if err != nil {
			t.Fatalf("%s open entry: %v", tc.name, err)
		}
		b, _ := io.ReadAll(es.Body)
		_ = es.Body.Close()
		if string(b) != "AAA" {
			t.Fatalf("%s entry body: %q", tc.name, b)
		}
		// a wrong password must not get a session
		if _, err := e.svc.Entries(ctx, j, EntriesRequest{UserID: e.user.ID, Archive: "/" + tc.name, Password: "nope"}); !errors.Is(err, ErrPasswordWrong) {
			t.Fatalf("%s wrong password: %v", tc.name, err)
		}
		// an unencrypted listing must still report the archive as encrypted
		// (plain headers) or demand the password (encrypted headers)
		r2, err := e.svc.Entries(ctx, j, EntriesRequest{UserID: e.user.ID, Archive: "/" + tc.name})
		switch {
		case tc.mhe && !errors.Is(err, ErrPasswordRequired):
			t.Fatalf("%s without password: %+v %v", tc.name, r2, err)
		case !tc.mhe && (err != nil || !r2.Encrypted || r2.Session != ""):
			t.Fatalf("%s without password: %+v %v", tc.name, r2, err)
		}
	}
}

// TestReviewCCorruptCLIErrorHidesHostPath: 7-Zip prints the archive path on
// stderr; ARCHIVE_CORRUPT details must not carry STORAGE_ROOT.
func TestReviewCCorruptCLIErrorHidesHostPath(t *testing.T) {
	have7z(t)
	e := newEnv(t)
	j := e.jail(t)
	fake := make([]byte, 0x8001+5+2048)
	copy(fake[0x8001:], "CD001")
	e.write(t, "fake.iso", fake)
	_, err := e.svc.Entries(context.Background(), j, EntriesRequest{UserID: e.user.ID, Archive: "/fake.iso"})
	if err == nil {
		t.Fatal("fake iso listed")
	}
	var ae *Error
	if !errors.As(err, &ae) {
		t.Fatalf("want *Error, got %T %v", err, err)
	}
	raw := ae.Message
	for k, v := range ae.Details {
		raw += " " + k + "=" + toString(v)
	}
	if strings.Contains(raw, e.root) || strings.Contains(raw, e.home) {
		t.Fatalf("host path leaked into the API error: %s", raw)
	}
	tk := e.extract(t, ExtractPayload{Archive: "/fake.iso", Dest: "/iso-out"}, "")
	if tk.State == "done" || tk.Error == nil {
		t.Fatalf("fake iso extracted: %+v", tk)
	}
	if strings.Contains(tk.Error.Message, e.root) || strings.Contains(e.logs.String(), e.home) {
		t.Fatalf("host path leaked into task error/log: %s", tk.Error.Message)
	}
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int:
		return string(rune('0' + x%10))
	}
	return ""
}

// FuzzEntryName: SafePath never yields a path that leaves the extraction
// root, and NormalizeInner is idempotent without "." / ".." segments.
func FuzzEntryName(f *testing.F) {
	for _, s := range []string{"a/b.txt", `..\..\evil`, "./../x", "/etc/passwd", "C:\\x", "a\x00b", "a/../../b", ".system/x",
		"dir/", "../", "a//b", "\\\\server\\share\\x", "a/./b", "ünï/ç.txt", "CON.txt", strings.Repeat("d/", 70) + "f", "%2e%2e/x", "..\u2215x"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		vp, reason, _ := SafePath(name)
		if reason == "" {
			s := string(vp)
			if !strings.HasPrefix(s, "/") || s == "/" || strings.HasSuffix(s, "/") || strings.ContainsRune(s, 0) {
				t.Fatalf("SafePath(%q) = %q", name, s)
			}
			for _, seg := range strings.Split(strings.TrimPrefix(s, "/"), "/") {
				if seg == "" || seg == "." || seg == ".." {
					t.Fatalf("SafePath(%q) = %q has segment %q", name, s, seg)
				}
			}
			if s == "/.system" || strings.HasPrefix(s, "/.system/") {
				t.Fatalf("SafePath(%q) reached .system", name)
			}
			again, err := fsjail.Clean(s)
			if err != nil || again != vp {
				t.Fatalf("SafePath(%q) = %q does not round-trip through fsjail.Clean: %v", name, s, err)
			}
		}
		n, ok := NormalizeInner(name)
		if ok {
			if strings.HasPrefix(n, "/") || strings.HasSuffix(n, "/") || strings.Contains(n, "//") || strings.ContainsRune(n, 0) {
				t.Fatalf("NormalizeInner(%q) = %q", name, n)
			}
			for _, seg := range strings.Split(n, "/") {
				if n != "" && (seg == "" || seg == "." || seg == "..") {
					t.Fatalf("NormalizeInner(%q) = %q has segment %q", name, n, seg)
				}
			}
			if n2, ok2 := NormalizeInner(n); !ok2 || n2 != n {
				t.Fatalf("NormalizeInner not idempotent: %q → %q → %q", name, n, n2)
			}
		}
	})
}
