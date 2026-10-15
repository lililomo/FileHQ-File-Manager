package fsjail

import (
	"strings"
	"testing"
)

// FuzzNormalize (PROMPT Step R-C task 1): Clean never yields a path with
// empty, "." or ".." segments, control characters or a ".system" prefix, is
// idempotent, and Join cannot be used to leave the directory it starts from.
func FuzzNormalize(f *testing.F) {
	for _, s := range []string{"", "/", "a", "/a/b", "a//b", "/a/b/", "../x", "/a/../b", "./a", "a\x00b", "/.system", "/.system/db",
		"/" + strings.Repeat("a", 256), strings.Repeat("/d", 70), "\\a\\b", "/a/\u200b", "/ä/é", "/a/%2e%2e/b", "/a∕..∕b", "/a/.", "/a/.."} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		vp, err := Clean(raw)
		if err != nil {
			return
		}
		s := string(vp)
		if !strings.HasPrefix(s, "/") || (s != "/" && strings.HasSuffix(s, "/")) || strings.Contains(s, "//") {
			t.Fatalf("Clean(%q) = %q", raw, s)
		}
		if s != "/" {
			for _, seg := range strings.Split(strings.TrimPrefix(s, "/"), "/") {
				if seg == "" || seg == "." || seg == ".." {
					t.Fatalf("Clean(%q) = %q has segment %q", raw, s, seg)
				}
				for _, r := range seg {
					if r < 0x20 || r == 0x7f {
						t.Fatalf("Clean(%q) = %q keeps a control character", raw, s)
					}
				}
			}
		}
		if s == "/.system" || strings.HasPrefix(s, "/.system/") {
			t.Fatalf("Clean(%q) reached the reserved folder: %q", raw, s)
		}
		again, err := Clean(s)
		if err != nil || again != vp {
			t.Fatalf("Clean not idempotent: %q → %q → %q (%v)", raw, s, again, err)
		}
		// Join with the raw input as a name: either refused or exactly one segment below vp
		if jp, jerr := Join(vp, raw); jerr == nil {
			js := string(jp)
			prefix := strings.TrimSuffix(s, "/") + "/"
			if !strings.HasPrefix(js, prefix) || strings.Contains(strings.TrimPrefix(js, prefix), "/") {
				t.Fatalf("Join(%q, %q) = %q escapes or nests", s, raw, js)
			}
		}
	})
}
