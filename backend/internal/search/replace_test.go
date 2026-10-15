package search

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kppbwi.app/filemanager/internal/tasks"
	"kppbwi.app/filemanager/internal/versions"
)

func previewOf(t *testing.T, tk tasks.Task) PreviewResult {
	t.Helper()
	if tk.State != tasks.Done {
		t.Fatalf("preview task %s: %+v", tk.State, tk.Error)
	}
	var res PreviewResult
	if err := json.Unmarshal(tk.Result, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func applyOf(t *testing.T, tk tasks.Task) ApplyResult {
	t.Helper()
	if tk.State != tasks.Done {
		t.Fatalf("apply task %s: %+v", tk.State, tk.Error)
	}
	var res ApplyResult
	if err := json.Unmarshal(tk.Result, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

// TestReplaceFiftyFiles (AC 4): preview then apply over 50 files creates
// 50 pre-image versions, logs one line per file, keeps mode/EOL and lets a
// version restore bring the old content back.
func TestReplaceFiftyFiles(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 50; i++ {
		eol := "\n"
		if i%2 == 1 {
			eol = "\r\n"
		}
		e.write(t, fmt.Sprintf("cfg/site-%02d.conf", i), "server_name old.example.com;"+eol+"listen 80;"+eol+"# old.example.com mirror"+eol)
	}
	if err := os.Chmod(filepath.Join(e.home, "cfg/site-07.conf"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.write(t, "cfg/unrelated.txt", "nothing\n")
	ctx := context.Background()
	j := e.jail(t)
	tk, err := e.svc.Preview(ctx, j, e.user.ID, PreviewPayload{ContentQuery: ContentQuery{Path: "/cfg", Q: "old.example.com", Recursive: true}, Replace: "new.example.net"})
	if err != nil {
		t.Fatal(err)
	}
	pv := previewOf(t, e.run(t, tk))
	if pv.TotalFiles != 50 || pv.TotalReplacements != 100 || pv.PreviewID != tk.ID {
		t.Fatalf("preview %+v", pv)
	}
	for i, f := range pv.Files {
		if i < PreviewFullFiles && (len(f.Hunks) != 2 || f.HunksTotal != 2 || f.Replacements != 2) {
			t.Fatalf("file %d hunks %+v", i, f)
		}
		if i >= PreviewFullFiles && f.Hunks != nil {
			t.Fatalf("file %d should carry counts only", i)
		}
	}
	if pv.Files[0].Hunks[0].After != "server_name new.example.net;" || pv.Files[0].Hunks[0].Line != 1 {
		t.Fatalf("hunk %+v", pv.Files[0].Hunks[0])
	}
	// preview dir exists under tmp
	if _, err := os.Stat(filepath.Join(e.cfg.DataDir, "tmp", "sr-"+tk.ID, "preview.json")); err != nil {
		t.Fatal(err)
	}
	// apply (all files)
	at, err := e.svc.Apply(ctx, e.user.ID, ApplyPayload{PreviewID: tk.ID})
	if err != nil {
		t.Fatal(err)
	}
	final := e.run(t, at)
	res := applyOf(t, final)
	if res.Replacements != 100 || len(res.VersionIDs) != 50 || res.Skipped != 0 || len(res.Files) != 50 {
		t.Fatalf("apply %+v", res)
	}
	if got := e.read(t, "cfg/site-01.conf"); got != "server_name new.example.net;\r\nlisten 80;\r\n# new.example.net mirror\r\n" {
		t.Fatalf("CRLF file after replace: %q", got)
	}
	if got := e.read(t, "cfg/site-00.conf"); got != "server_name new.example.net;\nlisten 80;\n# new.example.net mirror\n" {
		t.Fatalf("LF file after replace: %q", got)
	}
	if st, _ := os.Stat(filepath.Join(e.home, "cfg/site-07.conf")); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode not kept: %v", st.Mode())
	}
	logs, err := e.engine.Logs(ctx, e.user.ID, at.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	perFile := 0
	for _, l := range logs {
		if strings.Contains(l.Message, "penggantian (versi") {
			perFile++
		}
	}
	if perFile != 50 {
		t.Fatalf("per-file log lines %d", perFile)
	}
	// versions: one per file, reason replace_in_files, bytes accounted
	vs, err := e.versions.List(ctx, e.user.ID, "/cfg/site-07.conf")
	if err != nil || len(vs) != 1 || vs[0].Reason != versions.ReasonReplaceInFiles {
		t.Fatalf("versions %+v %v", vs, err)
	}
	usage, err := e.quota.Usage(ctx, e.user.ID)
	if err != nil || usage.Versions <= 0 {
		t.Fatalf("usage %+v %v", usage, err)
	}
	// restore one pre-image
	rr, err := e.versions.Restore(ctx, j, e.user.ID, vs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if got := e.read(t, "cfg/site-07.conf"); !strings.Contains(got, "old.example.com") {
		t.Fatalf("restore: %q", got)
	}
	if rr.Version == nil || rr.Version.Reason != versions.ReasonRestore {
		t.Fatalf("restore result %+v", rr)
	}
	if st, _ := os.Stat(filepath.Join(e.home, "cfg/site-07.conf")); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode after restore: %v", st.Mode())
	}
	// preview is gone after apply
	if _, err := e.svc.Apply(ctx, e.user.ID, ApplyPayload{PreviewID: tk.ID}); !IsCode(err, CodeNotFound) {
		t.Fatalf("reuse preview: %v", err)
	}
	// activity rows
	acts, err := e.store.ListActivity(ctx, e.user.ID, 0, 200)
	if err != nil {
		t.Fatal(err)
	}
	replaced := 0
	for _, a := range acts {
		if a.Action == "replace" {
			replaced++
		}
	}
	if replaced != 50 {
		t.Fatalf("activity rows %d", replaced)
	}
}

// TestReplaceRegexGroupsSubsetAndChangedFile: $1 groups, a subset of
// files, a file modified after the preview is skipped (hash mismatch), a
// non-UTF-8 file is listed as skipped, a foreign path in files → error.
func TestReplaceRegexGroupsSubsetAndChangedFile(t *testing.T) {
	e := newEnv(t)
	e.write(t, "a.txt", "name: alpha\nname: beta\n")
	e.write(t, "b.txt", "name: gamma\n")
	e.write(t, "c.txt", "name: delta\n")
	e.write(t, "latin1.txt", "name: alpha\ncaf\xe9\n")
	ctx := context.Background()
	j := e.jail(t)
	tk, err := e.svc.Preview(ctx, j, e.user.ID, PreviewPayload{ContentQuery: ContentQuery{Path: "/", Q: `^name: (\w+)$`, Regex: true, Recursive: true}, Replace: "label=${1}!"})
	if err != nil {
		t.Fatal(err)
	}
	pv := previewOf(t, e.run(t, tk))
	if pv.TotalFiles != 4 || pv.TotalReplacements != 4 {
		t.Fatalf("preview %+v", pv)
	}
	var skipped string
	for _, f := range pv.Files {
		if f.Path == "/latin1.txt" {
			skipped = f.Skipped
		}
	}
	if skipped != "encoding" {
		t.Fatalf("latin1 skipped=%q", skipped)
	}
	if _, err := e.svc.Apply(ctx, e.user.ID, ApplyPayload{PreviewID: tk.ID, Files: []string{"/latin1.txt"}}); !IsCode(err, CodeValidation) {
		t.Fatalf("skipped file selectable: %v", err)
	}
	if _, err := e.svc.Apply(ctx, e.user.ID, ApplyPayload{PreviewID: tk.ID, Files: []string{"/nope.txt"}}); !IsCode(err, CodeValidation) {
		t.Fatalf("foreign file: %v", err)
	}
	e.write(t, "b.txt", "name: gamma changed\n") // modified after the preview
	at, err := e.svc.Apply(ctx, e.user.ID, ApplyPayload{PreviewID: tk.ID, Files: []string{"/a.txt", "/b.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	res := applyOf(t, e.run(t, at))
	if res.Replacements != 2 || res.Skipped != 1 || len(res.Files) != 2 {
		t.Fatalf("apply %+v", res)
	}
	for _, f := range res.Files {
		if f.Path == "/b.txt" && (f.Status != "skipped" || f.Reason != "changed") {
			t.Fatalf("changed file %+v", f)
		}
	}
	if got := e.read(t, "a.txt"); got != "label=alpha!\nlabel=beta!\n" {
		t.Fatalf("a.txt %q", got)
	}
	if got := e.read(t, "c.txt"); got != "name: delta\n" {
		t.Fatalf("c.txt touched: %q", got)
	}
	// another user's preview id is unknown
	if _, err := e.svc.Apply(ctx, "someone-else", ApplyPayload{PreviewID: tk.ID}); !IsCode(err, CodeNotFound) {
		t.Fatalf("foreign preview: %v", err)
	}
}

func TestReplaceLinesKeepsEOLAndCountsHunks(t *testing.T) {
	pat, err := compile(ContentQuery{Q: "x", CaseSensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	out, hunks, total, n := replaceLines([]byte("x\r\nyx\nno\nx"), pat, "Z")
	if string(out) != "Z\r\nyZ\nno\nZ" || total != 3 || n != 3 || len(hunks) != 3 || hunks[1].Line != 2 {
		t.Fatalf("%q %+v %d %d", out, hunks, total, n)
	}
	// plain replacement is literal even with $ in it
	out, _, _, _ = replaceLines([]byte("x\n"), pat, "$1")
	if string(out) != "$1\n" {
		t.Fatalf("literal: %q", out)
	}
}
