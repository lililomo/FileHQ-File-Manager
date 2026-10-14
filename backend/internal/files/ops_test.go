package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/trash"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type env struct {
	t       *testing.T
	j       *fsjail.Jail
	store   *db.Store
	svc     *Service
	home    string
	dataDir string
	uid     string
	clock   *fakeClock
	trash   *trash.Store
	changes []string
}

type envOpts struct {
	dataDir        string
	crossFS        bool
	syncMaxEntries int
	syncMaxBytes   int64
	retentionDays  int
}

func newEnv(t *testing.T, o envOpts) *env {
	t.Helper()
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o750); err != nil {
		t.Fatal(err)
	}
	dataDir := o.dataDir
	if dataDir == "" {
		dataDir = filepath.Join(t.TempDir(), "data")
	}
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
	clock := &fakeClock{t: time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)}
	e := &env{t: t, j: j, store: store, home: home, dataDir: dataDir, uid: uid, clock: clock}
	e.trash = trash.New(dataDir, o.crossFS, o.retentionDays, store, nil, clock.Now)
	t.Cleanup(e.trash.Close)
	e.svc = New(Options{Repo: store, Trash: e.trash, Now: clock.Now, SyncMaxEntries: o.syncMaxEntries, SyncMaxBytes: o.syncMaxBytes,
		OnChange: func(_ context.Context, _ string, p fsjail.VPath, _ bool) { e.changes = append(e.changes, string(p)) }})
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

func (e *env) mkdir(rel string) {
	e.t.Helper()
	if err := os.MkdirAll(filepath.Join(e.home, filepath.FromSlash(rel)), 0o750); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) exists(rel string) bool {
	_, err := os.Lstat(filepath.Join(e.home, filepath.FromSlash(rel)))
	return err == nil
}

func (e *env) read(rel string) string {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.home, filepath.FromSlash(rel)))
	if err != nil {
		e.t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

func vp(s string) fsjail.VPath { return fsjail.VPath(s) }

func vps(s ...string) []fsjail.VPath {
	out := make([]fsjail.VPath, 0, len(s))
	for _, x := range s {
		out = append(out, fsjail.VPath(x))
	}
	return out
}

func (e *env) activity() []sqlc.ActivityLog {
	e.t.Helper()
	rows, err := e.store.ListActivity(context.Background(), e.uid, 0, 100)
	if err != nil {
		e.t.Fatal(err)
	}
	return rows
}

func TestRenameAndUndo(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.write("a.txt", "hello")
	ctx := context.Background()

	res, err := e.svc.Rename(ctx, e.j, e.uid, vp("/a.txt"), "b.txt")
	if err != nil {
		t.Fatal(err)
	}
	if res.UndoToken == "" || res.Undo == nil || res.Undo.Kind != "rename" || res.Undo.Path != "/b.txt" || res.Undo.NewName != "a.txt" {
		t.Fatalf("undo info: %+v token=%q", res.Undo, res.UndoToken)
	}
	if e.exists("a.txt") || !e.exists("b.txt") {
		t.Fatal("rename did not happen")
	}
	if len(e.changes) != 1 || e.changes[0] != "/a.txt" {
		t.Fatalf("OnChange calls: %v", e.changes)
	}
	// undo restores the old name and is single use
	ures, err := e.svc.Undo(ctx, e.j, e.uid, res.UndoToken)
	if err != nil {
		t.Fatal(err)
	}
	if ures.Undo != nil || ures.UndoToken != "" {
		t.Fatalf("undo must not be undoable: %+v", ures)
	}
	if !e.exists("a.txt") || e.exists("b.txt") {
		t.Fatal("undo did not restore a.txt")
	}
	if _, err := e.svc.Undo(ctx, e.j, e.uid, res.UndoToken); !errors.Is(err, ErrUndoNotFound) {
		t.Fatalf("second undo: %v", err)
	}
	// activity: rename + undo
	acts := e.activity()
	if len(acts) != 2 || acts[0].Action != ActUndo || acts[1].Action != ActRename {
		t.Fatalf("activity: %+v", acts)
	}

	// expiry
	res, err = e.svc.Rename(ctx, e.j, e.uid, vp("/a.txt"), "c.txt")
	if err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(UndoTTL + time.Second)
	if _, err := e.svc.Undo(ctx, e.j, e.uid, res.UndoToken); !errors.Is(err, ErrUndoNotFound) {
		t.Fatalf("expired undo: %v", err)
	}
	// undo conflict: name taken meanwhile
	res, err = e.svc.Rename(ctx, e.j, e.uid, vp("/c.txt"), "d.txt")
	if err != nil {
		t.Fatal(err)
	}
	e.write("c.txt", "other")
	if _, err := e.svc.Undo(ctx, e.j, e.uid, res.UndoToken); !errors.Is(err, ErrUndoConflict) {
		t.Fatalf("undo conflict: %v", err)
	}

	// validation
	if _, err := e.svc.Rename(ctx, e.j, e.uid, vp("/d.txt"), "d.txt"); err == nil {
		t.Fatal("same name must fail")
	}
	if _, err := e.svc.Rename(ctx, e.j, e.uid, vp("/d.txt"), "x/y"); !errors.Is(err, fsjail.ErrInvalidName) {
		t.Fatalf("slash: %v", err)
	}
	if _, err := e.svc.Rename(ctx, e.j, e.uid, vp("/missing"), "y"); !errors.Is(err, fsjail.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	var ce *ConflictError
	if _, err := e.svc.Rename(ctx, e.j, e.uid, vp("/d.txt"), "c.txt"); !errors.As(err, &ce) || ce.Conflicts[0].Existing.Size != 5 {
		t.Fatalf("conflict: %v", err)
	}
	res, err = e.svc.Rename(ctx, e.j, e.uid, vp("/d.txt"), "con")
	if err != nil || len(res.Warnings) != 1 || res.Warnings[0] != fsjail.WarnWindowsIncompatible {
		t.Fatalf("windows warning: %v %v", err, res)
	}
}

func TestMoveValidationAndPolicies(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	e.mkdir("dir/sub")
	e.write("dir/f.txt", "1")
	e.write("other/f.txt", "22")
	e.mkdir("other")

	var ve *ValidationError
	_, err := e.svc.Move(ctx, e.j, e.uid, vps("/dir"), vp("/dir/sub"), ConflictPolicy{})
	if !errors.As(err, &ve) || ve.Reason != "cannot_move_into_self" {
		t.Fatalf("into itself: %v", err)
	}
	if _, err := e.svc.Move(ctx, e.j, e.uid, vps("/dir"), vp("/dir"), ConflictPolicy{}); !errors.As(err, &ve) {
		t.Fatalf("onto itself: %v", err)
	}
	if _, err := e.svc.Move(ctx, e.j, e.uid, vps("/dir/f.txt"), vp("/other/f.txt"), ConflictPolicy{}); !errors.As(err, &ve) || ve.Reason != "not_a_directory" {
		t.Fatalf("to file: %v", err)
	}
	if _, err := e.svc.Move(ctx, e.j, e.uid, vps("/dir/f.txt"), vp("/nope"), ConflictPolicy{}); !errors.Is(err, fsjail.ErrNotFound) {
		t.Fatalf("missing dest: %v", err)
	}
	// conflict with fail policy: nothing moves, details carry both sides
	var ce *ConflictError
	_, err = e.svc.Move(ctx, e.j, e.uid, vps("/dir/f.txt"), vp("/other"), ConflictPolicy{})
	if !errors.As(err, &ce) || len(ce.Conflicts) != 1 || ce.Conflicts[0].Existing.Size != 2 || ce.Conflicts[0].Incoming.Size != 1 || ce.Conflicts[0].Path != "/other/f.txt" {
		t.Fatalf("conflict: %v", err)
	}
	if !e.exists("dir/f.txt") {
		t.Fatal("source must be untouched after conflict")
	}
	// skip
	res, err := e.svc.Move(ctx, e.j, e.uid, vps("/dir/f.txt"), vp("/other"), ConflictPolicy{Default: PolicySkip})
	if err != nil || res.Results[0].Status != "skipped" || res.Undo != nil {
		t.Fatalf("skip: %v %+v", err, res)
	}
	// rename → f (1).txt, undoable
	res, err = e.svc.Move(ctx, e.j, e.uid, vps("/dir/f.txt"), vp("/other"), ConflictPolicy{Default: PolicyRename})
	if err != nil || res.Results[0].Status != "renamed" || res.Results[0].To != "/other/f (1).txt" {
		t.Fatalf("rename policy: %v %+v", err, res)
	}
	if _, err := e.svc.Undo(ctx, e.j, e.uid, res.UndoToken); err != nil || !e.exists("dir/f.txt") || e.exists("other/f (1).txt") {
		t.Fatalf("undo move: %v", err)
	}
	// overwrite file
	res, err = e.svc.Move(ctx, e.j, e.uid, vps("/dir/f.txt"), vp("/other"), ConflictPolicy{Default: PolicyOverwrite})
	if err != nil || res.Results[0].Status != "overwritten" || e.read("other/f.txt") != "1" || res.Undo != nil {
		t.Fatalf("overwrite: %v %+v", err, res)
	}
	// per-item policy beats default
	e.write("dir/g.txt", "g")
	e.write("other/g.txt", "G")
	res, err = e.svc.Move(ctx, e.j, e.uid, vps("/dir/g.txt"), vp("/other"), ConflictPolicy{Default: PolicyFail, PerItem: map[string]Policy{"/dir/g.txt": PolicySkip}})
	if err != nil || res.Results[0].Status != "skipped" {
		t.Fatalf("per item: %v %+v", err, res)
	}
	// type mismatch
	e.mkdir("other/g.txt.d")
	e.write("dir/g.txt.d", "file")
	if _, err := e.svc.Move(ctx, e.j, e.uid, vps("/dir/g.txt.d"), vp("/other"), ConflictPolicy{Default: PolicyOverwrite}); !errors.As(err, &ve) || ve.Reason != "type_mismatch" {
		t.Fatalf("type mismatch: %v", err)
	}
	// already in place → skipped
	res, err = e.svc.Move(ctx, e.j, e.uid, vps("/other/f.txt"), vp("/other"), ConflictPolicy{})
	if err != nil || res.Results[0].Status != "skipped" {
		t.Fatalf("same place: %v %+v", err, res)
	}
}

func TestMoveFolderMerge(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	e.write("src/a.txt", "new a")
	e.write("src/sub/b.txt", "new b")
	e.write("src/only.txt", "only")
	e.write("dst/src/a.txt", "old a")
	e.write("dst/src/sub/keep.txt", "keep")
	e.write("dst/src/sub/b.txt", "old b")
	if err := e.store.UpsertFavorite(ctx, e.uid, "/dst/src/sub", db.Now()); err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpsertFileNote(ctx, e.uid, "/src/sub/b.txt", "note", db.Now()); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.Move(ctx, e.j, e.uid, vps("/src"), vp("/dst"), ConflictPolicy{Default: PolicyOverwrite})
	if err != nil || res.Results[0].Status != "merged" {
		t.Fatalf("merge: %v %+v", err, res)
	}
	if e.exists("src") {
		t.Fatal("merged source must be gone")
	}
	for rel, want := range map[string]string{"dst/src/a.txt": "new a", "dst/src/sub/b.txt": "new b", "dst/src/sub/keep.txt": "keep", "dst/src/only.txt": "only"} {
		if got := e.read(rel); got != want {
			t.Fatalf("%s = %q want %q", rel, got, want)
		}
	}
	favs, _ := e.store.ListFavorites(ctx, e.uid)
	if len(favs) != 1 || favs[0].Path != "/dst/src/sub" {
		t.Fatalf("destination favorite must survive a merge: %+v", favs)
	}
	notes, _ := e.store.ListFileNotes(ctx, e.uid)
	if len(notes) != 1 || notes[0].Path != "/dst/src/sub/b.txt" {
		t.Fatalf("note must follow the merged file: %+v", notes)
	}
	// skip policy keeps the source folder with the skipped file
	e.write("src2/x.txt", "x")
	e.write("dst/src2/x.txt", "X")
	if _, err := e.svc.Move(ctx, e.j, e.uid, vps("/src2"), vp("/dst"), ConflictPolicy{Default: PolicySkip}); err != nil {
		t.Fatal(err)
	}
	if !e.exists("src2/x.txt") {
		t.Fatal("skipped file must stay in source")
	}
}

func TestCopyPoliciesUndoAndCleanup(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	e.write("a.txt", "A")
	e.write("dir/b.txt", "B")
	e.write("dir/c/d.txt", "D")
	e.mkdir("dest")
	e.write("dest/a.txt", "old")

	var ce *ConflictError
	if _, err := e.svc.Copy(ctx, e.j, e.uid, vps("/a.txt"), vp("/dest"), ConflictPolicy{}); !errors.As(err, &ce) {
		t.Fatalf("copy conflict: %v", err)
	}
	res, err := e.svc.Copy(ctx, e.j, e.uid, vps("/a.txt", "/dir"), vp("/dest"), ConflictPolicy{Default: PolicyRename})
	if err != nil {
		t.Fatal(err)
	}
	if res.Results[0].To != "/dest/a (1).txt" || res.Results[1].To != "/dest/dir" || e.read("dest/dir/c/d.txt") != "D" {
		t.Fatalf("copy results: %+v", res.Results)
	}
	if e.read("dest/a.txt") != "old" || e.read("a.txt") != "A" {
		t.Fatal("sources/old destination changed")
	}
	// undo removes the copies (to trash)
	if _, err := e.svc.Undo(ctx, e.j, e.uid, res.UndoToken); err != nil {
		t.Fatal(err)
	}
	if e.exists("dest/a (1).txt") || e.exists("dest/dir") {
		t.Fatal("undo copy must remove the copies")
	}
	rows, _ := e.store.ListTrashItems(ctx, e.uid)
	if len(rows) != 2 {
		t.Fatalf("copies must be in the trash: %+v", rows)
	}
	// overwrite
	res, err = e.svc.Copy(ctx, e.j, e.uid, vps("/a.txt"), vp("/dest"), ConflictPolicy{Default: PolicyOverwrite})
	if err != nil || res.Results[0].Status != "overwritten" || e.read("dest/a.txt") != "A" || res.Undo != nil {
		t.Fatalf("overwrite copy: %v %+v", err, res)
	}
	// copy onto itself picks a fresh name (duplicate semantics)
	res, err = e.svc.Copy(ctx, e.j, e.uid, vps("/a.txt"), vp("/"), ConflictPolicy{})
	if err != nil || res.Results[0].To != "/a (1).txt" {
		t.Fatalf("copy in place: %v %+v", err, res)
	}
	// folder merge copy with skip keeps destination files
	e.write("dest/dir/b.txt", "KEEP")
	res, err = e.svc.Copy(ctx, e.j, e.uid, vps("/dir"), vp("/dest"), ConflictPolicy{Default: PolicySkip})
	if err != nil || res.Results[0].Status != "merged" || e.read("dest/dir/b.txt") != "KEEP" || e.read("dest/dir/c/d.txt") != "D" {
		t.Fatalf("merge copy: %v %+v", err, res)
	}
	// cancelled context: no partial destination remains
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := e.svc.Copy(cctx, e.j, e.uid, vps("/dir"), vp("/dest2"), ConflictPolicy{}); err == nil {
		t.Fatal("expected error")
	}
	e.mkdir("dest2")
	if _, err := e.svc.Copy(cctx, e.j, e.uid, vps("/dir"), vp("/dest2"), ConflictPolicy{}); err == nil {
		t.Fatal("cancelled copy must fail")
	}
	if e.exists("dest2/dir") {
		t.Fatal("partial copy must be cleaned up")
	}
}

func TestSyncLimits(t *testing.T) {
	e := newEnv(t, envOpts{syncMaxEntries: 3})
	ctx := context.Background()
	for _, n := range []string{"a", "b", "c", "d"} {
		e.write("big/"+n+".txt", n)
	}
	e.mkdir("dest")
	var te *TooLargeError
	if _, err := e.svc.Copy(ctx, e.j, e.uid, vps("/big"), vp("/dest"), ConflictPolicy{}); !errors.As(err, &te) || te.Reason != "entries" {
		t.Fatalf("too large: %v", err)
	}
	if e.exists("dest/big") {
		t.Fatal("nothing must be copied")
	}
	// moves are renames: no limit
	if _, err := e.svc.Move(ctx, e.j, e.uid, vps("/big"), vp("/dest"), ConflictPolicy{}); err != nil {
		t.Fatalf("move: %v", err)
	}
	// recursive chmod is bounded too
	if _, err := e.svc.Chmod(ctx, e.j, e.uid, ChmodRequest{Paths: vps("/dest/big"), Mode: 0o600, Recursive: true}); !errors.As(err, &te) {
		t.Fatalf("chmod too large: %v", err)
	}
	e2 := newEnv(t, envOpts{syncMaxBytes: 4})
	e2.write("f.bin", "12345")
	e2.mkdir("d")
	if _, err := e2.svc.Copy(ctx, e2.j, e2.uid, vps("/f.bin"), vp("/d"), ConflictPolicy{}); !errors.As(err, &te) || te.Reason != "bytes" {
		t.Fatalf("bytes: %v", err)
	}
}

func TestDuplicateSymlinkChmod(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	e.write("photo.tar.gz", "x")
	e.mkdir("folder")

	res, err := e.svc.Duplicate(ctx, e.j, e.uid, vp("/photo.tar.gz"))
	if err != nil || res.Results[0].To != "/photo (1).tar.gz" {
		t.Fatalf("duplicate: %v %+v", err, res)
	}
	res, err = e.svc.Duplicate(ctx, e.j, e.uid, vp("/photo.tar.gz"))
	if err != nil || res.Results[0].To != "/photo (2).tar.gz" {
		t.Fatalf("duplicate 2: %v %+v", err, res)
	}
	res, err = e.svc.Duplicate(ctx, e.j, e.uid, vp("/folder"))
	if err != nil || res.Results[0].To != "/folder (1)" || !res.Results[0].IsDir {
		t.Fatalf("duplicate folder: %v %+v", err, res)
	}
	if _, err := e.svc.Undo(ctx, e.j, e.uid, res.UndoToken); err != nil || e.exists("folder (1)") {
		t.Fatalf("undo duplicate: %v", err)
	}

	// symlink
	res, err = e.svc.Symlink(ctx, e.j, e.uid, vp("/folder/link"), "../photo.tar.gz")
	if err != nil {
		t.Fatal(err)
	}
	if target, _ := os.Readlink(filepath.Join(e.home, "folder/link")); target != "../photo.tar.gz" {
		t.Fatalf("link target %q", target)
	}
	if _, err := e.svc.Symlink(ctx, e.j, e.uid, vp("/folder/esc"), "../../etc"); !errors.Is(err, fsjail.ErrEscape) {
		t.Fatalf("escape: %v", err)
	}
	var ve *ValidationError
	if _, err := e.svc.Symlink(ctx, e.j, e.uid, vp("/folder/abs"), "/etc/passwd"); !errors.As(err, &ve) {
		t.Fatalf("absolute: %v", err)
	}
	if _, err := e.svc.Undo(ctx, e.j, e.uid, res.UndoToken); err != nil || e.exists("folder/link") {
		t.Fatalf("undo symlink: %v", err)
	}

	// chmod: rwx only, setuid dropped, folders get x where r, recursive
	e.write("folder/f.sh", "#!/bin/sh")
	m, ok := ParseMode("4755")
	if !ok || m != 0o755 {
		t.Fatalf("ParseMode: %o %v", m, ok)
	}
	if _, ok := ParseMode("0999"); ok {
		t.Fatal("invalid octal accepted")
	}
	res, err = e.svc.Chmod(ctx, e.j, e.uid, ChmodRequest{Paths: vps("/folder"), Mode: 0o640, Recursive: true})
	if err != nil || res.Undo != nil {
		t.Fatalf("chmod: %v %+v", err, res)
	}
	if st, _ := os.Stat(filepath.Join(e.home, "folder")); st.Mode().Perm() != 0o750 {
		t.Fatalf("dir mode %o", st.Mode().Perm())
	}
	if st, _ := os.Stat(filepath.Join(e.home, "folder/f.sh")); st.Mode().Perm() != 0o640 {
		t.Fatalf("file mode %o", st.Mode().Perm())
	}
	dm := os.FileMode(0o700)
	if _, err := e.svc.Chmod(ctx, e.j, e.uid, ChmodRequest{Paths: vps("/folder"), Mode: 0o600, Recursive: true, DirMode: &dm}); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(filepath.Join(e.home, "folder")); st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode %o", st.Mode().Perm())
	}
	if _, err := e.svc.Chmod(ctx, e.j, e.uid, ChmodRequest{Paths: vps("/"), Mode: 0o600}); !errors.As(err, &ve) {
		t.Fatalf("chmod root: %v", err)
	}
}

func TestDeleteToTrashAndUndo(t *testing.T) {
	e := newEnv(t, envOpts{retentionDays: 30})
	ctx := context.Background()
	e.write("doc.pdf", "pdf")
	e.write("proj/a.txt", "a")
	e.write("proj/b/c.txt", "c")
	if err := e.store.UpsertFileTag(ctx, e.uid, "/proj/a.txt", "red", db.Now()); err != nil {
		t.Fatal(err)
	}

	res, err := e.svc.Delete(ctx, e.j, e.uid, vps("/doc.pdf", "/proj", "/proj/b"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 2 || res.BatchID == "" || res.Undo == nil || res.Undo.Kind != "restore" || len(res.Undo.Trash) != 2 {
		t.Fatalf("delete result: %+v", res)
	}
	if e.exists("doc.pdf") || e.exists("proj") {
		t.Fatal("items must be gone")
	}
	rows, err := e.store.ListTrashByBatch(ctx, e.uid, res.BatchID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("trash rows: %v %+v", err, rows)
	}
	for _, row := range rows {
		phys := filepath.Join(e.dataDir, "trash", e.uid, row.ID)
		if _, err := os.Lstat(phys); err != nil {
			t.Fatalf("physical trash item missing: %v", err)
		}
		if row.ExpiresAt == nil || !strings.HasPrefix(*row.ExpiresAt, "2026-11-13") {
			t.Fatalf("expires_at: %v", row.ExpiresAt)
		}
		switch row.OriginalPath {
		case "/doc.pdf":
			if row.Kind != "document" || row.IsDir != 0 || row.SizeBytes != 3 || row.ItemCount != 0 {
				t.Fatalf("doc row: %+v", row)
			}
		case "/proj":
			if row.Kind != "folder" || row.IsDir != 1 || row.SizeBytes != 2 || row.ItemCount != 3 {
				t.Fatalf("proj row: %+v", row)
			}
		default:
			t.Fatalf("unexpected row %+v", row)
		}
	}
	// metadata rows stay for restore
	tags, _ := e.store.ListFileTags(ctx, e.uid)
	if len(tags) != 1 {
		t.Fatalf("tags must survive delete: %+v", tags)
	}
	// undo restores both
	if _, err := e.svc.Undo(ctx, e.j, e.uid, res.UndoToken); err != nil {
		t.Fatal(err)
	}
	if e.read("doc.pdf") != "pdf" || e.read("proj/b/c.txt") != "c" {
		t.Fatal("restore failed")
	}
	rows, _ = e.store.ListTrashByBatch(ctx, e.uid, res.BatchID)
	if len(rows) != 0 {
		t.Fatalf("restored rows must leave the batch listing: %+v", rows)
	}
	// restore conflict: path taken again
	res, err = e.svc.Delete(ctx, e.j, e.uid, vps("/doc.pdf"))
	if err != nil {
		t.Fatal(err)
	}
	e.write("doc.pdf", "new")
	if _, err := e.svc.Undo(ctx, e.j, e.uid, res.UndoToken); !errors.Is(err, ErrUndoConflict) {
		t.Fatalf("restore conflict: %v", err)
	}
	// root and missing
	var ve *ValidationError
	if _, err := e.svc.Delete(ctx, e.j, e.uid, vps("/")); !errors.As(err, &ve) {
		t.Fatalf("root: %v", err)
	}
	if _, err := e.svc.Delete(ctx, e.j, e.uid, vps("/nope")); !errors.Is(err, fsjail.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

// TestDeleteCrossFilesystem puts DATA_DIR on tmpfs (/dev/shm) so the move to
// the trash crosses a filesystem boundary and falls back to copy+delete.
func TestDeleteCrossFilesystem(t *testing.T) {
	shm, err := os.MkdirTemp("/dev/shm", "fm-trash-")
	if err != nil {
		t.Skip("/dev/shm not available:", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(shm) })
	if sameDevice(t, shm, t.TempDir()) {
		t.Skip("/dev/shm is on the same filesystem as TMPDIR")
	}
	e := newEnv(t, envOpts{dataDir: shm, crossFS: true})
	ctx := context.Background()
	e.write("tree/x/y.txt", "yy")
	e.write("tree/z.txt", "z")
	res, err := e.svc.Delete(ctx, e.j, e.uid, vps("/tree"))
	if err != nil {
		t.Fatal(err)
	}
	id := res.Results[0].TrashID
	if e.exists("tree") {
		t.Fatal("source must be gone")
	}
	if b, err := os.ReadFile(filepath.Join(shm, "trash", e.uid, id, "x", "y.txt")); err != nil || string(b) != "yy" {
		t.Fatalf("copied content: %v %q", err, b)
	}
	if _, err := e.svc.Undo(ctx, e.j, e.uid, res.UndoToken); err != nil || e.read("tree/z.txt") != "z" {
		t.Fatalf("restore across filesystems: %v", err)
	}
}

func sameDevice(t *testing.T, a, b string) bool {
	t.Helper()
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	return devOf(sa) == devOf(sb)
}

func TestLocksSerializeConcurrentMoves(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	e.write("f.txt", "f")
	e.mkdir("d1")
	e.mkdir("d2")
	started := make(chan struct{})
	proceed := make(chan struct{})
	var once sync.Once
	e.svc.TestHook = func(op string) {
		once.Do(func() {
			close(started)
			<-proceed
		})
	}
	var first *Result
	var firstErr error
	done := make(chan struct{})
	go func() {
		first, firstErr = e.svc.Move(ctx, e.j, e.uid, vps("/f.txt"), vp("/d1"), ConflictPolicy{})
		close(done)
	}()
	<-started
	_, err := e.svc.Move(ctx, e.j, e.uid, vps("/f.txt"), vp("/d2"), ConflictPolicy{})
	if !errors.Is(err, fsjail.ErrLocked) {
		t.Fatalf("second move: %v", err)
	}
	// an ancestor lock also conflicts
	if _, err := e.svc.Delete(ctx, e.j, e.uid, vps("/d1")); !errors.Is(err, fsjail.ErrLocked) {
		t.Fatalf("ancestor: %v", err)
	}
	close(proceed)
	<-done
	if firstErr != nil || first.Results[0].Status != "ok" || !e.exists("d1/f.txt") {
		t.Fatalf("first move: %v %+v", firstErr, first)
	}
}

func TestTrashKindAndHelpers(t *testing.T) {
	for name, want := range map[string]string{"a.jpg": "image", "a.mp4": "video", "a.mp3": "audio", "a.zip": "archive", "a.pdf": "document", "a.txt": "document", "a.go": "file", "a.bin": "other"} {
		if got := TrashKind(name, false); got != want {
			t.Errorf("TrashKind(%s)=%s want %s", name, got, want)
		}
	}
	if TrashKind("x", true) != "folder" {
		t.Error("folder kind")
	}
	if s, x := SplitExt("a.tar.gz", false); s != "a" || x != ".tar.gz" {
		t.Errorf("SplitExt: %q %q", s, x)
	}
	if s, x := SplitExt(".bashrc", false); s != ".bashrc" || x != "" {
		t.Errorf("SplitExt dotfile: %q %q", s, x)
	}
	if s, x := SplitExt("dir.v2", true); s != "dir.v2" || x != "" {
		t.Errorf("SplitExt dir: %q %q", s, x)
	}
	got := dropDescendants(vps("/a/b", "/a", "/c", "/a/b/c"))
	if len(got) != 2 || got[0] != "/a" || got[1] != "/c" {
		t.Errorf("dropDescendants: %v", got)
	}
}
