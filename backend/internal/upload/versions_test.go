package upload

import (
	"context"
	"net/http"
	"testing"
)

// TestOverwriteTakesTextVersion (Step 16b): an upload with onConflict=overwrite
// snapshots the replaced text file (reason external_overwrite) and reports
// the id in Upload-Version; binary targets get no version.
func TestOverwriteTakesTextVersion(t *testing.T) {
	e := newEnv(t, 0)
	e.mkdir("ow")
	e.write("ow/notes.txt", "old text\n")
	e.write("ow/img.bin", "\x89PNG\x00\x00\x01\x02")
	body := []byte("new text\n")
	loc, res := e.create(int64(len(body)), map[string]string{"filename": "notes.txt", "path": "/ow", "onConflict": "overwrite"})
	if res.status != http.StatusCreated {
		t.Fatalf("create: %d %s", res.status, res.raw)
	}
	last := e.patch(loc, 0, body)
	if last.status != http.StatusNoContent || last.header.Get(HeaderResult) != "overwritten" {
		t.Fatalf("patch: %d %v", last.status, last.header)
	}
	vid := last.header.Get(HeaderVersion)
	if vid == "" {
		t.Fatalf("no Upload-Version header: %v", last.header)
	}
	list, err := e.versions.List(context.Background(), e.user.ID, "/ow/notes.txt")
	if err != nil || len(list) != 1 || list[0].ID != vid || list[0].Reason != "external_overwrite" || list[0].Size != int64(len("old text\n")) {
		t.Fatalf("versions: %+v %v", list, err)
	}
	if e.read("ow/notes.txt") != "new text\n" {
		t.Fatal("file not overwritten")
	}
	// binary target: overwritten without a version
	bin := []byte{0, 1, 2, 3}
	loc, res = e.create(int64(len(bin)), map[string]string{"filename": "img.bin", "path": "/ow", "onConflict": "overwrite"})
	if res.status != http.StatusCreated {
		t.Fatalf("create bin: %d %s", res.status, res.raw)
	}
	last = e.patch(loc, 0, bin)
	if last.status != http.StatusNoContent || last.header.Get(HeaderVersion) != "" {
		t.Fatalf("binary overwrite: %d %v", last.status, last.header)
	}
	if l, _ := e.versions.List(context.Background(), e.user.ID, "/ow/img.bin"); len(l) != 0 {
		t.Fatalf("binary got a version: %+v", l)
	}
}
