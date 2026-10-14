package trash

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/quota"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

type env struct {
	t     *testing.T
	store *db.Store
	j     *fsjail.Jail
	home  string
	data  string
	uid   string
	uid2  string
	clock *clock
	q     *quota.Service
	s     *Store
}

func newEnv(t *testing.T, retentionDays int) *env {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "alice")
	data := filepath.Join(root, "data")
	for _, d := range []string{home, filepath.Join(data, "db")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	database, err := db.Open(filepath.Join(data, "db", "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.MigrateUp(context.Background()); err != nil {
		t.Fatal(err)
	}
	store := db.NewStore(database)
	now := db.Now()
	uid, uid2 := db.NewID(), db.NewID()
	for i, id := range []string{uid, uid2} {
		name := []string{"alice", "bob"}[i]
		if _, err := store.CreateUser(context.Background(), sqlc.CreateUserParams{ID: id, Username: name, DisplayName: name, Role: "user", Status: "active",
			PasswordHash: "x", QuotaBytes: 1 << 30, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	j, err := fsjail.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	c := &clock{t: time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)}
	q := quota.New(store, nil, c.Now)
	q.MinDiskFree = 0
	s := New(data, false, retentionDays, store, nil, c.Now)
	s.Quota = q
	t.Cleanup(s.Close)
	return &env{t: t, store: store, j: j, home: home, data: data, uid: uid, uid2: uid2, clock: c, q: q, s: s}
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

func (e *env) exists(rel string) bool {
	_, err := os.Lstat(filepath.Join(e.home, filepath.FromSlash(rel)))
	return err == nil
}

func (e *env) trashed(id string) bool {
	_, err := os.Lstat(filepath.Join(e.data, "trash", e.uid, id))
	return err == nil
}

func (e *env) del(rel, kind string) sqlc.TrashItem {
	e.t.Helper()
	row, err := e.s.MoveToTrash(context.Background(), e.j, e.uid, fsjail.VPath("/"+rel), kind, "")
	if err != nil {
		e.t.Fatalf("delete %s: %v", rel, err)
	}
	return row
}

func (e *env) usage() quota.Usage {
	u, err := e.q.Usage(context.Background(), e.uid)
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func TestRestoreOriginalParentRecreatedAndPolicies(t *testing.T) {
	e := newEnv(t, 30)
	ctx := context.Background()
	e.write("proyek/doc.txt", "hello")
	e.write("proyek/sub/x.txt", "xx")
	if err := e.q.Apply(ctx, nil, e.uid, 7, 0, 0); err != nil {
		t.Fatal(err)
	}
	row := e.del("proyek/doc.txt", "document")
	if row.Kind != "document" || row.SizeBytes != 5 || row.ExpiresAt == nil || !e.trashed(row.ID) || e.exists("proyek/doc.txt") {
		t.Fatalf("trash row: %+v", row)
	}
	if u := e.usage(); u.Used != 2 || u.Trash != 5 {
		t.Fatalf("quota after delete: %+v", u)
	}
	// parent folder removed meanwhile → recreated on restore
	if err := os.RemoveAll(filepath.Join(e.home, "proyek")); err != nil {
		t.Fatal(err)
	}
	res, err := e.s.Restore(ctx, e.j, e.uid, RestoreRequest{ID: row.ID})
	if err != nil || res.Status != "ok" || res.To != "/proyek/doc.txt" || !e.exists("proyek/doc.txt") {
		t.Fatalf("restore: %+v %v", res, err)
	}
	if u := e.usage(); u.Used != 7 || u.Trash != 0 {
		t.Fatalf("quota after restore: %+v", u)
	}
	if _, err := e.s.Restore(ctx, e.j, e.uid, RestoreRequest{ID: row.ID}); !errors.Is(err, ErrGone) {
		t.Fatalf("second restore: %v", err)
	}
	// conflict: destination occupied
	row2 := e.del("proyek/doc.txt", "document")
	e.write("proyek/doc.txt", "new content")
	_, err = e.s.Restore(ctx, e.j, e.uid, RestoreRequest{ID: row2.ID})
	var ce *ConflictError
	if !errors.As(err, &ce) || ce.Path != "/proyek/doc.txt" || ce.Existing.Size != 11 || ce.Incoming.Size != 5 || ce.Source != row2.ID {
		t.Fatalf("conflict: %v", err)
	}
	res, err = e.s.Restore(ctx, e.j, e.uid, RestoreRequest{ID: row2.ID, Policy: PolicySkip})
	if err != nil || res.Status != "skipped" || !e.trashed(row2.ID) {
		t.Fatalf("skip: %+v %v", res, err)
	}
	res, err = e.s.Restore(ctx, e.j, e.uid, RestoreRequest{ID: row2.ID, Policy: PolicyRename})
	if err != nil || res.Status != "renamed" || res.To != "/proyek/doc (1).txt" || !e.exists("proyek/doc (1).txt") {
		t.Fatalf("rename: %+v %v", res, err)
	}
	// overwrite: the existing item is displaced into the Recycle Bin
	row3 := e.del("proyek/doc (1).txt", "document")
	e.write("proyek/doc (1).txt", "zzz")
	res, err = e.s.Restore(ctx, e.j, e.uid, RestoreRequest{ID: row3.ID, Policy: PolicyOverwrite})
	if err != nil || res.Status != "overwritten" || res.OverwrittenID == "" || !e.trashed(res.OverwrittenID) {
		t.Fatalf("overwrite: %+v %v", res, err)
	}
	if b, _ := os.ReadFile(filepath.Join(e.home, "proyek", "doc (1).txt")); string(b) != "hello" {
		t.Fatalf("overwritten content: %q", b)
	}
	// restore elsewhere with explicit destination
	e.write("proyek/sub/x.txt", "xx")
	row4 := e.del("proyek/sub", "folder")
	if row4.IsDir != 1 || row4.Kind != "folder" || row4.SizeBytes != 2 || row4.ItemCount != 1 {
		t.Fatalf("folder row: %+v", row4)
	}
	res, err = e.s.Restore(ctx, e.j, e.uid, RestoreRequest{ID: row4.ID, Dest: "/lain/sub2"})
	if err != nil || res.To != "/lain/sub2" || !e.exists("lain/sub2/x.txt") {
		t.Fatalf("restore to: %+v %v", res, err)
	}
	// IDOR: another user's id is not found
	if _, err := e.s.Get(ctx, e.uid2, row.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign id: %v", err)
	}
	if _, err := e.s.Restore(ctx, e.j, e.uid2, RestoreRequest{ID: row2.ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign restore: %v", err)
	}
}

func TestPurgeEmptyAndList(t *testing.T) {
	e := newEnv(t, 30)
	ctx := context.Background()
	for i, n := range []string{"b.txt", "a.txt", "c.jpg", "folder/x.txt"} {
		e.write(n, string(make([]byte, i+1)))
	}
	if err := e.q.Apply(ctx, nil, e.uid, 10, 0, 0); err != nil {
		t.Fatal(err)
	}
	purged := 0
	e.s.OnPurge = func(_ context.Context, _ db.Repository, j *fsjail.Jail, uid, p string) error {
		if j != e.j || uid != e.uid || p == "" {
			t.Errorf("purge hook args: %v %s %s", j, uid, p)
		}
		purged++
		return nil
	}
	rb := e.del("b.txt", "document")
	e.clock.Advance(time.Second)
	ra := e.del("a.txt", "document")
	e.clock.Advance(time.Second)
	rc := e.del("c.jpg", "image")
	e.clock.Advance(time.Second)
	rf := e.del("folder", "folder")
	// list: default sort deleted desc
	lr, err := e.s.List(ctx, e.uid, Filter{})
	if err != nil || lr.Total != 4 || lr.TotalBytes != 1+2+3+4 || lr.Items[0].ID != rf.ID || lr.Items[3].ID != rb.ID || lr.NextCursor != "" {
		t.Fatalf("list: %+v %v", lr, err)
	}
	lr, _ = e.s.List(ctx, e.uid, Filter{Sort: "name"})
	if lr.Items[0].Name != "a.txt" || lr.Items[1].Name != "b.txt" || lr.Items[2].Name != "c.jpg" || lr.Items[3].Name != "folder" {
		t.Fatalf("sort name: %+v", lr.Items)
	}
	lr, _ = e.s.List(ctx, e.uid, Filter{Kind: "image"})
	if lr.Total != 1 || lr.Items[0].ID != rc.ID {
		t.Fatalf("kind filter: %+v", lr)
	}
	lr, _ = e.s.List(ctx, e.uid, Filter{Kind: "dir"})
	if lr.Total != 1 || lr.Items[0].ID != rf.ID || !lr.Items[0].IsDir {
		t.Fatalf("dir filter: %+v", lr)
	}
	lr, _ = e.s.List(ctx, e.uid, Filter{Query: ".TXT", Sort: "size", Limit: 1})
	if lr.Total != 2 || len(lr.Items) != 1 || lr.Items[0].ID != rb.ID || lr.NextCursor == "" {
		t.Fatalf("query+page: %+v", lr)
	}
	lr2, _ := e.s.List(ctx, e.uid, Filter{Query: ".TXT", Sort: "size", Limit: 1, Cursor: lr.NextCursor})
	if len(lr2.Items) != 1 || lr2.Items[0].ID != ra.ID || lr2.NextCursor != "" {
		t.Fatalf("page 2: %+v", lr2)
	}
	if lr, _ := e.s.List(ctx, e.uid2, Filter{}); lr.Total != 0 {
		t.Fatalf("other user sees items: %+v", lr)
	}
	// purge two (one foreign id reported gone)
	results, freed, err := e.s.Purge(ctx, e.j, e.uid, []string{rb.ID, "nope", rc.ID, rb.ID})
	if err != nil || freed != 1+3 || len(results) != 3 || results[1].Status != "gone" || results[0].Status != "ok" || e.trashed(rb.ID) || e.trashed(rc.ID) {
		t.Fatalf("purge: %+v %d %v", results, freed, err)
	}
	if purged != 2 {
		t.Fatalf("purge hook calls: %d", purged)
	}
	if u := e.usage(); u.Trash != 2+4 || u.Used != 0 {
		t.Fatalf("quota after purge: %+v", u)
	}
	if _, _, err := e.s.Purge(ctx, e.j, e.uid2, []string{ra.ID}); err != nil {
		t.Fatalf("foreign purge errored: %v", err)
	}
	if !e.trashed(ra.ID) {
		t.Fatal("foreign purge removed another user's item")
	}
	n, freed, err := e.s.Empty(ctx, e.j, e.uid)
	if err != nil || n != 2 || freed != 2+4 || e.trashed(ra.ID) || e.trashed(rf.ID) {
		t.Fatalf("empty: %d %d %v", n, freed, err)
	}
	if u := e.usage(); u.Trash != 0 {
		t.Fatalf("quota after empty: %+v", u)
	}
	if lr, _ := e.s.List(ctx, e.uid, Filter{}); lr.Total != 0 {
		t.Fatalf("list after empty: %+v", lr)
	}
}

func TestAutoPurgeExpired(t *testing.T) {
	e := newEnv(t, 1)
	ctx := context.Background()
	e.write("old.txt", "old")
	e.write("new.txt", "new!")
	jails := 0
	e.s.OpenJail = func(context.Context, string) (*fsjail.Jail, func(), error) { jails++; return e.j, func() {}, nil }
	hook := 0
	e.s.OnPurge = func(_ context.Context, _ db.Repository, j *fsjail.Jail, _, _ string) error {
		if j != e.j {
			t.Error("jail not passed to purge hook")
		}
		hook++
		return nil
	}
	rOld := e.del("old.txt", "document")
	e.clock.Advance(12 * time.Hour)
	rNew := e.del("new.txt", "document")
	n, freed, err := e.s.PurgeExpired(ctx, 100)
	if err != nil || n != 0 || freed != 0 {
		t.Fatalf("nothing expired yet: %d %d %v", n, freed, err)
	}
	e.clock.Advance(13 * time.Hour) // old: 25 h > 1 day; new: 13 h
	n, freed, err = e.s.PurgeExpired(ctx, 1)
	if err != nil || n != 1 || freed != 3 || e.trashed(rOld.ID) || !e.trashed(rNew.ID) || hook != 1 || jails != 1 {
		t.Fatalf("expired purge: n=%d freed=%d err=%v hook=%d jails=%d", n, freed, err, hook, jails)
	}
	row, _ := e.store.GetTrashItem(ctx, e.uid, rOld.ID)
	if row.PurgedAt == nil {
		t.Fatal("purged_at not set")
	}
	// rows older than PurgedRowRetention are pruned
	e.clock.Advance(8 * 24 * time.Hour)
	if _, _, err := e.s.PurgeExpired(ctx, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.GetTrashItem(ctx, e.uid, rOld.ID); !db.IsNotFound(err) {
		t.Fatalf("purged row not pruned: %v", err)
	}
	// retention 0 → no expiry at all
	e0 := newEnv(t, 0)
	e0.write("keep.txt", "k")
	row0 := e0.del("keep.txt", "document")
	if row0.ExpiresAt != nil {
		t.Fatalf("retention 0 must not set expires_at: %v", *row0.ExpiresAt)
	}
	e0.clock.Advance(365 * 24 * time.Hour)
	if n, _, _ := e0.s.PurgeExpired(ctx, 100); n != 0 || !e0.trashed(row0.ID) {
		t.Fatalf("retention 0 purged: %d", n)
	}
}

func TestCursorRoundTrip(t *testing.T) {
	for _, n := range []int{0, 1, 499, 12345} {
		if got := decodeCursor(encodeCursor(n)); got != n {
			t.Fatalf("cursor %d → %d", n, got)
		}
	}
	if decodeCursor("garbage") != 0 || decodeCursor("") != 0 {
		t.Fatal("bad cursors must start from 0")
	}
}
