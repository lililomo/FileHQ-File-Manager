package files

import (
	"context"
	"testing"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

type recordingUpdater struct {
	rewrites []string
	deletes  []string
}

func (r *recordingUpdater) Rewrite(_ context.Context, _ db.Repository, _ string, oldPath, newPath string) error {
	r.rewrites = append(r.rewrites, oldPath+"->"+newPath)
	return nil
}

func (r *recordingUpdater) Delete(_ context.Context, _ db.Repository, _ string, path string) error {
	r.deletes = append(r.deletes, path)
	return nil
}

func TestUpdateReferencesRewritesAllTables(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	now := db.Now()
	seed := func(p string) {
		t.Helper()
		if err := e.store.UpsertFavorite(ctx, e.uid, p, now); err != nil {
			t.Fatal(err)
		}
		if err := e.store.UpsertPin(ctx, e.uid, p, now); err != nil {
			t.Fatal(err)
		}
		if err := e.store.UpsertFileNote(ctx, e.uid, p, "n", now); err != nil {
			t.Fatal(err)
		}
		if err := e.store.UpsertFileTag(ctx, e.uid, p, "blue", now); err != nil {
			t.Fatal(err)
		}
		if err := e.store.InsertFileVersion(ctx, sqlc.InsertFileVersionParams{ID: db.NewID(), UserID: e.uid, Path: p, VersionNo: 1, Size: 1, Sha256: "x", BlobRel: "b", Reason: "save", CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	seed("/proyek")
	seed("/proyek/a.txt")
	seed("/proyek/sub/b.txt")
	seed("/proyek2/stale.txt") // destination rows must be cleared
	seed("/proyekX/other.txt") // sibling with the same prefix string must stay
	seed("/lain.txt")

	rec := &recordingUpdater{}
	RegisterReferenceUpdater("test-shares", rec)
	t.Cleanup(func() {
		refMu.Lock()
		delete(refRegs, "test-shares")
		refMu.Unlock()
	})

	if err := e.store.WithTx(ctx, func(tx db.Repository) error {
		return UpdateReferences(ctx, tx, e.uid, "/proyek", "/proyek2")
	}); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"/proyek2": true, "/proyek2/a.txt": true, "/proyek2/sub/b.txt": true, "/proyekX/other.txt": true, "/lain.txt": true}
	check := func(label string, paths []string) {
		t.Helper()
		if len(paths) != len(want) {
			t.Fatalf("%s: %v", label, paths)
		}
		for _, p := range paths {
			if !want[p] {
				t.Fatalf("%s: unexpected %s in %v", label, p, paths)
			}
		}
	}
	favs, _ := e.store.ListFavorites(ctx, e.uid)
	pins, _ := e.store.ListPins(ctx, e.uid)
	notes, _ := e.store.ListFileNotes(ctx, e.uid)
	tags, _ := e.store.ListFileTags(ctx, e.uid)
	vers, _ := e.store.ListFileVersions(ctx, e.uid)
	var fp, pp, np, tp, vp []string
	for _, r := range favs {
		fp = append(fp, r.Path)
	}
	for _, r := range pins {
		pp = append(pp, r.Path)
	}
	for _, r := range notes {
		np = append(np, r.Path)
	}
	for _, r := range tags {
		tp = append(tp, r.Path)
	}
	for _, r := range vers {
		vp = append(vp, r.Path)
	}
	check("favorites", fp)
	check("pins", pp)
	check("notes", np)
	check("tags", tp)
	check("versions", vp)
	if len(rec.deletes) != 1 || rec.deletes[0] != "/proyek2" || len(rec.rewrites) != 1 || rec.rewrites[0] != "/proyek->/proyek2" {
		t.Fatalf("custom updater calls: %v %v", rec.deletes, rec.rewrites)
	}

	// DeleteReferences removes the subtree only
	if err := e.store.WithTx(ctx, func(tx db.Repository) error {
		return DeleteReferences(ctx, tx, e.uid, "/proyek2")
	}); err != nil {
		t.Fatal(err)
	}
	favs, _ = e.store.ListFavorites(ctx, e.uid)
	if len(favs) != 2 {
		t.Fatalf("after delete: %+v", favs)
	}
	// no-op when unchanged
	if err := UpdateReferences(ctx, e.store, e.uid, "/x", "/x"); err != nil {
		t.Fatal(err)
	}
}

func TestRenameFolderMovesReferencesInOneTransaction(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	e.write("proyek/a.txt", "a")
	e.mkdir("proyek/sub")
	now := db.Now()
	_ = e.store.UpsertFavorite(ctx, e.uid, "/proyek/sub", now)
	_ = e.store.UpsertFileNote(ctx, e.uid, "/proyek/a.txt", "catatan", now)
	_ = e.store.UpsertFileNote(ctx, e.uid, "/proyek", "root note", now)

	if _, err := e.svc.Rename(ctx, e.j, e.uid, vp("/proyek"), "proyek2"); err != nil {
		t.Fatal(err)
	}
	favs, _ := e.store.ListFavorites(ctx, e.uid)
	notes, _ := e.store.ListFileNotes(ctx, e.uid)
	if len(favs) != 1 || favs[0].Path != "/proyek2/sub" {
		t.Fatalf("favorites: %+v", favs)
	}
	if len(notes) != 2 || notes[0].Path != "/proyek2" || notes[1].Path != "/proyek2/a.txt" {
		t.Fatalf("notes: %+v", notes)
	}
	// move keeps them in step as well
	e.mkdir("arsip")
	if _, err := e.svc.Move(ctx, e.j, e.uid, vps("/proyek2"), vp("/arsip"), ConflictPolicy{}); err != nil {
		t.Fatal(err)
	}
	notes, _ = e.store.ListFileNotes(ctx, e.uid)
	if len(notes) != 2 || notes[0].Path != "/arsip/proyek2" || notes[1].Path != "/arsip/proyek2/a.txt" {
		t.Fatalf("notes after move: %+v", notes)
	}
}
