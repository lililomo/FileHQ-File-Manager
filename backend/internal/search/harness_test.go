package search

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/auth"
	"kppbwi.app/filemanager/internal/config"
	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	fmexec "kppbwi.app/filemanager/internal/exec"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/listing"
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/tasks"
	"kppbwi.app/filemanager/internal/trash"
	"kppbwi.app/filemanager/internal/versions"
)

type env struct {
	cfg      *config.Config
	store    *db.Store
	quota    *quota.Service
	files    *files.Service
	engine   *tasks.Engine
	versions *versions.Store
	svc      *Service
	user     sqlc.User
	logs     *syncBuffer
	home     string
}

type envOpt func(o *Options, environ []string) []string

func withEngine(engine string) envOpt {
	return func(o *Options, e []string) []string { o.Engine = engine; return e }
}

func withEnv(kv string) envOpt {
	return func(_ *Options, e []string) []string { return append(e, kv) }
}

func newEnv(t *testing.T, opts ...envOpt) *env {
	t.Helper()
	storage := filepath.Join(t.TempDir(), "storage")
	if err := os.MkdirAll(storage, 0o750); err != nil {
		t.Fatal(err)
	}
	environ := []string{
		"APP_ENV=test", "APP_BASE_URL=https://file.example.test", "APP_SECRET=" + strings.Repeat("cd", 32),
		"STORAGE_ROOT=" + storage, "DATA_DIR=" + filepath.Join(storage, ".system"), "TRUSTED_PROXIES=127.0.0.1",
		"ADMIN_USERNAME=admin", "ADMIN_INITIAL_PASSWORD=Awal-sekali-2026!x",
	}
	ro := Options{Engine: "go"}
	for _, o := range opts {
		environ = o(&ro, environ)
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
	logs := &syncBuffer{}
	logger := config.NewLogger(logs, cfg.AppEnv, slog.LevelDebug)
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
	e := &env{cfg: cfg, store: store, user: user, logs: logs, home: filepath.Join(storage, "admin")}
	e.quota = quota.New(store, logger, nil)
	e.quota.MinDiskFree = 0
	locks := fsjail.NewLockManager()
	openJail := func(ctx context.Context, userID string) (*fsjail.Jail, func(), error) {
		u, err := store.GetUserByID(ctx, userID)
		if err != nil {
			return nil, nil, err
		}
		j, err := fsjail.OpenUser(cfg.StorageRoot, u.Username, u.ID)
		if err != nil {
			return nil, nil, err
		}
		return j, func() { _ = j.Close() }, nil
	}
	tr := trash.New(cfg.DataDir, cfg.CrossFSTrash, cfg.TrashRetentionDays, store, logger, nil)
	tr.Quota = e.quota
	tr.OpenJail = openJail
	t.Cleanup(tr.Close)
	e.files = files.New(files.Options{Repo: store, Locks: locks, Trash: tr, Logger: logger, Quota: e.quota})
	zero := time.Duration(0)
	e.engine = tasks.New(tasks.Options{Repo: store, Logger: logger, MaxPerUser: 3, MaxGlobal: 2, OpenJail: openJail, ErrorCode: files.TaskErrorCode, NotifyMinDuration: &zero})
	e.versions, err = versions.New(versions.Options{DataDir: cfg.DataDir, Limit: cfg.FileVersionLimit, Repo: store, Quota: e.quota, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.versions.Close() })
	collator := listing.NewCollator(cfg.DefaultLanguage)
	o := Options{DataDir: cfg.DataDir, RgPath: cfg.RgPath, Less: func(a, b string) bool { return listing.NaturalLess(a, b, collator) }, Runner: fmexec.New(filepath.Join(cfg.DataDir, "tmp")), MaxFileSize: cfg.EditorMaxFileSize,
		Repo: store, Quota: e.quota, Files: e.files, Locks: locks, Tasks: e.engine, Versions: e.versions, Logger: logger, Engine: ro.Engine,
		TagPaths: func(ctx context.Context, userID, color string) ([]string, error) {
			rows, err := store.ListFileTagsByColor(ctx, userID, color)
			if err != nil {
				return nil, err
			}
			out := make([]string, 0, len(rows))
			for _, r := range rows {
				out = append(out, r.Path)
			}
			return out, nil
		}}
	if ro.NameTimeout > 0 {
		o.NameTimeout = ro.NameTimeout
	}
	if ro.ContentTimeout > 0 {
		o.ContentTimeout = ro.ContentTimeout
	}
	if ro.NameRateLimit > 0 {
		o.NameRateLimit = ro.NameRateLimit
	}
	e.svc = New(o)
	ctx, cancel := context.WithCancel(context.Background())
	if err := e.engine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); e.engine.Shutdown(2 * time.Second) })
	return e
}

func (e *env) jail(t *testing.T) *fsjail.Jail {
	t.Helper()
	j, err := fsjail.OpenUser(e.cfg.StorageRoot, e.user.Username, e.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	return j
}

func (e *env) write(t *testing.T, rel, content string) {
	t.Helper()
	p := filepath.Join(e.home, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

func (e *env) read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(e.home, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// run waits for a task and returns its final row.
func (e *env) run(t *testing.T, tk tasks.Task) tasks.Task {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := e.engine.Wait(ctx, tk.ID); err != nil {
		t.Fatalf("wait %s: %v", tk.ID, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		got, err := e.engine.Get(ctx, e.user.ID, tk.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State.Final() {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %s never final", tk.ID)
	return tasks.Task{}
}

// collect runs a content search and returns every match.
func (e *env) collect(t *testing.T, q ContentQuery) ([]Match, Summary) {
	t.Helper()
	var out []Match
	sum, err := e.svc.SearchContent(context.Background(), e.jail(t), e.user.ID, q, func(m Match) error { out = append(out, m); return nil })
	if err != nil {
		t.Fatalf("content search: %v", err)
	}
	return out, sum
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func haveRg(t *testing.T) {
	t.Helper()
	if !fmexec.Executable("/usr/bin/rg") {
		t.Skip("ripgrep not installed")
	}
}
