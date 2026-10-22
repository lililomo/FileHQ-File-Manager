package admin

import (
	"context"
	"errors"
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
	"kppbwi.app/filemanager/internal/quota"
)

// Test harness: real SQLite + temp STORAGE_ROOT, seeded admin, no task
// engine (deletion runs synchronously).

type env struct {
	t     *testing.T
	cfg   *config.Config
	store *db.Store
	auth  *auth.Service
	svc   *Service
	admin sqlc.User
	actor Actor
	clock *clock
	mu    sync.Mutex
	users []string // ids reported through OnUserChanged
	rts   int      // OnSettingsChanged calls
}

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func newEnv(t *testing.T, extraEnv ...string) *env {
	t.Helper()
	storage := filepath.Join(t.TempDir(), "storage")
	if err := os.MkdirAll(storage, 0o750); err != nil {
		t.Fatal(err)
	}
	environ := append([]string{
		"APP_ENV=test",
		"APP_BASE_URL=https://file.example.test",
		"APP_SECRET=" + strings.Repeat("ab", 32),
		"STORAGE_ROOT=" + storage,
		"DATA_DIR=" + filepath.Join(storage, ".system"),
		"ADMIN_USERNAME=admin",
		"ADMIN_INITIAL_PASSWORD=Initial-Passw0rd-2026!",
	}, extraEnv...)
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
	store := db.NewStore(database)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	e := &env{t: t, cfg: cfg, store: store, clock: &clock{t: time.Date(2026, 10, 22, 9, 0, 0, 0, time.UTC)}}
	rec := audit.New(store, logger, e.clock.Now)
	e.auth = auth.NewService(cfg, store, rec, logger, e.clock.Now)
	if err := e.auth.Seed(context.Background()); err != nil {
		t.Fatal(err)
	}
	e.admin, err = store.GetUserByUsername(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	e.actor = Actor{ID: e.admin.ID, Username: "admin", IP: "203.0.113.1", UserAgent: "test"}
	e.svc, err = New(context.Background(), Options{Cfg: cfg, Repo: store, Auth: e.auth, Audit: rec, Quota: quota.New(store, logger, e.clock.Now),
		Build: BuildInfo{Version: "v-test", Commit: "abc1234", BuildDate: "2026-10-22"}, Logger: logger, Now: e.clock.Now,
		Deps: config.CheckDeps(cfg.ToolValues()),
		OnSettingsChanged: func(_ *config.Runtime) {
			e.mu.Lock()
			e.rts++
			e.mu.Unlock()
		},
		OnUserChanged: func(id string) {
			e.mu.Lock()
			e.users = append(e.users, id)
			e.mu.Unlock()
		}})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// create adds a user through the service and returns its row.
func (e *env) create(username string, role string) (sqlc.User, string) {
	e.t.Helper()
	res, err := e.svc.CreateUser(context.Background(), e.actor, CreateInput{Username: username, DisplayName: strings.ToUpper(username[:1]) + username[1:], Role: role})
	if err != nil {
		e.t.Fatalf("create %s: %v", username, err)
	}
	u, err := e.store.GetUserByID(context.Background(), res.User.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return u, res.TemporaryPassword
}

// login provisions the home folder like a real first login.
func (e *env) login(username, password string) auth.LoginResult {
	e.t.Helper()
	res, err := e.auth.Login(context.Background(), auth.LoginInput{Username: username, Password: password, IP: "203.0.113.9", UserAgent: "test"})
	if err != nil {
		e.t.Fatalf("login %s: %v", username, err)
	}
	return res
}

// audits lists audit rows of one action, newest first.
func (e *env) audits(action string) []sqlc.AuditLog {
	e.t.Helper()
	rows, err := e.store.ListAudit(context.Background(), db.AuditFilter{Action: &action, Limit: 100})
	if err != nil {
		e.t.Fatal(err)
	}
	return rows
}

func asAdminErr(t *testing.T, err error) *Error {
	t.Helper()
	var ae *Error
	if !errors.As(err, &ae) {
		t.Fatalf("expected *admin.Error, got %T: %v", err, err)
	}
	return ae
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
