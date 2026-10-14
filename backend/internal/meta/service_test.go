package meta

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/listing"
)

type env struct {
	t     *testing.T
	j     *fsjail.Jail
	store *db.Store
	svc   *Service
	files *files.Service
	home  string
	uid   string
	inval []string
}

func newEnv(t *testing.T) *env {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o750); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(t.TempDir(), "data")
	if err := os.MkdirAll(filepath.Join(dataDir, "db"), 0o750); err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(dataDir, "db", "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.MigrateUp(context.Background()); err != nil {
		t.Fatal(err)
	}
	store := db.NewStore(database)
	uid := db.NewID()
	now := db.Now()
	if _, err := store.CreateUser(context.Background(), sqlc.CreateUserParams{ID: uid, Username: "alice", DisplayName: "Alice", Role: "user", Status: "active",
		PasswordHash: "x", QuotaBytes: 1 << 30, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	j, err := fsjail.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	e := &env{t: t, j: j, store: store, home: home, uid: uid}
	e.svc = New(Options{Repo: store, Now: func() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) },
		Invalidate: func(_ string, dir fsjail.VPath) { e.inval = append(e.inval, string(dir)) }})
	e.files = files.New(files.Options{Repo: store})
	return e
}

func (e *env) write(rel, content string) {
	e.t.Helper()
	p := filepath.Join(e.home, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o640); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) list(dir string) []*listing.Item {
	e.t.Helper()
	infos, err := e.j.ReadDir(fsjail.VPath(dir))
	if err != nil {
		e.t.Fatal(err)
	}
	items := make([]*listing.Item, 0, len(infos))
	for i := range infos {
		items = append(items, listing.FromInfo(infos[i]))
	}
	if err := e.svc.Annotate(context.Background(), e.uid, fsjail.VPath(dir), items); err != nil {
		e.t.Fatal(err)
	}
	return items
}

func find(items []*listing.Item, name string) *listing.Item {
	for _, it := range items {
		if it.Name == name {
			return it
		}
	}
	return nil
}

func vp(ps ...string) []fsjail.VPath {
	out := make([]fsjail.VPath, 0, len(ps))
	for _, p := range ps {
		out = append(out, fsjail.VPath(p))
	}
	return out
}

func TestCRUDAndAnnotate(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.write("proyek/a.txt", "a")
	e.write("proyek/b.txt", "b")
	e.write("c.txt", "c")

	if _, err := e.svc.AddFavorites(ctx, e.j, e.uid, vp("/proyek/a.txt", "/proyek")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddPins(ctx, e.j, e.uid, vp("/proyek/b.txt")); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetNote(ctx, e.j, e.uid, "/proyek/a.txt", "catatan penting"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetTags(ctx, e.j, e.uid, vp("/proyek/a.txt", "/c.txt"), "red"); err != nil {
		t.Fatal(err)
	}
	items := e.list("/proyek")
	a, b := find(items, "a.txt"), find(items, "b.txt")
	if !a.Favorite || a.Pinned || a.Note == nil || *a.Note != "catatan penting" || a.Tag == nil || *a.Tag != "red" {
		t.Fatalf("a annotations: %+v", a)
	}
	if b.Favorite || !b.Pinned || b.Note != nil || b.Tag != nil {
		t.Fatalf("b annotations: %+v", b)
	}
	root := e.list("/")
	if p := find(root, "proyek"); !p.Favorite {
		t.Fatalf("folder favorite missing: %+v", p)
	}
	if c := find(root, "c.txt"); c.Tag == nil || *c.Tag != "red" {
		t.Fatalf("c tag: %+v", c)
	}
	// nested rows must not leak into the parent folder scan
	if p := find(root, "proyek"); p.Note != nil || p.Tag != nil {
		t.Fatalf("parent got child's metadata: %+v", p)
	}
	// listing cache invalidation per parent folder
	if !strings.Contains(strings.Join(e.inval, ","), "/proyek") || !strings.Contains(strings.Join(e.inval, ","), "/") {
		t.Fatalf("invalidate calls: %v", e.inval)
	}

	// favorites list, tagged list
	favs, err := e.svc.ListFavorites(ctx, e.j, e.uid, "id")
	if err != nil || len(favs) != 2 || !favs[0].IsDir || favs[0].Path != "/proyek" || favs[1].Path != "/proyek/a.txt" {
		t.Fatalf("favorites: %v %+v", err, favs)
	}
	tagged, err := e.svc.ListTagged(ctx, e.j, e.uid, "red", "id")
	if err != nil || len(tagged) != 2 || tagged[0].Path != "/c.txt" {
		t.Fatalf("tagged: %v %+v", err, tagged)
	}
	if _, err := e.svc.ListTagged(ctx, e.j, e.uid, "pink", "id"); err == nil {
		t.Fatal("bad colour accepted")
	}

	// removals and empty note
	if _, err := e.svc.RemoveFavorites(ctx, e.uid, vp("/proyek")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RemovePins(ctx, e.uid, vp("/proyek/b.txt")); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetNote(ctx, e.j, e.uid, "/proyek/a.txt", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.RemoveTags(ctx, e.uid, vp("/c.txt")); err != nil {
		t.Fatal(err)
	}
	items = e.list("/proyek")
	if a := find(items, "a.txt"); a.Note != nil || !a.Favorite {
		t.Fatalf("after removals: %+v", a)
	}
	if b := find(items, "b.txt"); b.Pinned {
		t.Fatal("pin not removed")
	}
	if c := find(e.list("/"), "c.txt"); c.Tag != nil {
		t.Fatal("tag not removed")
	}
	if p := find(e.list("/"), "proyek"); p.Favorite {
		t.Fatal("favorite not removed")
	}
}

func TestValidation(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.write("a.txt", "a")
	var ve *files.ValidationError
	// 501 runes rejected, 500 accepted (PROMPT AC)
	if err := e.svc.SetNote(ctx, e.j, e.uid, "/a.txt", strings.Repeat("é", 501)); !errors.As(err, &ve) || ve.Reason != "note_too_long" {
		t.Fatalf("501 chars: %v", err)
	}
	if err := e.svc.SetNote(ctx, e.j, e.uid, "/a.txt", strings.Repeat("é", 500)); err != nil {
		t.Fatalf("500 chars: %v", err)
	}
	// missing path → fsjail not-found error (API 404)
	if _, err := e.svc.AddFavorites(ctx, e.j, e.uid, vp("/missing.txt")); err == nil {
		t.Fatal("missing path accepted")
	} else if _, _, ok := fsjail.APIError(err); !ok {
		t.Fatalf("expected fsjail error, got %T %v", err, err)
	}
	// root never carries metadata
	if _, err := e.svc.AddPins(ctx, e.j, e.uid, vp("/")); !errors.As(err, &ve) {
		t.Fatalf("root accepted: %v", err)
	}
	// bad colour
	if _, err := e.svc.SetTags(ctx, e.j, e.uid, vp("/a.txt"), "magenta"); !errors.As(err, &ve) || ve.Reason != "color_invalid" {
		t.Fatalf("colour: %v", err)
	}
	// pin limit per folder
	for i := range MaxPinsPerFolder + 1 {
		e.write("many/f"+strings.Repeat("x", i%3)+string(rune('a'+i%26))+string(rune('A'+i/26))+".txt", "x")
	}
	infos, _ := e.j.ReadDir("/many")
	paths := make([]fsjail.VPath, 0, len(infos))
	for _, in := range infos {
		paths = append(paths, in.Path)
	}
	if _, err := e.svc.AddPins(ctx, e.j, e.uid, paths[:MaxPinsPerFolder]); err != nil {
		t.Fatalf("50 pins: %v", err)
	}
	if _, err := e.svc.AddPins(ctx, e.j, e.uid, paths[MaxPinsPerFolder:MaxPinsPerFolder+1]); !errors.As(err, &ve) || ve.Reason != "pin_limit" {
		t.Fatalf("51st pin: %v", err)
	}
	// re-pinning an existing pin does not count against the limit
	if _, err := e.svc.AddPins(ctx, e.j, e.uid, paths[:1]); err != nil {
		t.Fatalf("re-pin: %v", err)
	}
}

func TestBrokenReferences(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.write("gone.txt", "x")
	e.write("keep.txt", "x")
	if _, err := e.svc.AddFavorites(ctx, e.j, e.uid, vp("/gone.txt", "/keep.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetTags(ctx, e.j, e.uid, vp("/gone.txt"), "blue"); err != nil {
		t.Fatal(err)
	}
	// "deleted over SSH"
	if err := os.Remove(filepath.Join(e.home, "gone.txt")); err != nil {
		t.Fatal(err)
	}
	favs, err := e.svc.ListFavorites(ctx, e.j, e.uid, "id")
	if err != nil {
		t.Fatal(err)
	}
	if len(favs) != 2 {
		t.Fatalf("favorites: %+v", favs)
	}
	gone := find(favs, "gone.txt")
	if gone == nil || !gone.Broken || gone.BrokenReason != "missing" || !gone.Favorite || gone.Tag == nil {
		t.Fatalf("broken entry: %+v", gone)
	}
	if keep := find(favs, "keep.txt"); keep == nil || keep.Broken {
		t.Fatalf("intact entry: %+v", keep)
	}
	tagged, err := e.svc.ListTagged(ctx, e.j, e.uid, "blue", "id")
	if err != nil || len(tagged) != 1 || !tagged[0].Broken {
		t.Fatalf("tagged broken: %v %+v", err, tagged)
	}
	// the broken favorite can still be removed
	if res, err := e.svc.RemoveFavorites(ctx, e.uid, vp("/gone.txt")); err != nil || res.Count != 1 {
		t.Fatalf("remove broken: %v %+v", err, res)
	}
}

// TestReferencesFollowRename proves the rows move with files.UpdateReferences
// for the item itself and for a parent folder (SPEC §5.7, PROMPT AC 1).
func TestReferencesFollowRename(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	e.write("proyek/doc/a.txt", "a")
	if _, err := e.svc.AddFavorites(ctx, e.j, e.uid, vp("/proyek/doc/a.txt", "/proyek/doc")); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.AddPins(ctx, e.j, e.uid, vp("/proyek/doc/a.txt")); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.SetNote(ctx, e.j, e.uid, "/proyek/doc/a.txt", "n"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SetTags(ctx, e.j, e.uid, vp("/proyek/doc/a.txt"), "green"); err != nil {
		t.Fatal(err)
	}
	// rename the file, then the grandparent folder
	if _, err := e.files.Rename(ctx, e.j, e.uid, "/proyek/doc/a.txt", "b.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.files.Rename(ctx, e.j, e.uid, "/proyek", "proyek2"); err != nil {
		t.Fatal(err)
	}
	items := e.list("/proyek2/doc")
	b := find(items, "b.txt")
	if b == nil || !b.Favorite || !b.Pinned || b.Note == nil || b.Tag == nil || *b.Tag != "green" {
		t.Fatalf("metadata after renames: %+v", b)
	}
	if d := find(e.list("/proyek2"), "doc"); d == nil || !d.Favorite {
		t.Fatalf("folder favorite after rename: %+v", d)
	}
	favs, _ := e.svc.ListFavorites(ctx, e.j, e.uid, "id")
	for _, f := range favs {
		if f.Broken {
			t.Fatalf("stale favorite: %+v", f)
		}
	}
	// move into another folder
	e.write("arsip/.keep", "")
	if _, err := e.files.Move(ctx, e.j, e.uid, vp("/proyek2/doc"), "/arsip", files.ConflictPolicy{}); err != nil {
		t.Fatal(err)
	}
	if b := find(e.list("/arsip/doc"), "b.txt"); b == nil || !b.Favorite || !b.Pinned || b.Note == nil {
		t.Fatalf("metadata after move: %+v", b)
	}
}
