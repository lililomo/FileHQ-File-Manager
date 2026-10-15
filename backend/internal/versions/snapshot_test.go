package versions

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/auth"
	"kppbwi.app/filemanager/internal/config"
	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/quota"
)

type env struct {
	store  *db.Store
	quota  *quota.Service
	st     *Store
	userID string
	j      *fsjail.Jail
	home   string
	data   string
}

func newEnv(t *testing.T, limit int) *env {
	t.Helper()
	storage := filepath.Join(t.TempDir(), "storage")
	if err := os.MkdirAll(storage, 0o750); err != nil {
		t.Fatal(err)
	}
	environ := []string{
		"APP_ENV=test", "APP_BASE_URL=https://file.example.test", "APP_SECRET=" + strings.Repeat("ab", 32),
		"STORAGE_ROOT=" + storage, "DATA_DIR=" + filepath.Join(storage, ".system"), "TRUSTED_PROXIES=127.0.0.1",
		"ADMIN_USERNAME=admin", "ADMIN_INITIAL_PASSWORD=Awal-sekali-2026!x",
	}
	cfg, err := config.Load(config.Options{Environ: environ, Cwd: "/opt/app", EUID: 1000, NumCPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(cfg.DataDir, "db", "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.MigrateUp(context.Background()); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	store := db.NewStore(database)
	authSvc := auth.NewService(cfg, store, audit.New(store, logger, nil), logger, nil)
	if err := authSvc.Seed(context.Background()); err != nil {
		t.Fatal(err)
	}
	user, err := store.GetUserByUsername(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fsjail.ProvisionHome(cfg.StorageRoot, user.Username, false); err != nil {
		t.Fatal(err)
	}
	q := quota.New(store, logger, nil)
	q.MinDiskFree = 0
	st, err := New(Options{DataDir: cfg.DataDir, Limit: limit, Repo: store, Quota: q, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	j, err := fsjail.OpenUser(cfg.StorageRoot, user.Username, user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	return &env{store: store, quota: q, st: st, userID: user.ID, j: j, home: filepath.Join(storage, "admin"), data: cfg.DataDir}
}

func (e *env) write(t *testing.T, rel, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.home, rel), []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

// TestSnapshotLimitDedupQuota: Snapshot keeps at most Limit versions per
// path (oldest pruned with its blob), skips identical content, accounts
// versions_bytes, and Restore writes the blob back (pre-image taken).
func TestSnapshotLimitDedupQuota(t *testing.T) {
	e := newEnv(t, 3)
	ctx := context.Background()
	e.write(t, "a.txt", "v1")
	v1, created, err := e.st.Snapshot(ctx, e.j, e.userID, "/a.txt", ReasonSave)
	if err != nil || !created || v1.No != 1 || v1.Size != 2 {
		t.Fatalf("v1 %+v %v %v", v1, created, err)
	}
	again, created, err := e.st.Snapshot(ctx, e.j, e.userID, "/a.txt", ReasonSave)
	if err != nil || created || again.ID != v1.ID {
		t.Fatalf("dedup %+v %v %v", again, created, err)
	}
	u, _ := e.quota.Usage(ctx, e.userID)
	if u.Versions != 2 {
		t.Fatalf("versions bytes %d", u.Versions)
	}
	for i, c := range []string{"v2!", "v3!!", "v4!!!"} {
		e.write(t, "a.txt", c)
		if _, created, err := e.st.Snapshot(ctx, e.j, e.userID, "/a.txt", ReasonReplaceInFiles); err != nil || !created {
			t.Fatalf("snapshot %d: %v %v", i, created, err)
		}
	}
	list, err := e.st.List(ctx, e.userID, "/a.txt")
	if err != nil || len(list) != 3 || list[0].No != 4 || list[2].No != 2 {
		t.Fatalf("list %+v %v", list, err)
	}
	if _, err := os.Stat(filepath.Join(e.data, "versions", e.userID, v1.ID)); !os.IsNotExist(err) {
		t.Fatalf("pruned blob still there: %v", err)
	}
	u, _ = e.quota.Usage(ctx, e.userID)
	if u.Versions != 3+4+5 {
		t.Fatalf("versions bytes after prune %d", u.Versions)
	}
	if _, err := e.st.Get(ctx, e.userID, v1.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pruned version get: %v", err)
	}
	// restore v2 ("v2!") over the current "v4!!!"
	e.write(t, "a.txt", "current")
	res, err := e.st.Restore(ctx, e.j, e.userID, list[2].ID)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(e.home, "a.txt"))
	if string(b) != "v2!" || res.Size != 3 || res.Version == nil || res.Version.Reason != ReasonRestore {
		t.Fatalf("restore %q %+v", b, res)
	}
	// the pre-image "current" is now version 5 and the limit still holds
	list, _ = e.st.List(ctx, e.userID, "/a.txt")
	if len(list) != 3 || list[0].No != 5 || list[0].Size != 7 {
		t.Fatalf("after restore %+v", list)
	}
	// delete returns the bytes
	if err := e.st.Delete(ctx, e.userID, list[0].ID); err != nil {
		t.Fatal(err)
	}
	u, _ = e.quota.Usage(ctx, e.userID)
	if u.Versions != 4+5 {
		t.Fatalf("versions bytes after delete %d", u.Versions)
	}
	// other users see nothing
	if _, err := e.st.Get(ctx, "other", list[1].ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-user get: %v", err)
	}
	// folders and symlinks have no versions
	if err := os.Mkdir(filepath.Join(e.home, "d"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.st.Snapshot(ctx, e.j, e.userID, "/d", ReasonSave); err == nil {
		t.Fatal("folder snapshot accepted")
	}
}

func TestVersionsDisabled(t *testing.T) {
	e := newEnv(t, 0)
	e.write(t, "a.txt", "x")
	v, created, err := e.st.Snapshot(context.Background(), e.j, e.userID, "/a.txt", ReasonSave)
	if err != nil || created || v != nil {
		t.Fatalf("%+v %v %v", v, created, err)
	}
	if _, err := e.st.Restore(context.Background(), e.j, e.userID, "x"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("restore disabled: %v", err)
	}
}
