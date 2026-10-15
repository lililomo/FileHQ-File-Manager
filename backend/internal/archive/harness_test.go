package archive

import (
	"bytes"
	"context"
	"encoding/json"
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
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/tasks"
	"kppbwi.app/filemanager/internal/trash"
)

const sevenZipBin = "/usr/lib/7zip/7z"

// env is a real engine + database + admin home on a temp dir with the
// archive runners registered.
type env struct {
	cfg    *config.Config
	store  *db.Store
	quota  *quota.Service
	files  *files.Service
	trash  *trash.Store
	engine *tasks.Engine
	svc    *Service
	user   sqlc.User
	logs   *syncBuffer
	home   string
	root   string
}

type envOpt func(o *Options, environ []string) []string

func withOptions(fn func(o *Options)) envOpt {
	return func(o *Options, e []string) []string { fn(o); return e }
}

func newEnv(t *testing.T, opts ...envOpt) *env {
	t.Helper()
	storage := filepath.Join(t.TempDir(), "storage")
	if err := os.MkdirAll(storage, 0o750); err != nil {
		t.Fatal(err)
	}
	environ := []string{
		"APP_ENV=test", "APP_BASE_URL=https://file.example.test", "APP_SECRET=" + strings.Repeat("ef", 32),
		"STORAGE_ROOT=" + storage, "DATA_DIR=" + filepath.Join(storage, ".system"), "TRUSTED_PROXIES=127.0.0.1",
		"ADMIN_USERNAME=admin", "ADMIN_INITIAL_PASSWORD=Awal-sekali-2026!x", "BLOCKED_EXTENSIONS=exe,bat",
	}
	ro := Options{}
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
	e := &env{cfg: cfg, store: store, user: user, logs: logs, home: filepath.Join(storage, "admin"), root: storage}
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
	e.trash = trash.New(cfg.DataDir, cfg.CrossFSTrash, cfg.TrashRetentionDays, store, logger, nil)
	e.trash.Quota = e.quota
	e.trash.Unique = files.UniqueName
	e.trash.OnPurge = files.PurgeReferences
	e.trash.OpenJail = openJail
	t.Cleanup(e.trash.Close)
	e.files = files.New(files.Options{Repo: store, Locks: locks, Trash: e.trash, Logger: logger, Quota: e.quota, BlockedExtensions: cfg.BlockedExtensions})
	zero := time.Duration(0)
	e.engine = tasks.New(tasks.Options{Repo: store, Logger: logger, MaxPerUser: 3, MaxGlobal: 2, OpenJail: openJail, ErrorCode: files.TaskErrorCode, NotifyMinDuration: &zero})
	o := Options{DataDir: cfg.DataDir, SevenZip: cfg.SevenZipPath, Runner: fmexec.New(filepath.Join(cfg.DataDir, "tmp")),
		MaxExtractSize: cfg.ArchiveMaxExtractSize, MaxEntries: cfg.ArchiveMaxEntries, Repo: store, Quota: e.quota, Files: e.files, Locks: locks,
		Tasks: e.engine, Logger: logger, ProgressStep: 64 << 10, CLITimeout: 2 * time.Minute}
	if ro.MaxExtractSize > 0 {
		o.MaxExtractSize = ro.MaxExtractSize
	}
	if ro.MaxEntries > 0 {
		o.MaxEntries = ro.MaxEntries
	}
	if ro.RatioMinBytes > 0 {
		o.RatioMinBytes = ro.RatioMinBytes
	}
	if ro.Ratio > 0 {
		o.Ratio = ro.Ratio
	}
	if ro.SevenZip == "-" {
		o.SevenZip = ""
	}
	e.svc = New(o)
	ctx, cancel := context.WithCancel(context.Background())
	if err := e.engine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); e.engine.Shutdown(2 * time.Second) })
	return e
}

func (e *env) setQuota(t *testing.T, bytes int64) {
	t.Helper()
	if err := e.store.UpdateUserQuota(context.Background(), sqlc.UpdateUserQuotaParams{QuotaBytes: bytes, UpdatedAt: db.Now(), ID: e.user.ID}); err != nil {
		t.Fatal(err)
	}
}

func (e *env) enqueue(t *testing.T, typ string, payload any, password string) tasks.Task {
	t.Helper()
	var secrets map[string]string
	if password != "" {
		secrets = map[string]string{"password": password}
	}
	tk, err := e.engine.Enqueue(context.Background(), tasks.EnqueueRequest{UserID: e.user.ID, Type: typ, Title: "t", Payload: payload, Secrets: secrets})
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func (e *env) wait(t *testing.T, id string, final time.Duration) tasks.Task {
	t.Helper()
	deadline := time.Now().Add(final)
	for time.Now().Before(deadline) {
		tk, err := e.engine.Get(context.Background(), e.user.ID, id)
		if err != nil {
			t.Fatal(err)
		}
		if tk.State.Final() {
			return tk
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("task %s did not finish", id)
	return tasks.Task{}
}

func (e *env) extract(t *testing.T, p ExtractPayload, password string) tasks.Task {
	t.Helper()
	if p.Policy == "" {
		p.Policy = "fail"
	}
	p.HasPassword = password != ""
	tk := e.enqueue(t, TaskExtract, p, password)
	return e.wait(t, tk.ID, 120*time.Second)
}

func (e *env) compress(t *testing.T, p CompressPayload, password string) tasks.Task {
	t.Helper()
	if p.Level == "" {
		p.Level = LevelNormal
	}
	p.HasPassword = password != ""
	tk := e.enqueue(t, TaskCompress, p, password)
	return e.wait(t, tk.ID, 120*time.Second)
}

// write puts a file into the admin home (host side).
func (e *env) write(t *testing.T, rel string, content []byte) {
	t.Helper()
	p := filepath.Join(e.home, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, content, 0o640); err != nil {
		t.Fatal(err)
	}
}

func (e *env) read(t *testing.T, rel string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(e.home, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func (e *env) exists(rel string) bool {
	_, err := os.Lstat(filepath.Join(e.home, filepath.FromSlash(rel)))
	return err == nil
}

// tmpDirs lists DATA_DIR/tmp/ar-* folders left behind.
func (e *env) tmpDirs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(e.cfg.DataDir, "tmp"))
	if err != nil {
		return nil
	}
	var out []string
	for _, d := range entries {
		if strings.HasPrefix(d.Name(), TmpPrefix) {
			out = append(out, d.Name())
		}
	}
	return out
}

// outsideCanary plants a file above the home and returns a checker that
// fails when anything new appeared outside the destination.
func (e *env) snapshotOutside(t *testing.T) func() {
	t.Helper()
	before := walkNames(t, e.root)
	return func() {
		t.Helper()
		after := walkNames(t, e.root)
		for name := range after {
			if _, ok := before[name]; ok {
				continue
			}
			rel, _ := filepath.Rel(e.root, name)
			if strings.HasPrefix(rel, "admin/") || strings.HasPrefix(rel, ".system/") {
				continue
			}
			t.Fatalf("file created outside the home: %s", rel)
		}
	}
}

func walkNames(t *testing.T, root string) map[string]struct{} {
	t.Helper()
	out := map[string]struct{}{}
	_ = filepath.WalkDir(root, func(p string, _ os.DirEntry, err error) error {
		if err == nil {
			out[p] = struct{}{}
		}
		return nil
	})
	return out
}

func resultOf[T any](t *testing.T, tk tasks.Task) T {
	t.Helper()
	var v T
	if tk.State != tasks.Done {
		code, msg := "", ""
		if tk.Error != nil {
			code, msg = tk.Error.Code, tk.Error.Message
		}
		t.Fatalf("task %s %s: %s %s", tk.ID, tk.State, code, msg)
	}
	if err := json.Unmarshal(tk.Result, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func failedWith(t *testing.T, tk tasks.Task, code string) {
	t.Helper()
	if tk.State != tasks.Failed || tk.Error == nil || tk.Error.Code != code {
		got := "<nil>"
		if tk.Error != nil {
			got = tk.Error.Code + ": " + tk.Error.Message
		}
		t.Fatalf("want failed %s, got %s %s", code, tk.State, got)
	}
}

func have7z(t *testing.T) {
	t.Helper()
	if !fmexec.Executable(sevenZipBin) {
		t.Skip("7z not installed")
	}
}

// syncBuffer is a goroutine-safe log sink: runners log from engine goroutines
// while tests read the buffer (race detector finding of review C).
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

func (s *syncBuffer) Bytes() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.b.Bytes()...)
}

func (s *syncBuffer) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.b.Reset()
}
