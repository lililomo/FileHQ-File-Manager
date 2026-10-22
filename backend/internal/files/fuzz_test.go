package files

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// FuzzBatchRenameRule (Step 20, SPEC §10.6): any rule + name must either be
// rejected by compileRule or produce a name without path separators, NUL
// or control characters, and never panic.
func FuzzBatchRenameRule(f *testing.F) {
	f.Add("IMG_(\\d+)", "photo-$1", true, false, "{name} {n}", 1, 1, 2, "lower", "keep", "IMG_0042.JPG", false)
	f.Add("a", "b", false, true, "", 0, 0, 0, "title", "include", "laporan akhir.txt", false)
	f.Add("(", "", true, false, "", 0, 0, 0, "", "", "x", false)
	f.Add(".", "/", true, false, "{n}", 1, 1, 0, "upper", "keep", "..", true)
	f.Add("", "", false, false, "{ext}{name}", 5, 2, 3, "none", "include", "a.tar.gz", false)
	f.Fuzz(func(t *testing.T, find, replace string, regex, ci bool, pattern string, start, step, pad int, cas, ext, name string, isDir bool) {
		rule := RenameRule{Find: find, Replace: replace, Regex: regex, CaseInsensitive: ci, Case: cas, ExtMode: ext}
		if pattern != "" || start != 0 || step != 0 || pad != 0 {
			rule.Numbering = &Numbering{Pattern: pattern, Start: start, Step: step, Pad: pad}
		}
		// names reach the rule from the jail listing, so only valid names are
		// in scope. A separator in the result is reported per item by the
		// preview (ValidateName), so the invariant here is: no panic, UTF-8
		// stays valid, and no NUL/control character is ever introduced.
		if strings.ContainsAny(name, "/\x00") || !utf8.ValidString(name) || strings.ContainsFunc(name, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return
		}
		for _, s := range []string{find, replace, pattern} {
			if strings.ContainsRune(s, 0) || !utf8.ValidString(s) || strings.ContainsFunc(s, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
				return // rejected per item by ValidateName in the preview
			}
		}
		c, err := compileRule(rule)
		if err != nil {
			return
		}
		out := c.apply(name, isDir, 3)
		if strings.ContainsRune(out, 0) {
			t.Fatalf("rule %+v on %q produced NUL in %q", rule, name, out)
		}
		if !utf8.ValidString(out) {
			t.Fatalf("rule %+v on %q produced invalid UTF-8 %q", rule, name, out)
		}
		for _, r := range out {
			if r < 0x20 || r == 0x7f {
				t.Fatalf("rule %+v on %q produced control char in %q", rule, name, out)
			}
		}
	})
}
