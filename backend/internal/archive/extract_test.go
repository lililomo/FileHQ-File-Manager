package archive

import (
	"archive/tar"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/tasks"
)

// TestExtractDestinationsAndPolicies: here/new/path destinations, conflict
// policies (fail → ALREADY_EXISTS with the list, skip, rename, overwrite
// with folder merge), partial extraction with entry filters, mtime kept.
func TestExtractDestinationsAndPolicies(t *testing.T) {
	e := newEnv(t)
	z := buildZip(t, []zipEntry{{name: "docs/", content: nil, mode: 0o755 | os.ModeDir}, {name: "docs/a.txt", content: []byte("A")}, {name: "docs/b.txt", content: []byte("B")}, {name: "top.txt", content: []byte("T")}})
	e.write(t, "in/pack.zip", z)
	// new → folder named after the archive
	xr := resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/in/pack.zip", Dest: DestNew}, ""))
	if xr.Dest != "/in/pack" || string(e.read(t, "in/pack/docs/a.txt")) != "A" || xr.Dirs != 1 || xr.Files != 3 {
		t.Fatalf("new: %+v", xr)
	}
	if st, err := os.Stat(filepath.Join(e.home, "in", "pack", "top.txt")); err != nil || !st.ModTime().Equal(time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)) {
		t.Fatalf("mtime not kept: %v %v", st.ModTime(), err)
	}
	// new again: the folder exists → rename policy picks "pack (1)"
	xr = resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/in/pack.zip", Dest: DestNew, Policy: "rename"}, ""))
	if xr.Dest != "/in/pack" || !e.exists("in/pack/docs (1)") || !e.exists("in/pack/top (1).txt") {
		t.Fatalf("new+rename merges with renamed entries: %+v", xr)
	}
	// here → next to the archive; conflicts under fail list the entries
	tk := e.extract(t, ExtractPayload{Archive: "/in/pack.zip", Dest: DestHere}, "")
	resultOf[ExtractResult](t, tk)
	e.write(t, "in/top.txt", []byte("changed"))
	tk = e.extract(t, ExtractPayload{Archive: "/in/pack.zip", Dest: DestHere}, "")
	failedWith(t, tk, CodeAlreadyExists)
	if string(e.read(t, "in/top.txt")) != "changed" {
		t.Fatal("fail policy touched the destination")
	}
	// skip keeps the existing file
	xr = resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/in/pack.zip", Dest: DestHere, Policy: "skip"}, ""))
	if string(e.read(t, "in/top.txt")) != "changed" || xr.SkippedDest != 2 {
		t.Fatalf("skip: %+v", xr)
	}
	// overwrite merges the folder per file and replaces the file
	e.write(t, "in/docs/extra.txt", []byte("keep me"))
	xr = resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/in/pack.zip", Dest: DestHere, Policy: "overwrite"}, ""))
	if string(e.read(t, "in/top.txt")) != "T" || string(e.read(t, "in/docs/extra.txt")) != "keep me" || xr.Overwritten != 3 {
		t.Fatalf("overwrite: %+v", xr)
	}
	// explicit destination that does not exist yet (its parent must)
	xr = resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/in/pack.zip", Dest: "/in/target"}, ""))
	if xr.Dest != "/in/target" || !e.exists("in/target/top.txt") {
		t.Fatalf("explicit dest: %+v", xr)
	}
	failedWith(t, e.extract(t, ExtractPayload{Archive: "/in/pack.zip", Dest: "/missing/parent/x"}, ""), CodeNotFound)
	// partial: only docs/ (prefix) from a tar.gz
	tgz := gzipBytes(t, buildTar(t, []tarEntry{{name: "docs/", typeflag: tar.TypeDir, mode: 0o755}, {name: "docs/a.txt", content: []byte("A")}, {name: "other/b.txt", content: []byte("B")}, {name: "c.txt", content: []byte("C")}}))
	e.write(t, "in/part.tar.gz", tgz)
	xr = resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/in/part.tar.gz", Dest: "/part", Entries: []string{"docs", "c.txt"}}, ""))
	if xr.Files != 2 || e.exists("part/other") || !e.exists("part/docs/a.txt") || !e.exists("part/c.txt") {
		t.Fatalf("partial: %+v", xr)
	}
	// the archive stays where it was and the quota matches the written bytes
	if !e.exists("in/pack.zip") {
		t.Fatal("archive removed")
	}
	u, err := e.quota.Usage(t.Context(), e.user.ID)
	if err != nil || u.Used <= 0 {
		t.Fatalf("quota usage %+v %v", u, err)
	}
}

// TestExtractCancelCleansUp: cancelling mid-way leaves no tmp folder and no
// destination; the reservation is released.
func TestExtractCancelCleansUp(t *testing.T) {
	e := newEnv(t)
	_ = os.MkdirAll(filepath.Join(e.home, "in"), 0o750)
	writeTarGzZeros(t, filepath.Join(e.home, "in", "slow.tar.gz"), 96<<20)
	tk := e.enqueue(t, TaskExtract, ExtractPayload{Archive: "/in/slow.tar.gz", Dest: "/slow", Policy: "fail"}, "")
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		cur, _ := e.engine.Get(t.Context(), e.user.ID, tk.ID)
		if cur.State == tasks.Running && cur.Progress.Done > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := e.engine.Cancel(t.Context(), e.user.ID, tk.ID); err != nil {
		t.Fatal(err)
	}
	final := e.wait(t, tk.ID, 30*time.Second)
	if final.State != tasks.Cancelled {
		t.Fatalf("state %s", final.State)
	}
	if e.exists("slow") || len(e.tmpDirs(t)) != 0 {
		t.Fatal("cancel left files")
	}
	u, _ := e.quota.Usage(t.Context(), e.user.ID)
	if u.Reserved != 0 {
		t.Fatalf("reservation leaked: %+v", u)
	}
	n, err := e.svc.CleanOrphans(t.Context())
	if err != nil || n != 0 {
		t.Fatalf("orphans: %d %v", n, err)
	}
}
