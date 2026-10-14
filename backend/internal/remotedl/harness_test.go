package remotedl

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
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
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/netguard"
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/tasks"
)

// env is a real engine + database + admin home on a temp dir with the
// remote download runner registered. DNS goes through a fake resolver;
// loopback is allowed so httptest servers are reachable.
type env struct {
	cfg      *config.Config
	store    *db.Store
	quota    *quota.Service
	files    *files.Service
	engine   *tasks.Engine
	svc      *Service
	user     sqlc.User
	logs     *bytes.Buffer
	resolver *fakeResolver
	home     string
}

type fakeResolver struct {
	mu    sync.Mutex
	table map[string][]string
}

func (r *fakeResolver) set(host string, ips ...string) {
	r.mu.Lock()
	r.table[host] = ips
	r.mu.Unlock()
}

func (r *fakeResolver) LookupNetIP(_ context.Context, _, host string) ([]netip.Addr, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	raw, ok := r.table[host]
	if !ok {
		return nil, fmt.Errorf("no such host %s", host)
	}
	out := make([]netip.Addr, 0, len(raw))
	for _, a := range raw {
		out = append(out, netip.MustParseAddr(a))
	}
	return out, nil
}

type envOpt func(*Options, []string) []string

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
		"APP_ENV=test", "APP_BASE_URL=https://file.example.test", "APP_SECRET=" + strings.Repeat("cd", 32),
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
	logs := &bytes.Buffer{}
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
	e := &env{cfg: cfg, store: store, user: user, logs: logs, resolver: &fakeResolver{table: map[string][]string{}}, home: filepath.Join(storage, "admin")}
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
	e.files = files.New(files.Options{Repo: store, Locks: locks, Logger: logger, Quota: e.quota, BlockedExtensions: cfg.BlockedExtensions})
	zero := time.Duration(0)
	e.engine = tasks.New(tasks.Options{Repo: store, Logger: logger, MaxPerUser: 3, MaxGlobal: 2, OpenJail: openJail, ErrorCode: files.TaskErrorCode, NotifyMinDuration: &zero})
	guard := netguard.New(netguard.Policy{AllowLoopback: true})
	guard.Resolver = e.resolver
	guard.ConnectTimeout = 2 * time.Second
	o := Options{DataDir: cfg.DataDir, Enabled: cfg.RemoteDownloadEnabled, MaxSize: cfg.RemoteDownloadMaxSize, MaxUploadSize: cfg.MaxUploadSize,
		Version: "test", BaseURL: cfg.AppBaseURL, Repo: store, Quota: e.quota, Files: e.files, Locks: locks, Tasks: e.engine, Guard: guard,
		Logger: logger, RetryBackoff: []time.Duration{10 * time.Millisecond, 10 * time.Millisecond, 10 * time.Millisecond}, ProgressStep: 64 << 10}
	if ro.RetryBackoff != nil {
		o.RetryBackoff = ro.RetryBackoff
	}
	if ro.MaxSize > 0 {
		o.MaxSize = ro.MaxSize
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

func (e *env) enqueue(t *testing.T, p Payload) tasks.Task {
	t.Helper()
	tk, err := e.engine.Enqueue(context.Background(), tasks.EnqueueRequest{UserID: e.user.ID, Type: TaskType, Title: "t", Payload: p})
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

func (e *env) run(t *testing.T, p Payload) tasks.Task {
	t.Helper()
	tk := e.enqueue(t, p)
	return e.wait(t, tk.ID, 60*time.Second)
}

func result(t *testing.T, tk tasks.Task) Result {
	t.Helper()
	var r Result
	if err := json.Unmarshal(tk.Result, &r); err != nil {
		t.Fatalf("result: %v (%s)", err, string(tk.Result))
	}
	return r
}

func (e *env) tmpEntries(t *testing.T) []string {
	t.Helper()
	ents, err := os.ReadDir(filepath.Join(e.cfg.DataDir, "tmp"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []string
	for _, en := range ents {
		if strings.HasPrefix(en.Name(), TmpPrefix) {
			out = append(out, en.Name())
		}
	}
	return out
}

func (e *env) reserved() int64 {
	u, _ := e.quota.Usage(context.Background(), e.user.ID)
	return u.Reserved
}

func sha256File(t *testing.T, p string) string {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// pattern returns n deterministic bytes (fast xorshift) and their sha256.
func pattern(n int64) ([]byte, string) {
	buf := make([]byte, n)
	var x uint64 = 0x9E3779B97F4A7C15
	for i := range buf {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		buf[i] = byte(x)
	}
	sum := sha256.Sum256(buf)
	return buf, hex.EncodeToString(sum[:])
}

// server is an httptest server registered in the fake resolver as
// "files.test" (URL helper) with handlers the tests compose.
type server struct {
	srv  *httptest.Server
	mux  *http.ServeMux
	host string // files.test:port
}

func (e *env) newServer(t *testing.T) *server {
	t.Helper()
	mux := http.NewServeMux()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	_, port, _ := net.SplitHostPort(u.Host)
	e.resolver.set("files.test", "127.0.0.1")
	return &server{srv: srv, mux: mux, host: "files.test:" + port}
}

func (s *server) url(p string) string { return "http://" + s.host + p }

// serveBytes answers with http.ServeContent (Range, ETag) for name.
func (s *server) serveBytes(path, name string, data []byte, extra http.Header) {
	mod := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	s.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		for k, v := range extra {
			w.Header()[k] = v
		}
		w.Header().Set("ETag", `"`+name+`"`)
		http.ServeContent(w, r, name, mod, bytes.NewReader(data))
	})
}
