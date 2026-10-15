package versions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
)

// Step 16b tests: FILE_VERSION_LIMIT over 60 saves, versions_bytes vs the
// bytes on disk, restore chain, delete / delete-all, blob cleanup through
// the reference updater, SnapshotIfText and Content.

func dirBytes(t *testing.T, dir string) int64 {
	t.Helper()
	var n int64
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		n += info.Size()
	}
	return n
}

func TestLimitSixtySavesKeepsFifty(t *testing.T) {
	e := newEnv(t, 50)
	ctx := context.Background()
	for i := 1; i <= 60; i++ {
		e.write(t, "doc.txt", "content "+strconv.Itoa(i)+"\n")
		if _, created, err := e.st.Snapshot(ctx, e.j, e.userID, "/doc.txt", ReasonSave); err != nil || !created {
			t.Fatalf("save %d: created=%v err=%v", i, created, err)
		}
	}
	list, err := e.st.List(ctx, e.userID, "/doc.txt")
	if err != nil || len(list) != 50 {
		t.Fatalf("versions after 60 saves: %d %v", len(list), err)
	}
	if list[0].No != 60 || list[49].No != 11 {
		t.Fatalf("kept range %d..%d, want 60..11", list[0].No, list[49].No)
	}
	u, _ := e.quota.Usage(ctx, e.userID)
	disk := dirBytes(t, filepath.Join(e.data, "versions", e.userID))
	if u.Versions != disk || disk == 0 {
		t.Fatalf("versions_bytes %d vs disk %d", u.Versions, disk)
	}
	entries, _ := os.ReadDir(filepath.Join(e.data, "versions", e.userID))
	if len(entries) != 50 {
		t.Fatalf("blobs on disk %d", len(entries))
	}
}

func TestRestoreChainDeleteAndContent(t *testing.T) {
	e := newEnv(t, 10)
	ctx := context.Background()
	e.write(t, "a.txt", "v1\r\n")
	v1, _, err := e.st.Snapshot(ctx, e.j, e.userID, "/a.txt", ReasonSave)
	if err != nil {
		t.Fatal(err)
	}
	e.write(t, "a.txt", "v2\n")
	v2, _, err := e.st.Snapshot(ctx, e.j, e.userID, "/a.txt", ReasonSave)
	if err != nil {
		t.Fatal(err)
	}
	e.write(t, "a.txt", "v3\n")
	before, _ := e.j.Lstat(fsjail.VPath("/a.txt"))
	res, err := e.st.Restore(ctx, e.j, e.userID, v1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(e.home, "a.txt")); string(b) != "v1\r\n" {
		t.Fatalf("restored content %q", b)
	}
	if res.Etag == etagOf(before) || res.Version == nil || res.Version.Reason != ReasonRestore || res.Version.No != 3 {
		t.Fatalf("restore result %+v", res)
	}
	// content of a version decodes like the live file (CRLF kept, read-only)
	c, err := e.st.Content(ctx, e.userID, v1.ID, files.ContentOptions{MaxSize: 1 << 20})
	if err != nil || c.Content != "v1\r\n" || c.EOL != "crlf" || !c.ReadOnly || c.Etag != `"v-`+v1.ID+`"` || c.Path != "/a.txt" {
		t.Fatalf("content %+v %v", c, err)
	}
	// delete one, then all
	if err := e.st.Delete(ctx, e.userID, v2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Get(ctx, e.userID, v2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted version still readable: %v", err)
	}
	if _, err := e.st.Get(ctx, "someone-else", v1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user get: %v", err)
	}
	n, err := e.st.DeleteAll(ctx, e.userID, "/a.txt")
	if err != nil || n != 2 {
		t.Fatalf("delete all: %d %v", n, err)
	}
	u, _ := e.quota.Usage(ctx, e.userID)
	if u.Versions != 0 || dirBytes(t, filepath.Join(e.data, "versions", e.userID)) != 0 {
		t.Fatalf("bytes left after delete all: quota %d", u.Versions)
	}
}

func TestReferenceUpdaterRemovesBlobs(t *testing.T) {
	e := newEnv(t, 10)
	ctx := context.Background()
	if err := os.MkdirAll(filepath.Join(e.home, "d", "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	e.write(t, "d/sub/x.txt", "x1")
	e.write(t, "d/y.txt", "y1")
	e.write(t, "keep.txt", "k1")
	for _, p := range []string{"/d/sub/x.txt", "/d/y.txt", "/keep.txt"} {
		if _, _, err := e.st.Snapshot(ctx, e.j, e.userID, fsjail.VPath(p), ReasonSave); err != nil {
			t.Fatal(err)
		}
	}
	// rename keeps blobs, rewrites paths
	if err := files.UpdateReferences(ctx, e.store, e.userID, "/d", "/e"); err != nil {
		t.Fatal(err)
	}
	moved, _ := e.st.List(ctx, e.userID, "/e/sub/x.txt")
	if len(moved) != 1 {
		t.Fatalf("versions after rename: %+v", moved)
	}
	// delete removes rows + blobs below the prefix and returns the bytes
	if err := files.DeleteReferences(ctx, e.store, e.userID, "/e"); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/e/sub/x.txt", "/e/y.txt"} {
		if l, _ := e.st.List(ctx, e.userID, fsjail.VPath(p)); len(l) != 0 {
			t.Fatalf("%s still has versions", p)
		}
	}
	kept, _ := e.st.List(ctx, e.userID, "/keep.txt")
	if len(kept) != 1 {
		t.Fatal("unrelated versions removed")
	}
	u, _ := e.quota.Usage(ctx, e.userID)
	if u.Versions != 2 || dirBytes(t, filepath.Join(e.data, "versions", e.userID)) != 2 {
		t.Fatalf("bytes after prefix delete: quota %d disk %d", u.Versions, dirBytes(t, filepath.Join(e.data, "versions", e.userID)))
	}
}

func TestSnapshotIfTextSkipsBinaryAndLarge(t *testing.T) {
	e := newEnv(t, 10)
	ctx := context.Background()
	e.write(t, "bin.dat", "\x89PNG\x00\x00\x01")
	e.write(t, "big.txt", strings.Repeat("a", 100))
	e.write(t, "ok.txt", "hello")
	if v, ok, err := e.st.SnapshotIfText(ctx, e.j, e.userID, "/bin.dat", ReasonExternalOverwrite, 1000); err != nil || ok || v != nil {
		t.Fatalf("binary: %v %v %v", v, ok, err)
	}
	if v, ok, err := e.st.SnapshotIfText(ctx, e.j, e.userID, "/big.txt", ReasonExternalOverwrite, 50); err != nil || ok || v != nil {
		t.Fatalf("large: %v %v %v", v, ok, err)
	}
	v, ok, err := e.st.SnapshotIfText(ctx, e.j, e.userID, "/ok.txt", ReasonExternalOverwrite, 50)
	if err != nil || !ok || v == nil || v.Reason != ReasonExternalOverwrite {
		t.Fatalf("text: %+v %v %v", v, ok, err)
	}
}
