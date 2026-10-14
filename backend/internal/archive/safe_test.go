package archive

import (
	"archive/tar"
	"os"
	"path/filepath"
	"testing"
)

func TestSafePath(t *testing.T) {
	cases := []struct {
		raw, want, reason string
	}{
		{"a/b.txt", "/a/b.txt", ""},
		{"./a/./b", "/a/b", ""},
		{"a\\b\\c.txt", "/a/b/c.txt", ""},
		{"dir/", "/dir", ""},
		{"../../evil.txt", "", ReasonDotDot},
		{"sub/../../evil2.txt", "", ReasonDotDot},
		{"/etc/evil", "", ReasonAbsolute},
		{"\\etc\\evil", "", ReasonAbsolute},
		{"C:\\evil.txt", "", ReasonDrive},
		{"bad\x00name", "", ReasonNUL},
		{"ctl\x07.txt", "", ReasonControl},
		{"", "", ReasonEmpty},
		{".", "", ReasonEmpty},
		{".system/x", "", ReasonSystem},
	}
	for _, c := range cases {
		got, reason, _ := SafePath(c.raw)
		if string(got) != c.want || reason != c.reason {
			t.Errorf("SafePath(%q) = %q,%q want %q,%q", c.raw, got, reason, c.want, c.reason)
		}
	}
	deep := ""
	for i := 0; i < 70; i++ {
		deep += "d/"
	}
	if _, reason, _ := SafePath(deep + "f"); reason != ReasonTooDeep {
		t.Errorf("deep path: %s", reason)
	}
	if _, reason, warnings := SafePath("CON.txt"); reason != "" || len(warnings) == 0 {
		t.Errorf("windows reserved should warn, not reject: %s %v", reason, warnings)
	}
	if LinkInside("/a/l", "../b") != true || LinkInside("/a/l", "../../x") != false || LinkInside("/l", "/etc/passwd") != false {
		t.Error("LinkInside")
	}
}

// TestDangerousFixturesNeverEscape covers PROMPT Step 14a AC 1: every
// hostile fixture is refused or its entry skipped, and nothing is written
// outside the destination (walk of the parent temp dir).
func TestDangerousFixturesNeverEscape(t *testing.T) {
	e := newEnv(t)
	canary := filepath.Join(e.root, "outside.txt")
	if err := os.WriteFile(canary, []byte("canary"), 0o600); err != nil {
		t.Fatal(err)
	}
	check := e.snapshotOutside(t)
	fixtures := map[string][]byte{
		"slip.zip":            buildZip(t, []zipEntry{{name: "../../evil.txt", content: []byte("x")}, {name: "ok.txt", content: []byte("ok")}}),
		"slip2.zip":           buildZip(t, []zipEntry{{name: "sub/../../evil2.txt", content: []byte("x")}}),
		"abs.zip":             buildZip(t, []zipEntry{{name: "/etc/evil", content: []byte("x")}}),
		"drive.zip":           buildZip(t, []zipEntry{{name: "C:\\evil.txt", content: []byte("x")}}),
		"symlink-escape.tar":  buildTar(t, []tarEntry{{name: "x", typeflag: tar.TypeSymlink, link: "../../outside"}, {name: "x/evil", content: []byte("pwned")}}),
		"symlink-abs.tar":     buildTar(t, []tarEntry{{name: "p", typeflag: tar.TypeSymlink, link: "/etc/passwd"}, {name: "p/evil", content: []byte("pwned")}}),
		"hardlink-escape.tar": buildTar(t, []tarEntry{{name: "h", typeflag: tar.TypeLink, link: "../../etc/passwd"}}),
		"setuid.tar":          buildTar(t, []tarEntry{{name: "s", content: []byte("#!/bin/sh"), mode: 0o4755}}),
		"device.tar":          buildTar(t, []tarEntry{{name: "dev", typeflag: tar.TypeChar}, {name: "fifo", typeflag: tar.TypeFifo}, {name: "f.txt", content: []byte("f")}}),
		"zip-symlink.zip":     buildZip(t, []zipEntry{{name: "lnk", symlink: "../../../etc/passwd"}, {name: "inside", symlink: "ok.txt"}, {name: "ok.txt", content: []byte("ok")}}),
		"windows-names.zip":   buildZip(t, []zipEntry{{name: "CON.txt", content: []byte("c")}, {name: "a|b.txt", content: []byte("p")}, {name: "trailing. ", content: []byte("t")}, {name: "fine.txt", content: []byte("f")}}),
		"blocked.zip":         buildZip(t, []zipEntry{{name: "run.exe", content: []byte("MZ")}, {name: "ok.txt", content: []byte("ok")}}),
	}
	for name, b := range fixtures {
		e.write(t, "in/"+name, b)
	}
	// whole-archive refusals (hostile names)
	for _, name := range []string{"slip.zip", "slip2.zip", "abs.zip", "drive.zip"} {
		tk := e.extract(t, ExtractPayload{Archive: "/in/" + name, Dest: "/out-" + name}, "")
		failedWith(t, tk, CodeUnsafe)
		if e.exists("out-" + name) {
			t.Fatalf("%s: destination created", name)
		}
	}
	// link / special entries are skipped, regular files extracted
	res := resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/in/symlink-escape.tar", Dest: "/o1"}, ""))
	if st, err := os.Lstat(filepath.Join(e.home, "o1", "x")); err != nil || !st.IsDir() || !hasSkip(res, "x", ReasonLinkEscape) {
		t.Fatalf("symlink escape: %+v %v", res, err)
	}
	// x/evil was written as a regular file under a real directory, never through a link
	if string(e.read(t, "o1/x/evil")) != "pwned" {
		t.Fatal("x/evil should exist as a plain file inside o1")
	}
	res = resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/in/symlink-abs.tar", Dest: "/o2"}, ""))
	if !hasSkip(res, "p", ReasonLinkEscape) {
		t.Fatalf("absolute symlink: %+v", res)
	}
	res = resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/in/hardlink-escape.tar", Dest: "/o3"}, ""))
	if !hasSkip(res, "h", ReasonHardlink) || e.exists("o3/h") {
		t.Fatalf("hardlink escape: %+v", res)
	}
	resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/in/setuid.tar", Dest: "/o4"}, ""))
	if st, err := os.Lstat(filepath.Join(e.home, "o4", "s")); err != nil || st.Mode()&os.ModeSetuid != 0 || st.Mode().Perm() != 0o640 {
		t.Fatalf("setuid kept or mode wrong: %v %v", st.Mode(), err)
	}
	res = resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/in/device.tar", Dest: "/o5"}, ""))
	if !hasSkip(res, "dev", ReasonSpecial) || !hasSkip(res, "fifo", ReasonSpecial) || string(e.read(t, "o5/f.txt")) != "f" {
		t.Fatalf("device entries: %+v", res)
	}
	res = resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/in/zip-symlink.zip", Dest: "/o6"}, ""))
	if !hasSkip(res, "lnk", ReasonLinkEscape) || res.Links != 1 {
		t.Fatalf("zip symlinks: %+v", res)
	}
	if target, err := os.Readlink(filepath.Join(e.home, "o6", "inside")); err != nil || target != "ok.txt" {
		t.Fatalf("inside link: %q %v", target, err)
	}
	res = resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/in/windows-names.zip", Dest: "/o7"}, ""))
	if !e.exists("o7/fine.txt") || res.Warnings == 0 {
		t.Fatalf("windows names: %+v", res)
	}
	res = resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/in/blocked.zip", Dest: "/o8"}, ""))
	if e.exists("o8/run.exe") || !hasSkip(res, "run.exe", ReasonBlockedExt) || !e.exists("o8/ok.txt") {
		t.Fatalf("blocked extension: %+v", res)
	}
	check()
	if b, _ := os.ReadFile(canary); string(b) != "canary" {
		t.Fatal("canary modified")
	}
	if n := e.tmpDirs(t); len(n) != 0 {
		t.Fatalf("tmp left behind: %v", n)
	}
}

func hasSkip(res ExtractResult, name, reason string) bool {
	for _, s := range res.Skipped {
		if s.Name == name && s.Reason == reason {
			return true
		}
	}
	return false
}
