package fsjail

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCountEntries(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.txt", "b.txt", ".hidden"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	j, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()

	n, trunc, err := j.CountEntries(Root, 0)
	if err != nil || trunc || n != 4 {
		t.Fatalf("root: n=%d trunc=%v err=%v", n, trunc, err)
	}
	n, trunc, err = j.CountEntries(Root, 2)
	if err != nil || !trunc || n != 2 {
		t.Fatalf("truncated: n=%d trunc=%v err=%v", n, trunc, err)
	}
	n, trunc, err = j.CountEntries("/sub", 10)
	if err != nil || trunc || n != 0 {
		t.Fatalf("empty: n=%d trunc=%v err=%v", n, trunc, err)
	}
	if _, _, err = j.CountEntries("/a.txt", 0); !errors.Is(err, ErrNotDir) && !errors.Is(err, ErrNotFound) {
		t.Fatalf("file: want ErrNotDir, got %v", err)
	}
	if _, _, err = j.CountEntries("/missing", 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: want ErrNotFound, got %v", err)
	}
	if _, _, err = j.CountEntries("/a/../b", 0); !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("unclean: want ErrInvalidPath, got %v", err)
	}
}
