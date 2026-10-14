package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"kppbwi.app/filemanager/internal/db"
)

func previewMap(items []PreviewItem) map[string]PreviewItem {
	m := map[string]PreviewItem{}
	for _, it := range items {
		m[it.From] = it
	}
	return m
}

func TestBatchRenamePreviewRules(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	for _, n := range []string{"IMG_001.jpg", "IMG_002.jpg", "notes.TXT", "archive.tar.gz"} {
		e.write(n, n)
	}
	e.mkdir("Folder One")
	paths := vps("/IMG_001.jpg", "/IMG_002.jpg", "/notes.TXT", "/archive.tar.gz", "/Folder One")

	// plain find/replace keeps the extension, literal $ in replacement
	items, err := e.svc.BatchRenamePreview(ctx, e.j, paths, RenameRule{Find: "IMG_", Replace: "foto$"})
	if err != nil {
		t.Fatal(err)
	}
	m := previewMap(items)
	if m["/IMG_001.jpg"].To != "/foto$001.jpg" || m["/notes.TXT"].To != "/notes.TXT" || m["/notes.TXT"].Status != "ok" {
		t.Fatalf("plain: %+v", items)
	}
	// regex with capture group and case-insensitive flag
	items, err = e.svc.BatchRenamePreview(ctx, e.j, paths, RenameRule{Find: `^img_(\d+)$`, Replace: "shot-$1", Regex: true, CaseInsensitive: true})
	if err != nil {
		t.Fatal(err)
	}
	m = previewMap(items)
	if m["/IMG_002.jpg"].To != "/shot-002.jpg" || m["/archive.tar.gz"].To != "/archive.tar.gz" {
		t.Fatalf("regex: %+v", items)
	}
	// case + extension handling
	items, _ = e.svc.BatchRenamePreview(ctx, e.j, paths, RenameRule{Case: "lower"})
	m = previewMap(items)
	if m["/IMG_001.jpg"].To != "/img_001.jpg" || m["/notes.TXT"].To != "/notes.TXT" || m["/Folder One"].To != "/folder one" {
		t.Fatalf("lower keep ext: %+v", items)
	}
	items, _ = e.svc.BatchRenamePreview(ctx, e.j, paths, RenameRule{Case: "lower", ExtMode: "include"})
	m = previewMap(items)
	if m["/notes.TXT"].To != "/notes.txt" {
		t.Fatalf("lower include ext: %+v", items)
	}
	items, _ = e.svc.BatchRenamePreview(ctx, e.j, paths, RenameRule{Case: "title"})
	m = previewMap(items)
	if m["/notes.TXT"].To != "/Notes.TXT" || m["/Folder One"].To != "/Folder One" {
		t.Fatalf("title: %+v", items)
	}
	// numbering with padding and step; order follows the request
	items, err = e.svc.BatchRenamePreview(ctx, e.j, vps("/IMG_002.jpg", "/IMG_001.jpg", "/notes.TXT"), RenameRule{Numbering: &Numbering{Pattern: "liburan-{n}", Start: 10, Step: 5, Pad: 3}})
	if err != nil {
		t.Fatal(err)
	}
	if items[0].To != "/liburan-010.jpg" || items[1].To != "/liburan-015.jpg" || items[2].To != "/liburan-020.TXT" {
		t.Fatalf("numbering: %+v", items)
	}
	items, _ = e.svc.BatchRenamePreview(ctx, e.j, vps("/archive.tar.gz"), RenameRule{Numbering: &Numbering{Pattern: "{name}_{n}.{ext}", Start: 1}})
	if items[0].To != "/archive_1.tar.gz.tar.gz" {
		t.Fatalf("tokens: %+v", items)
	}
	// invalid rules
	var ve *ValidationError
	if _, err := e.svc.BatchRenamePreview(ctx, e.j, paths, RenameRule{Find: "(", Regex: true}); !errors.As(err, &ve) {
		t.Fatalf("bad regex: %v", err)
	}
	if _, err := e.svc.BatchRenamePreview(ctx, e.j, paths, RenameRule{Case: "weird"}); !errors.As(err, &ve) {
		t.Fatalf("bad case: %v", err)
	}
	if _, err := e.svc.BatchRenamePreview(ctx, e.j, paths, RenameRule{Numbering: &Numbering{Pattern: "no-token"}}); !errors.As(err, &ve) {
		t.Fatalf("bad numbering: %v", err)
	}
	// invalid results: empty name, slash
	items, _ = e.svc.BatchRenamePreview(ctx, e.j, vps("/notes.TXT"), RenameRule{Find: "notes", Replace: "", ExtMode: "include", Regex: false})
	if items[0].Status != "ok" || items[0].To != "/.TXT" {
		t.Fatalf("dot name: %+v", items)
	}
	items, _ = e.svc.BatchRenamePreview(ctx, e.j, vps("/notes.TXT"), RenameRule{Find: "notes.TXT", Replace: "", ExtMode: "include"})
	if items[0].Status != "invalid" || items[0].Reason != "empty" {
		t.Fatalf("empty: %+v", items)
	}
	items, _ = e.svc.BatchRenamePreview(ctx, e.j, vps("/notes.TXT"), RenameRule{Find: "notes", Replace: "a/b"})
	if items[0].Status != "invalid" || items[0].Reason != "slash" {
		t.Fatalf("slash: %+v", items)
	}
	items, _ = e.svc.BatchRenamePreview(ctx, e.j, vps("/missing.txt"), RenameRule{Case: "upper"})
	if items[0].Status != "invalid" || items[0].Reason != "not_found" {
		t.Fatalf("missing: %+v", items)
	}
	// too many items
	big := make([]string, MaxBatchRenameItems+1)
	for i := range big {
		big[i] = "/x"
	}
	var te *TooLargeError
	if _, err := e.svc.BatchRenamePreview(ctx, e.j, vps(big...), RenameRule{}); !errors.As(err, &te) {
		t.Fatalf("too many: %v", err)
	}
}

func TestBatchRenameCollisionsAndApply(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	e.write("a.txt", "a")
	e.write("b.txt", "b")
	e.write("c.txt", "c")
	e.write("A.txt", "A")

	// a→c collides with an existing entry that is not renamed
	items, _ := e.svc.BatchRenamePreview(ctx, e.j, vps("/a.txt"), RenameRule{Find: "a", Replace: "c"})
	if items[0].Status != "collision" || items[0].Reason != "exists" {
		t.Fatalf("exists collision: %+v", items)
	}
	// two items producing the same result
	items, _ = e.svc.BatchRenamePreview(ctx, e.j, vps("/a.txt", "/A.txt"), RenameRule{Case: "lower"})
	m := previewMap(items)
	if m["/a.txt"].Status != "collision" || m["/A.txt"].Status != "collision" || m["/A.txt"].Reason != "duplicate_result" {
		t.Fatalf("duplicate result: %+v", items)
	}
	// apply refuses a preview with collisions
	var be *BatchRenameError
	if _, _, err := e.svc.BatchRename(ctx, e.j, e.uid, vps("/a.txt", "/A.txt"), RenameRule{Case: "lower"}); !errors.As(err, &be) || len(be.Items) != 2 {
		t.Fatalf("apply with collision: %v", err)
	}
	if !e.exists("a.txt") || !e.exists("A.txt") {
		t.Fatal("nothing must be renamed")
	}

	// swap chain a→b, b→a (both selected): two-phase rename handles it
	now := db.Now()
	_ = e.store.UpsertFileNote(ctx, e.uid, "/a.txt", "note-a", now)
	_ = e.store.UpsertFileNote(ctx, e.uid, "/b.txt", "note-b", now)
	res, preview, err := e.svc.BatchRename(ctx, e.j, e.uid, vps("/a.txt", "/b.txt"), RenameRule{Find: `^([ab])$`, Replace: "$1", Regex: true, Numbering: &Numbering{Pattern: "{n}", Start: 2, Step: -1}})
	if err != nil {
		t.Fatalf("apply: %v (%+v)", err, preview)
	}
	// numbering 2,1 maps a.txt→2.txt, b.txt→1.txt
	if res.Results[0].To != "/2.txt" || res.Results[1].To != "/1.txt" || e.read("2.txt") != "a" || e.read("1.txt") != "b" {
		t.Fatalf("apply results: %+v", res.Results)
	}
	notes, _ := e.store.ListFileNotes(ctx, e.uid)
	byPath := map[string]string{}
	for _, n := range notes {
		byPath[n.Path] = n.Note
	}
	if byPath["/2.txt"] != "note-a" || byPath["/1.txt"] != "note-b" {
		t.Fatalf("notes after batch rename: %v", byPath)
	}
	// real swap: 1.txt→2.txt and 2.txt→1.txt
	res, _, err = e.svc.BatchRename(ctx, e.j, e.uid, vps("/1.txt", "/2.txt"), RenameRule{Numbering: &Numbering{Pattern: "{n}", Start: 2, Step: -1}})
	if err != nil || e.read("2.txt") != "b" || e.read("1.txt") != "a" {
		t.Fatalf("swap: %v", err)
	}
	// undo reverses the pairs
	if _, err := e.svc.Undo(ctx, e.j, e.uid, res.UndoToken); err != nil || e.read("2.txt") != "a" || e.read("1.txt") != "b" {
		t.Fatalf("undo swap: %v", err)
	}
	if entries, _ := os.ReadDir(e.home); len(entries) != 4 {
		t.Fatalf("no temp files may remain: %d", len(entries))
	}
	// unchanged items are skipped, not errors
	res, _, err = e.svc.BatchRename(ctx, e.j, e.uid, vps("/c.txt"), RenameRule{Case: "lower"})
	if err != nil || len(res.Results) != 0 || res.UndoToken != "" {
		t.Fatalf("unchanged: %v %+v", err, res)
	}
	// rollback: a destination appears between preview and apply
	e.svc.TestHook = func(op string) {
		if op == "batch_rename" {
			_ = os.WriteFile(filepath.Join(e.home, "C.txt"), []byte("sneaky"), 0o640)
		}
	}
	var ce *ConflictError
	if _, _, err := e.svc.BatchRename(ctx, e.j, e.uid, vps("/c.txt"), RenameRule{Case: "upper"}); !errors.As(err, &ce) {
		t.Fatalf("late conflict: %v", err)
	}
	if e.read("c.txt") != "c" || e.read("C.txt") != "sneaky" {
		t.Fatal("rollback failed")
	}
}
