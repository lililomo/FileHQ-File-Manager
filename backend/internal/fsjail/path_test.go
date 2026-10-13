package fsjail

import (
	"errors"
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

func TestClean(t *testing.T) {
	long := strings.Repeat("a", 256)
	deep := "/" + strings.TrimSuffix(strings.Repeat("d/", 65), "/")
	cases := []struct {
		in     string
		want   VPath
		reason string
	}{
		{"", Root, ""},
		{"/", Root, ""},
		{"///", Root, ""},
		{"a", "/a", ""},
		{"/a/b", "/a/b", ""},
		{"a//b", "/a/b", ""},
		{"/a/b/", "/a/b", ""},
		{"/link/", "/link", ""},
		{"/etc/passwd", "/etc/passwd", ""}, // virtual: inside the user root
		{"../x", "", "parent_segment"},
		{"/a/../b", "", "parent_segment"},
		{"a/../../b", "", "parent_segment"},
		{"/a/..", "", "parent_segment"},
		{"./a", "", "dot_segment"},
		{"/a/./b", "", "dot_segment"},
		{"a\x00b", "", "control_char"},
		{"a\nb", "", "control_char"},
		{"a\x7fb", "", "control_char"},
		{"/" + long, "", "segment_too_long"},
		{deep, "", "too_deep"},
		{"/.system", "", "reserved"},
		{"/.system/db", "", "reserved"},
		{"/a/.system", "/a/.system", ""},
		{"\xff\xfe", "", "invalid_utf8"},
		{"/" + strings.Repeat("a", 255), VPath("/" + strings.Repeat("a", 255)), ""},
	}
	for _, c := range cases {
		got, err := Clean(c.in)
		if c.reason == "" {
			if err != nil || got != c.want {
				t.Errorf("Clean(%q) = %q, %v; want %q", c.in, got, err, c.want)
			}
			continue
		}
		var fe *Error
		if !errors.As(err, &fe) || fe.Code != CodePathInvalid || fe.Reason != c.reason || !errors.Is(err, ErrInvalidPath) {
			t.Errorf("Clean(%q) = %q, %v; want PATH_INVALID/%s", c.in, got, err, c.reason)
		}
	}
	// path > 4096 bytes
	tooLong := "/" + strings.Repeat(strings.Repeat("a", 200)+"/", 21)
	if _, err := Clean(tooLong); err == nil {
		t.Error("expected path_too_long")
	}
	if v, err := Normalize("x"); err != nil || v != "/x" {
		t.Error("Normalize alias")
	}
}

func TestCleanNFC(t *testing.T) {
	nfd := "é"
	nfc := "é"
	a, err := Clean("/" + nfd + ".txt")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := Clean("/" + nfc + ".txt")
	if a != b || string(a) != "/"+norm.NFC.String(nfd)+".txt" {
		t.Fatalf("NFD and NFC must normalize to the same VPath: %q vs %q", a, b)
	}
}

func TestVPathHelpers(t *testing.T) {
	v := VPath("/a/b/c")
	if v.Dir() != "/a/b" || v.Base() != "c" || v.Rel() != "a/b/c" || v.Depth() != 3 || v.IsRoot() {
		t.Fatal("helpers")
	}
	if Root.Dir() != Root || Root.Base() != "" || Root.Rel() != "." || Root.Depth() != 0 || !Root.IsRoot() || VPath("").IsRoot() == false {
		t.Fatal("root helpers")
	}
	if VPath("/a").Dir() != Root {
		t.Fatal("dir of top-level")
	}
	if !v.HasPrefix("/a") || !v.HasPrefix("/a/b/c") || !v.HasPrefix(Root) || v.HasPrefix("/a/b/cd") || VPath("/ab").HasPrefix("/a") {
		t.Fatal("HasPrefix must be segment-aware")
	}
	if v.String() != "/a/b/c" {
		t.Fatal("String")
	}
}

func TestJoin(t *testing.T) {
	if v, err := Join(Root, "x"); err != nil || v != "/x" {
		t.Fatal(v, err)
	}
	if v, err := Join("/a", "b"); err != nil || v != "/a/b" {
		t.Fatal(v, err)
	}
	if _, err := Join(Root, ".system"); !errors.Is(err, ErrInvalidName) {
		t.Fatal("reserved at root must be NAME_INVALID", err)
	}
	if v, err := Join("/a", ".system"); err != nil || v != "/a/.system" {
		t.Fatal("reserved name is allowed below root", v, err)
	}
	if _, err := Join(Root, "a/b"); !errors.Is(err, ErrInvalidName) {
		t.Fatal("slash")
	}
	deep := VPath("/" + strings.TrimSuffix(strings.Repeat("d/", 64), "/"))
	if _, err := Join(deep, "x"); !errors.Is(err, ErrInvalidPath) {
		t.Fatal("too deep")
	}
	if v, _ := Join("/a", "é"); v != "/a/é" {
		t.Fatal("Join must NFC-normalize the name")
	}
}

func TestValidateName(t *testing.T) {
	bad := map[string]string{
		"": "empty", ".": "dot", "..": "dot", "a/b": "slash", "a\x00": "nul", "a\tb": "control_char",
		"a\\b": "backslash", strings.Repeat("x", 256): "too_long", "\xff": "invalid_utf8",
	}
	for name, reason := range bad {
		_, err := ValidateName(name)
		var fe *Error
		if !errors.As(err, &fe) || fe.Code != CodeNameInvalid || fe.Reason != reason {
			t.Errorf("ValidateName(%q) = %v; want NAME_INVALID/%s", name, err, reason)
		}
	}
	warn := []string{"CON", "con.txt", "Lpt1", "aux.tar.gz", "trailing.", "trailing ", "a:b", "q?", "x|y", "<a>", `"q"`, "star*"}
	for _, name := range warn {
		w, err := ValidateName(name)
		if err != nil || len(w) != 1 || w[0] != WarnWindowsIncompatible {
			t.Errorf("ValidateName(%q) = %v, %v; want windows_incompatible warning", name, w, err)
		}
	}
	ok := []string{"a", ".hidden", "normal name.txt", "ünïcödé", "console", "lpt10", strings.Repeat("x", 255), "a b"}
	for _, name := range ok {
		if w, err := ValidateName(name); err != nil || len(w) != 0 {
			t.Errorf("ValidateName(%q) = %v, %v; want ok", name, w, err)
		}
	}
}

func TestResolveLink(t *testing.T) {
	cases := []struct {
		dir    VPath
		target string
		want   VPath
		ok     bool
	}{
		{"/a", "b", "/a/b", true},
		{"/a", "../b", "/b", true},
		{"/a", "..", "/", true},
		{"/a", "../..", "", false},
		{"/a/b", "../../c/./d", "/c/d", true},
		{"/a", "/abs", "", false},
		{"/a", "", "", false},
		{"/", "x/../y", "/y", true},
	}
	for _, c := range cases {
		got, ok := resolveLink(c.dir, c.target)
		if ok != c.ok || got != c.want {
			t.Errorf("resolveLink(%q,%q) = %q,%v want %q,%v", c.dir, c.target, got, ok, c.want, c.ok)
		}
	}
}

func TestAPIError(t *testing.T) {
	cases := []struct {
		err  error
		code string
	}{
		{newErr(CodeNotFound, "x", "/p", "", nil), "NOT_FOUND"},
		{newErr(CodeAlreadyExists, "x", "/p", "", nil), "ALREADY_EXISTS"},
		{newErr(CodeSymlinkEscape, "x", "/p", "escape", nil), "SYMLINK_ESCAPE"},
		{newErr(CodeNotEmpty, "x", "/p", "not_empty", nil), "CONFLICT"},
		{newErr(CodeIsDir, "x", "/p", "", nil), "VALIDATION_FAILED"},
		{newErr(CodeInternal, "x", "/p", "", nil), "INTERNAL"},
		{lockedErr("/p", "locked", "task:1", ErrLocked), "OPERATION_IN_PROGRESS"},
		{ErrLocked, "OPERATION_IN_PROGRESS"},
		{ErrReadOnly, "FORBIDDEN"},
		{ErrHomeMissing, "INTERNAL"},
	}
	for _, c := range cases {
		code, details, ok := APIError(c.err)
		if !ok || code != c.code {
			t.Errorf("APIError(%v) = %s,%v,%v want %s", c.err, code, details, ok, c.code)
		}
	}
	if _, _, ok := APIError(errors.New("other")); ok {
		t.Error("foreign error must not map")
	}
	code, details, _ := APIError(lockedErr("/p", "locked", "task:1", ErrLocked))
	if code != "OPERATION_IN_PROGRESS" || details["by"] != "task:1" || details["path"] != "/p" {
		t.Error("lock details", details)
	}
	e := newErr(CodeNotFound, "open", "/x", "", errors.New("/abs/host/path: no such file"))
	if strings.Contains(e.Error(), "/abs/host") {
		t.Error("Error() must not leak the underlying host path:", e.Error())
	}
	if !errors.Is(e, ErrNotFound) || errors.Is(e, ErrExists) {
		t.Error("Is mapping")
	}
}
