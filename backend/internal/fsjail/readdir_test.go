package fsjail

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
)

// TestReadDirParallelMatchesSequential covers the Step 21 parallel statx path
// (≥ 2 × readDirChunk entries and GOMAXPROCS > 1) against the sequential one.
func TestReadDirParallelMatchesSequential(t *testing.T) {
	dir := t.TempDir()
	const n = 3*readDirChunk + 17
	for i := range n {
		if err := os.WriteFile(filepath.Join(dir, "f"+strconv.Itoa(i)), make([]byte, i%5), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("f1", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../etc/passwd", filepath.Join(dir, "evil")); err != nil {
		t.Fatal(err)
	}
	j, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()
	read := func(procs int) map[string]Info {
		prev := runtime.GOMAXPROCS(procs)
		defer runtime.GOMAXPROCS(prev)
		infos, err := j.ReadDir(Root)
		if err != nil {
			t.Fatal(err)
		}
		out := make(map[string]Info, len(infos))
		for _, in := range infos {
			out[in.Name] = in
		}
		return out
	}
	seq, par := read(1), read(4)
	if len(seq) != n+3 || len(par) != len(seq) {
		t.Fatalf("entries: sequential %d, parallel %d, want %d", len(seq), len(par), n+3)
	}
	for name, a := range seq {
		b, ok := par[name]
		if !ok || a.Path != b.Path || a.Size != b.Size || a.IsDir != b.IsDir || a.IsSymlink != b.IsSymlink ||
			a.SymlinkTarget != b.SymlinkTarget || a.SymlinkReason != b.SymlinkReason || a.Ino != b.Ino {
			t.Fatalf("%s differs: %+v vs %+v", name, a, b)
		}
	}
	if l := par["link"]; !l.IsSymlink || l.SymlinkBroken || l.Path != "/link" {
		t.Fatalf("link: %+v", l)
	}
	if e := par["evil"]; !e.SymlinkBroken || e.SymlinkReason != "escape" {
		t.Fatalf("evil: %+v", e)
	}
	if s := par["sub"]; !s.IsDir || s.Path != "/sub" {
		t.Fatalf("sub: %+v", s)
	}
	// nested folder paths keep the parent prefix
	for i := range 2*readDirChunk + 1 {
		if err := os.WriteFile(filepath.Join(dir, "sub", "g"+strconv.Itoa(i)), nil, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	prev := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(prev)
	sub, err := j.ReadDir("/sub")
	if err != nil || len(sub) != 2*readDirChunk+1 || sub[0].Path != VPath("/sub/"+sub[0].Name) {
		t.Fatalf("sub: %d entries, %v", len(sub), err)
	}
}
