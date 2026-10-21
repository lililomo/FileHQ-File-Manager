package search

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Review D probes (PROMPT Step R-D checklist d, e): ripgrep argument
// hygiene and the replace task against binary files, symlinks swapped in
// after the preview, mode preservation and staging leftovers.

// TestReviewDRgArgsNeverBecomeFlags: whatever the user types, the pattern
// travels behind -e, the paths behind --, symlinks are never followed and
// the extension filter cannot start with a dash.
func TestReviewDRgArgsNeverBecomeFlags(t *testing.T) {
	s := New(Options{MaxFileSize: 1 << 20})
	for _, q := range []string{"-e x", "--files", "-", "--pre=sh", "-g *", "--max-count=0", "-f /etc/passwd", "--", "-C 9999 ."} {
		pat, err := compile(ContentQuery{Path: "/", Q: q, Regex: false, Recursive: true, Ext: []string{"-txt", "--exclude", "x y", ""}, MaxSize: 1 << 20})
		if err != nil {
			t.Fatalf("%q: %v", q, err)
		}
		args := s.rgArgs(pat, true)
		joined := strings.Join(args, "\x00")
		ei := indexOf(args, "-e")
		if ei < 0 || args[ei+1] != q {
			t.Fatalf("%q: pattern not behind -e: %q", q, args)
		}
		if lastIndexOf(args, "--") != len(args)-2 || args[len(args)-1] != "." {
			t.Fatalf("%q: paths not behind --: %q", q, args)
		}
		if !strings.Contains(joined, "\x00--no-follow\x00") || !strings.Contains(joined, "\x00--no-config\x00") || !strings.Contains(joined, "\x00-F\x00") {
			t.Fatalf("%q: flags missing: %q", q, args)
		}
		for i, a := range args {
			if i > ei+1 && i < len(args)-2 && strings.HasPrefix(a, "-") && args[i-1] != "-g" && a != "-g" && a != "--max-depth" {
				t.Fatalf("%q: user data turned into a flag at %d: %q", q, i, args)
			}
			if args[i] == "-g" && !strings.HasPrefix(args[i+1], "*.") && !strings.HasPrefix(args[i+1], "!") {
				t.Fatalf("%q: ext glob %q does not start with *.", q, args[i+1])
			}
		}
	}
	// the regex engine rejects what RE2 cannot compile before rg is spawned
	if _, err := compile(ContentQuery{Q: "(a+)+$\\1", Regex: true}); !IsCode(err, CodeValidation) {
		t.Fatalf("backreference accepted: %v", err)
	}
	if _, err := compile(ContentQuery{Q: "(?=x)", Regex: true}); !IsCode(err, CodeValidation) {
		t.Fatalf("lookahead accepted: %v", err)
	}
}

func lastIndexOf(args []string, s string) int {
	for i := len(args) - 1; i >= 0; i-- {
		if args[i] == s {
			return i
		}
	}
	return -1
}

func indexOf(args []string, s string) int {
	for i, a := range args {
		if a == s {
			return i
		}
	}
	return -1
}

// TestReviewDReplaceBinarySymlinkModeAndStaging: binary files are skipped
// at preview time, a file swapped for a symlink after the preview is
// skipped at apply time, the file mode survives the atomic write, the
// pre-image version holds the exact old bytes and no staging file is left
// behind (checklist d).
func TestReviewDReplaceBinarySymlinkModeAndStaging(t *testing.T) {
	for _, engine := range []string{"go", "rg"} {
		t.Run(engine, func(t *testing.T) {
			if engine == "rg" {
				haveRg(t)
			}
			e := newEnv(t, withEngine(engine), withEnv("FILE_VERSION_LIMIT=5"))
			e.write(t, "ok.sh", "#!/bin/sh\necho needle\n")
			if err := os.Chmod(filepath.Join(e.home, "ok.sh"), 0o750); err != nil {
				t.Fatal(err)
			}
			e.write(t, "bin.dat", "needle\x00needle\n")
			e.write(t, "swap.txt", "needle here\n")
			e.write(t, "crlf.txt", "needle\r\nneedle\r\n")
			ctx := context.Background()
			j := e.jail(t)
			tk, err := e.svc.Preview(ctx, j, e.user.ID, PreviewPayload{ContentQuery: ContentQuery{Path: "/", Q: "needle", Recursive: true}, Replace: "pin"})
			if err != nil {
				t.Fatal(err)
			}
			pv := previewOf(t, e.run(t, tk))
			byPath := map[string]PreviewFile{}
			for _, f := range pv.Files {
				byPath[f.Path] = f
			}
			if _, binary := byPath["/bin.dat"]; binary && byPath["/bin.dat"].Skipped != "binary" {
				t.Fatalf("binary file in preview: %+v", byPath["/bin.dat"])
			}
			if byPath["/ok.sh"].Replacements != 1 || byPath["/crlf.txt"].Replacements != 2 || byPath["/swap.txt"].Replacements != 1 {
				t.Fatalf("preview %+v", pv)
			}
			// swap.txt becomes a symlink to ok.sh before apply
			if err := os.Remove(filepath.Join(e.home, "swap.txt")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("ok.sh", filepath.Join(e.home, "swap.txt")); err != nil {
				t.Fatal(err)
			}
			at, err := e.svc.Apply(ctx, e.user.ID, ApplyPayload{PreviewID: tk.ID})
			if err != nil {
				t.Fatal(err)
			}
			res := applyOf(t, e.run(t, at))
			status := map[string]ApplyFile{}
			for _, f := range res.Files {
				status[f.Path] = f
			}
			if status["/swap.txt"].Status != "skipped" || status["/swap.txt"].Reason != "unreadable" {
				t.Fatalf("symlink swap: %+v", status["/swap.txt"])
			}
			if status["/ok.sh"].Status != "ok" || status["/crlf.txt"].Status != "ok" || res.Replacements != 3 {
				t.Fatalf("apply %+v", res)
			}
			if got := e.read(t, "ok.sh"); got != "#!/bin/sh\necho pin\n" {
				t.Fatalf("ok.sh %q", got)
			}
			if got := e.read(t, "crlf.txt"); got != "pin\r\npin\r\n" {
				t.Fatalf("crlf.txt %q", got)
			}
			if got := e.read(t, "bin.dat"); got != "needle\x00needle\n" {
				t.Fatalf("binary touched: %q", got)
			}
			if target, _ := os.Readlink(filepath.Join(e.home, "swap.txt")); target != "ok.sh" {
				t.Fatalf("symlink replaced: %q", target)
			}
			st, err := os.Stat(filepath.Join(e.home, "ok.sh"))
			if err != nil || st.Mode().Perm() != 0o750 {
				t.Fatalf("mode after replace: %v %v", st.Mode(), err)
			}
			// pre-image version = exact old bytes
			vs, err := e.versions.List(ctx, e.user.ID, "/ok.sh")
			if err != nil || len(vs) != 1 || vs[0].Reason != "replace_in_files" || vs[0].ID != status["/ok.sh"].VersionID {
				t.Fatalf("versions %+v %v", vs, err)
			}
			f, _, err := e.versions.Open(ctx, e.user.ID, vs[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			old := make([]byte, 64)
			n, _ := f.Read(old)
			_ = f.Close()
			if string(old[:n]) != "#!/bin/sh\necho needle\n" {
				t.Fatalf("pre-image %q", old[:n])
			}
			// no staging leftovers in the home, preview dir gone
			entries, _ := os.ReadDir(e.home)
			for _, en := range entries {
				if strings.Contains(en.Name(), ".fm-tmp-") {
					t.Fatalf("staging file left: %s", en.Name())
				}
			}
			if _, err := os.Stat(filepath.Join(e.cfg.DataDir, "tmp", "sr-"+tk.ID)); !os.IsNotExist(err) {
				t.Fatalf("preview dir not removed: %v", err)
			}
			// the preview is single-use
			if _, err := e.svc.Apply(ctx, e.user.ID, ApplyPayload{PreviewID: tk.ID}); !IsCode(err, CodeNotFound) {
				t.Fatalf("preview reusable: %v", err)
			}
		})
	}
}
