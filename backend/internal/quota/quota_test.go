package quota

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/fsjail"
)

type env struct {
	store *db.Store
	j     *fsjail.Jail
	home  string
	data  string
	uid   string
	svc   *Service
}

func newEnv(t *testing.T, limit int64) *env {
	t.Helper()
	root := t.TempDir()
	home := filepath.Join(root, "storage", "alice")
	data := filepath.Join(root, "storage", ".system")
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
	uid := db.NewID()
	now := db.Now()
	if _, err := store.CreateUser(context.Background(), sqlc.CreateUserParams{ID: uid, Username: "alice", DisplayName: "Alice", Role: "user", Status: "active",
		PasswordHash: "x", QuotaBytes: limit, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetHomeProvisioned(context.Background(), sqlc.SetHomeProvisionedParams{HomeProvisionedAt: &now, ID: uid}); err != nil {
		t.Fatal(err)
	}
	j, err := fsjail.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	svc := New(store, nil, func() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) })
	svc.MinDiskFree = 0
	return &env{store: store, j: j, home: home, data: data, uid: uid, svc: svc}
}

func (e *env) write(t *testing.T, rel string, size int) {
	t.Helper()
	p := filepath.Join(e.home, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0o640); err != nil {
		t.Fatal(err)
	}
}

func TestReserveCommitRelease(t *testing.T) {
	e := newEnv(t, 1000)
	ctx := context.Background()
	u, err := e.svc.Usage(ctx, e.uid)
	if err != nil || u.Limit != 1000 || u.Effective() != 0 || !u.Dirty {
		t.Fatalf("fresh usage: %+v %v", u, err)
	}
	r1, err := e.svc.Reserve(ctx, e.j, e.uid, 600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Reserve(ctx, e.j, e.uid, 500); err == nil {
		t.Fatal("second reservation over the limit accepted")
	} else {
		var qe *ExceededError
		if !errors.As(err, &qe) || qe.Reason != "quota" || qe.Used != 600 || qe.Needed != 500 || qe.Limit != 1000 {
			t.Fatalf("exceeded error: %v", err)
		}
	}
	u, _ = e.svc.Usage(ctx, e.uid)
	if u.Reserved != 600 || u.Free != 400 || u.Percent != 60 {
		t.Fatalf("usage with reservation: %+v", u)
	}
	if err := r1.Commit(ctx, nil, 550); err != nil {
		t.Fatal(err)
	}
	u, _ = e.svc.Usage(ctx, e.uid)
	if u.Reserved != 0 || u.Used != 550 || u.Free != 450 {
		t.Fatalf("after commit: %+v", u)
	}
	r2, err := e.svc.Reserve(ctx, e.j, e.uid, 450)
	if err != nil {
		t.Fatalf("exact fit rejected: %v", err)
	}
	r2.Release()
	r2.Release() // idempotent
	u, _ = e.svc.Usage(ctx, e.uid)
	if u.Reserved != 0 {
		t.Fatalf("release: %+v", u)
	}
	// apply clamps at zero
	if err := e.svc.Apply(ctx, nil, e.uid, -10000, 0, 0); err != nil {
		t.Fatal(err)
	}
	u, _ = e.svc.Usage(ctx, e.uid)
	if u.Used != 0 {
		t.Fatalf("clamp: %+v", u)
	}
	// unlimited user: limit 0 never exceeds
	if err := e.store.UpdateUserQuota(ctx, sqlc.UpdateUserQuotaParams{QuotaBytes: 0, UpdatedAt: db.Now(), ID: e.uid}); err != nil {
		t.Fatal(err)
	}
	r3, err := e.svc.Reserve(ctx, nil, e.uid, 1<<40)
	if err != nil {
		t.Fatalf("unlimited: %v", err)
	}
	r3.Release()
}

func TestDiskCheck(t *testing.T) {
	e := newEnv(t, 0)
	e.svc.MinDiskFree = 1 << 62
	_, err := e.svc.Reserve(context.Background(), e.j, e.uid, 1)
	var qe *ExceededError
	if !errors.As(err, &qe) || qe.Reason != "disk" {
		t.Fatalf("disk check: %v", err)
	}
	if _, err := e.svc.Reserve(context.Background(), nil, e.uid, 1); err != nil {
		t.Fatalf("nil jail skips the disk check: %v", err)
	}
}

func TestRecalculateMatchesIncremental(t *testing.T) {
	e := newEnv(t, 1<<30)
	ctx := context.Background()
	e.write(t, "a.bin", 1000)
	e.write(t, "dir/b.bin", 2500)
	e.write(t, "dir/sub/c.bin", 7)
	if err := os.Symlink("a.bin", filepath.Join(e.home, "link")); err != nil {
		t.Fatal(err)
	}
	trashDir, err := fsjail.OpenSys(e.data, "trash/"+e.uid)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = trashDir.Close() }()
	f, err := trashDir.Create("t1")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.Write(make([]byte, 300))
	_ = f.Close()
	res, err := e.svc.Recalculate(ctx, Target{UserID: e.uid, Jail: e.j, Trash: trashDir})
	if err != nil {
		t.Fatal(err)
	}
	// symlink counts as its link text (5 bytes "a.bin")
	if res.Usage.Used != 1000+2500+7+5 || res.Usage.Trash != 300 || res.Usage.Versions != 0 || res.Usage.FileCount != 4 || res.Usage.DirCount != 2 || res.Usage.Dirty {
		t.Fatalf("recalc: %+v", res.Usage)
	}
	if res.Drifted {
		t.Fatal("first calculation of a dirty user must not count as drift")
	}
	// incremental delta then recalc: equal, no drift
	if err := e.svc.Apply(ctx, nil, e.uid, 100, 0, 0); err != nil {
		t.Fatal(err)
	}
	e.write(t, "d.bin", 100)
	res, err = e.svc.Recalculate(ctx, Target{UserID: e.uid, Jail: e.j, Trash: trashDir})
	if err != nil || res.Drifted || res.Before != res.After {
		t.Fatalf("no drift expected: %+v %v", res, err)
	}
	// external change (SSH) → drift detected
	e.write(t, "big.bin", 50000)
	res, err = e.svc.Recalculate(ctx, Target{UserID: e.uid, Jail: e.j, Trash: trashDir})
	if err != nil || !res.Drifted || res.After-res.Before != 50000 {
		t.Fatalf("drift expected: %+v %v", res, err)
	}
	// RecalculateAll over the storage root
	storageRoot := filepath.Dir(e.home)
	done, failed := e.svc.RecalculateAll(ctx, storageRoot, e.data, time.Minute, nil)
	if done != 1 || failed != 0 {
		t.Fatalf("recalculate all: done=%d failed=%d", done, failed)
	}
	if n, err := e.svc.SumAll(ctx); err != nil || n != 1000+2500+7+5+100+50000+300 {
		t.Fatalf("sum all: %d %v", n, err)
	}
}
