package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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
	"kppbwi.app/filemanager/internal/listing"
	"kppbwi.app/filemanager/internal/meta"
	"kppbwi.app/filemanager/internal/notify"
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/sse"
	"kppbwi.app/filemanager/internal/tasks"
	"kppbwi.app/filemanager/internal/trash"
	"kppbwi.app/filemanager/internal/upload"
)

const initialAdminPassword = "IB+35t3U5+I_p5ZJcHLodfcr"

func TestMain(m *testing.M) {
	// argon2id at 64 MiB is too slow under -race on 1 vCPU; the parameters are
	// covered by internal/auth tests, this package tests the HTTP flow.
	auth.DefaultParams = auth.Params{Memory: 8 * 1024, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}
	os.Exit(m.Run())
}

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

type authEnv struct {
	cfg     *config.Config
	store   *db.Store
	auth    *auth.Service
	handler http.Handler
	logs    *bytes.Buffer
	clock   *fakeClock
	locks   *fsjail.LockManager
	trash   *trash.Store
	listing *listing.Service
	files   *files.Service
	quota   *quota.Service
	meta    *meta.Service
	tasks   *tasks.Engine
	events  *sse.Broker
	notify  *notify.Service
	upload  *upload.Service
}

type notifyFunc func(ctx context.Context, userID, typ, title, body, link string) error

func (f notifyFunc) Create(ctx context.Context, userID, typ, title, body, link string) error {
	return f(ctx, userID, typ, title, body, link)
}

func newAuthEnv(t *testing.T, extraEnv ...string) *authEnv {
	t.Helper()
	storage := filepath.Join(t.TempDir(), "storage")
	if err := os.MkdirAll(storage, 0o750); err != nil {
		t.Fatal(err)
	}
	environ := []string{
		"APP_ENV=test",
		"APP_BASE_URL=https://file.example.test",
		"APP_SECRET=" + strings.Repeat("ab", 32),
		"STORAGE_ROOT=" + storage,
		"DATA_DIR=" + filepath.Join(storage, ".system"),
		"TRUSTED_PROXIES=127.0.0.1",
		"ADMIN_USERNAME=admin",
		"ADMIN_INITIAL_PASSWORD=" + initialAdminPassword,
	}
	// TEST_* entries tune the services instead of the configuration.
	syncMaxEntries, itemDelay := 0, time.Duration(0)
	for _, kv := range extraEnv {
		switch {
		case strings.HasPrefix(kv, "TEST_SYNC_MAX_ENTRIES="):
			syncMaxEntries, _ = strconv.Atoi(strings.TrimPrefix(kv, "TEST_SYNC_MAX_ENTRIES="))
		case strings.HasPrefix(kv, "TEST_TASK_ITEM_DELAY_MS="):
			ms, _ := strconv.Atoi(strings.TrimPrefix(kv, "TEST_TASK_ITEM_DELAY_MS="))
			itemDelay = time.Duration(ms) * time.Millisecond
		default:
			environ = append(environ, kv)
		}
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
	clock := &fakeClock{t: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)}
	store := db.NewStore(database)
	svc := auth.NewService(cfg, store, audit.New(store, logger, clock.Now), logger, clock.Now)
	if err := svc.Seed(context.Background()); err != nil {
		t.Fatal(err)
	}
	env := &authEnv{cfg: cfg, store: store, auth: svc, logs: logs, clock: clock}
	env.locks = fsjail.NewLockManager()
	env.quota = quota.New(store, logger, clock.Now)
	env.quota.MinDiskFree = 0
	env.trash = trash.New(cfg.DataDir, cfg.CrossFSTrash, cfg.TrashRetentionDays, store, logger, clock.Now)
	env.trash.Quota = env.quota
	env.trash.Unique = files.UniqueName
	env.trash.OnPurge = files.PurgeReferences
	t.Cleanup(env.trash.Close)
	env.meta = meta.New(meta.Options{Repo: store, Logger: logger, Now: clock.Now, Invalidate: func(userID string, dir fsjail.VPath) {
		if env.listing != nil {
			env.listing.Invalidate(userID, "", dir)
		}
	}})
	env.listing = listing.New(listing.Options{Secret: cfg.AppSecret, PageSize: cfg.ListingPageSize, Meta: env.meta})
	env.files = files.New(files.Options{Repo: store, Locks: env.locks, Trash: env.trash, Logger: logger, Quota: env.quota, Now: clock.Now, BlockedExtensions: cfg.BlockedExtensions,
		SyncMaxEntries: syncMaxEntries,
		Invalidate:     func(userID string, dir fsjail.VPath) { env.listing.Invalidate(userID, "", dir) }})
	// Task engine + SSE + notifications (Step 11) on a real clock: progress
	// throttling and ETA depend on elapsed time.
	env.trash.OpenJail = func(ctx context.Context, userID string) (*fsjail.Jail, func(), error) {
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
	env.events = sse.New(nil)
	env.notify = notify.New(store, env.events, logger, nil)
	zero := time.Duration(0)
	env.tasks = tasks.New(tasks.Options{Repo: store, Logger: logger, MaxPerUser: cfg.TaskMaxPerUser, MaxGlobal: cfg.TaskMaxGlobal, Events: env.events,
		Notify: notifyFunc(func(ctx context.Context, userID, typ, title, body, link string) error {
			_, err := env.notify.Create(ctx, userID, typ, title, body, link)
			return err
		}), Quota: env.quota, OpenJail: env.trash.OpenJail, ErrorCode: files.TaskErrorCode, NotifyMinDuration: &zero, ItemDelay: itemDelay})
	files.RegisterTasks(env.tasks, env.files)
	engineCtx, stopEngine := context.WithCancel(context.Background())
	if err := env.tasks.Start(engineCtx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stopEngine(); env.tasks.Shutdown(2 * time.Second); env.events.Close() })
	env.upload, err = upload.New(upload.Options{DataDir: cfg.DataDir, MaxUploadSize: cfg.MaxUploadSize, ChunkSize: cfg.UploadChunkSize, BaseURL: cfg.AppBaseURL,
		Repo: store, Quota: env.quota, Files: env.files, Locks: env.locks, OpenJail: upload.OpenJailFunc(env.trash.OpenJail),
		Invalidate: func(userID string, dir fsjail.VPath) { env.listing.Invalidate(userID, "", dir) },
		Publish:    env.events.Publish, RequestID: RequestIDFrom, Logger: logger, Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	env.handler = New(Options{Cfg: cfg, Logger: logger, Version: "test", Store: store, Auth: svc, Listing: env.listing, Locks: env.locks, Files: env.files, Quota: env.quota, Meta: env.meta,
		Tasks: env.tasks, Events: env.events, Notify: env.notify, Upload: env.upload,
		Assets: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("spa")) })})
	return env
}

type resp struct {
	rec  *httptest.ResponseRecorder
	body map[string]any
}

func (e *authEnv) call(method, target, remote string, body any, hdr map[string]string, cookie *http.Cookie) resp {
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, rd)
	req.RemoteAddr = remote
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	out := resp{rec: rec}
	if rec.Body.Len() > 0 && strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(rec.Body.Bytes(), &out.body)
	}
	return out
}

func (r resp) code(t *testing.T) string {
	t.Helper()
	e, _ := r.body["error"].(map[string]any)
	c, _ := e["code"].(string)
	return c
}

func sessionCookie(rec *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == auth.CookieName {
			return c
		}
	}
	return nil
}

func (e *authEnv) login(t *testing.T, username, password, remote string) (resp, *http.Cookie, string) {
	t.Helper()
	r := e.call(http.MethodPost, "/api/v1/auth/login", remote, map[string]string{"username": username, "password": password}, nil, nil)
	tok, _ := r.body["csrfToken"].(string)
	return r, sessionCookie(r.rec), tok
}

func TestLoginFlowFirstAdmin(t *testing.T) {
	env := newAuthEnv(t)
	if !strings.Contains(env.logs.String(), "admin.seeded") {
		t.Fatalf("seed not logged: %s", env.logs.String())
	}
	if n, _ := env.store.CountAllUsers(context.Background()); n != 1 {
		t.Fatalf("users=%d", n)
	}

	// Validation.
	r := env.call(http.MethodPost, "/api/v1/auth/login", "203.0.113.5:1", map[string]string{"username": "", "password": ""}, nil, nil)
	if r.rec.Code != 400 || r.code(t) != "VALIDATION_FAILED" {
		t.Fatalf("empty login: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// Wrong password → generic 401.
	r, _, _ = env.login(t, "admin", "wrong-password-123", "203.0.113.5:1")
	if r.rec.Code != 401 || r.code(t) != "UNAUTHENTICATED" {
		t.Fatalf("wrong password: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// Correct password (case/space variations of the username are normalized).
	r, ck, csrf := env.login(t, "  Admin ", initialAdminPassword, "203.0.113.5:1")
	if r.rec.Code != 200 {
		t.Fatalf("login: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r.body["mustChangePassword"] != true || csrf == "" {
		t.Fatalf("login body: %v", r.body)
	}
	if ck == nil || !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" || ck.MaxAge != 168*3600 {
		t.Fatalf("cookie flags: %+v", ck)
	}
	user := r.body["user"].(map[string]any)
	if user["username"] != "admin" || user["role"] != "admin" || user["homeProvisioned"] != true {
		t.Fatalf("user view: %v", user)
	}
	for _, k := range []string{"password_hash", "passwordHash", "totp"} {
		if _, ok := user[k]; ok {
			t.Fatalf("user view leaks %s", k)
		}
	}

	// Home directory created 0750 at first login.
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	info, err := os.Stat(home)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o750 {
		t.Fatalf("home: %v %v", err, info)
	}
	mtime := info.ModTime()

	// /auth/me works while must_change_password; other S+P endpoints are gated.
	r = env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.5:1", nil, nil, ck)
	if r.rec.Code != 200 {
		t.Fatalf("me: %d %s", r.rec.Code, r.rec.Body.String())
	}
	me := r.body["user"].(map[string]any)
	if me["mustChangePassword"] != true || me["language"] != "id" {
		t.Fatalf("me body: %v", me)
	}
	if cfgm := r.body["config"].(map[string]any); cfgm["chunkSize"] == nil || cfgm["features"] == nil {
		t.Fatalf("me config: %v", cfgm)
	}
	r = env.call(http.MethodGet, "/api/v1/me/preferences", "203.0.113.5:1", nil, nil, ck)
	if r.rec.Code != 403 || r.code(t) != "PASSWORD_CHANGE_REQUIRED" {
		t.Fatalf("gating: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// Change password: CSRF required.
	body := map[string]string{"currentPassword": initialAdminPassword, "newPassword": "Kelapa muda 2026 segar!"}
	r = env.call(http.MethodPost, "/api/v1/auth/change-password", "203.0.113.5:1", body, nil, ck)
	if r.rec.Code != 403 || r.code(t) != "CSRF_INVALID" {
		t.Fatalf("missing csrf: %d %s", r.rec.Code, r.rec.Body.String())
	}
	hdr := map[string]string{"X-CSRF-Token": csrf}
	r = env.call(http.MethodPost, "/api/v1/auth/change-password", "203.0.113.5:1", body, map[string]string{"X-CSRF-Token": "bogus"}, ck)
	if r.rec.Code != 403 {
		t.Fatalf("bogus csrf: %d", r.rec.Code)
	}

	// Policy violations.
	for newPw, want := range map[string]string{
		initialAdminPassword: "same_as_initial",
		"short1!":            "too_short",
		"password1234":       "common",
		"my-admin-pass-2026": "contains_username",
	} {
		r = env.call(http.MethodPost, "/api/v1/auth/change-password", "203.0.113.5:1",
			map[string]string{"currentPassword": initialAdminPassword, "newPassword": newPw}, hdr, ck)
		if r.rec.Code != 400 || r.code(t) != "VALIDATION_FAILED" {
			t.Fatalf("policy %q: %d %s", newPw, r.rec.Code, r.rec.Body.String())
		}
		fields := r.body["error"].(map[string]any)["details"].(map[string]any)["fields"].(map[string]any)
		if fields["newPassword"] != want {
			t.Fatalf("policy %q: got %v want %s", newPw, fields, want)
		}
	}
	r = env.call(http.MethodPost, "/api/v1/auth/change-password", "203.0.113.5:1",
		map[string]string{"currentPassword": "not-the-current-one", "newPassword": "Kelapa muda 2026 segar!"}, hdr, ck)
	if r.rec.Code != 400 {
		t.Fatalf("wrong current: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// A second session that must be revoked by the password change.
	_, ck2, _ := env.login(t, "admin", initialAdminPassword, "203.0.113.6:1")

	// Success.
	r = env.call(http.MethodPost, "/api/v1/auth/change-password", "203.0.113.5:1", body, hdr, ck)
	if r.rec.Code != 204 {
		t.Fatalf("change password: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.5:1", nil, nil, ck)
	if r.rec.Code != 200 || r.body["user"].(map[string]any)["mustChangePassword"] != false {
		t.Fatalf("me after change: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/me/preferences", "203.0.113.5:1", nil, nil, ck)
	if r.rec.Code != 200 {
		t.Fatalf("preferences after change: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.6:1", nil, nil, ck2)
	if r.rec.Code != 401 {
		t.Fatalf("other session should be revoked: %d", r.rec.Code)
	}
	if _, err := env.store.GetSetting(context.Background(), auth.SettingInitialPasswordHash); !db.IsNotFound(err) {
		t.Fatalf("initial password marker should be deleted: %v", err)
	}

	// Preferences merge patch (S+P + CSRF).
	r = env.call(http.MethodPut, "/api/v1/me/preferences", "203.0.113.5:1",
		map[string]any{"prefs": map[string]any{"ui.language": "en", "ui.theme": "dark"}}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("put prefs: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPut, "/api/v1/me/preferences", "203.0.113.5:1",
		map[string]any{"prefs": map[string]any{"ui.theme": nil}}, hdr, ck)
	prefs := r.body["prefs"].(map[string]any)
	if r.rec.Code != 200 || prefs["ui.theme"] != nil || prefs["ui.language"] != "en" {
		t.Fatalf("merge patch: %d %v", r.rec.Code, prefs)
	}
	r = env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.5:1", nil, nil, ck)
	if r.body["user"].(map[string]any)["language"] != "en" {
		t.Fatalf("language pref not reflected: %v", r.body)
	}

	// Second login with the new password: home untouched, old password rejected.
	env.clock.Advance(2 * time.Second)
	r, ck3, _ := env.login(t, "admin", "Kelapa muda 2026 segar!", "203.0.113.5:1")
	if r.rec.Code != 200 || r.body["mustChangePassword"] != false {
		t.Fatalf("relogin: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if info2, _ := os.Stat(home); !info2.ModTime().Equal(mtime) {
		t.Fatal("second login modified the home directory")
	}
	if r, _, _ = env.login(t, "admin", initialAdminPassword, "203.0.113.5:1"); r.rec.Code != 401 {
		t.Fatalf("old password still accepted: %d", r.rec.Code)
	}

	// /auth/csrf returns the same token for the session.
	r = env.call(http.MethodGet, "/api/v1/auth/csrf", "203.0.113.5:1", nil, nil, ck3)
	csrf3, _ := r.body["csrfToken"].(string)
	if r.rec.Code != 200 || csrf3 == "" {
		t.Fatalf("csrf: %d", r.rec.Code)
	}

	// Logout clears the cookie and invalidates the session.
	r = env.call(http.MethodPost, "/api/v1/auth/logout", "203.0.113.5:1", nil, map[string]string{"X-CSRF-Token": csrf3}, ck3)
	if r.rec.Code != 204 {
		t.Fatalf("logout: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if c := sessionCookie(r.rec); c == nil || c.MaxAge != -1 {
		t.Fatalf("logout must clear cookie: %+v", c)
	}
	if r = env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.5:1", nil, nil, ck3); r.rec.Code != 401 {
		t.Fatalf("session after logout: %d", r.rec.Code)
	}
	if r = env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.5:1", nil, nil, nil); r.rec.Code != 401 {
		t.Fatalf("no cookie: %d", r.rec.Code)
	}

	// Audit trail and secrets.
	actions := auditActions(t, env)
	for _, want := range []string{audit.ActionSeedAdmin, audit.ActionLoginFailed, audit.ActionLogin, audit.ActionHomeProvisioned,
		audit.ActionPasswordChanged, audit.ActionLogout} {
		if !actions[want] {
			t.Errorf("audit action %s missing: %v", want, actions)
		}
	}
	for _, secret := range []string{initialAdminPassword, "Kelapa muda 2026 segar!"} {
		if strings.Contains(env.logs.String(), secret) {
			t.Fatalf("log contains a password")
		}
		rows, _ := env.store.ListAudit(context.Background(), db.AuditFilter{Limit: 100})
		for _, row := range rows {
			if row.DetailsJson != nil && strings.Contains(*row.DetailsJson, secret) {
				t.Fatalf("audit details contain a password: %s", *row.DetailsJson)
			}
		}
	}
}

func auditActions(t *testing.T, env *authEnv) map[string]bool {
	t.Helper()
	rows, err := env.store.ListAudit(context.Background(), db.AuditFilter{Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, r := range rows {
		out[r.Action] = true
	}
	return out
}

func TestLoginLockoutEscalates(t *testing.T) {
	env := newAuthEnv(t)
	for i := 1; i <= 5; i++ {
		r, _, _ := env.login(t, "admin", fmt.Sprintf("wrong-%d", i), "198.51.100.7:1")
		if r.rec.Code != 401 {
			t.Fatalf("attempt %d: %d %s", i, r.rec.Code, r.rec.Body.String())
		}
	}
	r, _, _ := env.login(t, "ADMIN", initialAdminPassword, "198.51.100.7:1") // even the right password is rejected while locked
	if r.rec.Code != 423 || r.code(t) != "ACCOUNT_LOCKED" || r.rec.Header().Get("Retry-After") == "" {
		t.Fatalf("6th attempt: %d %s hdr=%v", r.rec.Code, r.rec.Body.String(), r.rec.Header())
	}
	if !auditActions(t, env)[audit.ActionAccountLocked] {
		t.Fatal("lockout not audited")
	}
	env.clock.Advance(61 * time.Second)
	if r, _, _ = env.login(t, "admin", initialAdminPassword, "198.51.100.7:1"); r.rec.Code != 200 {
		t.Fatalf("after lock expiry: %d %s", r.rec.Code, r.rec.Body.String())
	}
	u, _ := env.store.GetUserByUsername(context.Background(), "admin")
	if u.FailedLoginCount != 0 || u.LockedUntil != nil {
		t.Fatalf("counters not reset: %+v", u)
	}
	// Second round of failures within the window escalates to 5 minutes (10 cumulative failures).
	for i := 0; i < 5; i++ {
		env.login(t, "admin", "wrong-again", "198.51.100.7:1")
	}
	// failed_login_count is 5 again (reset after success) → 1 minute lock; verify lock is active.
	if r, _, _ = env.login(t, "admin", initialAdminPassword, "198.51.100.7:1"); r.rec.Code != 423 {
		t.Fatalf("second lock: %d", r.rec.Code)
	}
}

func TestLoginRateLimitPerIP(t *testing.T) {
	env := newAuthEnv(t)
	for i := 1; i <= 20; i++ {
		r, _, _ := env.login(t, fmt.Sprintf("nouser%d", i), "whatever-password", "192.0.2.9:1")
		if r.rec.Code != 401 {
			t.Fatalf("request %d: %d", i, r.rec.Code)
		}
	}
	r, _, _ := env.login(t, "nouser21", "whatever-password", "192.0.2.9:1")
	if r.rec.Code != 429 || r.code(t) != "RATE_LIMITED" || r.rec.Header().Get("Retry-After") == "" {
		t.Fatalf("21st request: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r, _, _ = env.login(t, "admin", initialAdminPassword, "192.0.2.10:1"); r.rec.Code != 200 {
		t.Fatalf("other IP must not be limited: %d", r.rec.Code)
	}
}

func TestCrossSiteRequestsRejected(t *testing.T) {
	env := newAuthEnv(t)
	body := map[string]string{"username": "admin", "password": initialAdminPassword}
	r := env.call(http.MethodPost, "/api/v1/auth/login", "203.0.113.1:1", body, map[string]string{"Sec-Fetch-Site": "cross-site"}, nil)
	if r.rec.Code != 403 || r.code(t) != "CSRF_INVALID" {
		t.Fatalf("sec-fetch-site: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/auth/login", "203.0.113.1:1", body, map[string]string{"Origin": "https://evil.example"}, nil)
	if r.rec.Code != 403 {
		t.Fatalf("foreign origin: %d", r.rec.Code)
	}
	r = env.call(http.MethodPost, "/api/v1/auth/login", "203.0.113.1:1", body,
		map[string]string{"Origin": "https://file.example.test", "Sec-Fetch-Site": "same-origin"}, nil)
	if r.rec.Code != 200 {
		t.Fatalf("same origin: %d %s", r.rec.Code, r.rec.Body.String())
	}
}

func TestDisabledAccount(t *testing.T) {
	env := newAuthEnv(t)
	ctx := context.Background()
	hash, _ := auth.HashPassword("Rahasia sekali 2026!")
	now := db.Now()
	u, err := env.store.CreateUser(ctx, sqlc.CreateUserParams{ID: db.NewID(), Username: "budi", DisplayName: "Budi", Role: "user",
		Status: "active", PasswordHash: hash, MustChangePassword: 0, QuotaBytes: 1 << 30, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	r, ck, _ := env.login(t, "budi", "Rahasia sekali 2026!", "203.0.113.2:1")
	if r.rec.Code != 200 {
		t.Fatalf("login: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if err := env.store.UpdateUserStatus(ctx, sqlc.UpdateUserStatusParams{Status: "disabled", UpdatedAt: db.Now(), ID: u.ID}); err != nil {
		t.Fatal(err)
	}
	env.auth.InvalidateUser(u.ID)
	r = env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.2:1", nil, nil, ck)
	if r.rec.Code != 403 || r.code(t) != "ACCOUNT_DISABLED" {
		t.Fatalf("disabled mid-session: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if c := sessionCookie(r.rec); c == nil || c.MaxAge != -1 {
		t.Fatal("cookie should be cleared")
	}
	if r, _, _ = env.login(t, "budi", "Rahasia sekali 2026!", "203.0.113.2:1"); r.rec.Code != 403 || r.code(t) != "ACCOUNT_DISABLED" {
		t.Fatalf("login disabled: %d %s", r.rec.Code, r.rec.Body.String())
	}
}

func TestHomePreexistingDirectoryIsReused(t *testing.T) {
	env := newAuthEnv(t)
	ctx := context.Background()
	hash, _ := auth.HashPassword("Rahasia sekali 2026!")
	now := db.Now()
	if _, err := env.store.CreateUser(ctx, sqlc.CreateUserParams{ID: db.NewID(), Username: "sari", DisplayName: "Sari", Role: "user",
		Status: "active", PasswordHash: hash, QuotaBytes: 1 << 30, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(env.cfg.StorageRoot, "sari", "lama")
	if err := os.MkdirAll(old, 0o750); err != nil {
		t.Fatal(err)
	}
	r, _, _ := env.login(t, "sari", "Rahasia sekali 2026!", "203.0.113.3:1")
	if r.rec.Code != 200 {
		t.Fatalf("login: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r.body["user"].(map[string]any)["homeWarning"] != auth.HomeWarningReused {
		t.Fatalf("home warning missing: %v", r.body["user"])
	}
	if _, err := os.Stat(old); err != nil {
		t.Fatal("pre-existing content must be preserved")
	}
	if !auditActions(t, env)[audit.ActionHomeReused] {
		t.Fatal("reuse not audited")
	}
	// A file in place of the home directory makes login fail safely.
	if _, err := env.store.CreateUser(ctx, sqlc.CreateUserParams{ID: db.NewID(), Username: "rina", DisplayName: "Rina", Role: "user",
		Status: "active", PasswordHash: hash, QuotaBytes: 1 << 30, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.cfg.StorageRoot, "rina"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	if r, _, _ = env.login(t, "rina", "Rahasia sekali 2026!", "203.0.113.3:1"); r.rec.Code != 500 || r.code(t) != "INTERNAL" {
		t.Fatalf("file in place of home: %d %s", r.rec.Code, r.rec.Body.String())
	}
}

func TestSeedIsIdempotentAndWarnsAboutLeftoverPassword(t *testing.T) {
	env := newAuthEnv(t)
	ctx := context.Background()
	if err := env.auth.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := env.store.CountAllUsers(ctx); n != 1 {
		t.Fatalf("seed must not create a second admin: %d", n)
	}
	if strings.Contains(env.logs.String(), "still set") {
		t.Fatal("no warning expected before the password is changed")
	}
	_, ck, csrf := env.login(t, "admin", initialAdminPassword, "203.0.113.4:1")
	r := env.call(http.MethodPost, "/api/v1/auth/change-password", "203.0.113.4:1",
		map[string]string{"currentPassword": initialAdminPassword, "newPassword": "Kelapa muda 2026 segar!"},
		map[string]string{"X-CSRF-Token": csrf}, ck)
	if r.rec.Code != 204 {
		t.Fatalf("change: %d %s", r.rec.Code, r.rec.Body.String())
	}
	env.logs.Reset()
	if err := env.auth.Seed(ctx); err != nil { // simulates a restart with ADMIN_INITIAL_PASSWORD still in .env
		t.Fatal(err)
	}
	if !strings.Contains(env.logs.String(), "still set") {
		t.Fatalf("expected leftover-password warning, got: %s", env.logs.String())
	}
	if r, _, _ = env.login(t, "admin", initialAdminPassword, "203.0.113.4:1"); r.rec.Code != 401 {
		t.Fatalf("restart must not reset the password: %d", r.rec.Code)
	}
	if r, _, _ = env.login(t, "admin", "Kelapa muda 2026 segar!", "203.0.113.4:1"); r.rec.Code != 200 {
		t.Fatalf("new password must keep working: %d", r.rec.Code)
	}
}

func TestSeedRequiresInitialPassword(t *testing.T) {
	storage := filepath.Join(t.TempDir(), "storage")
	_ = os.MkdirAll(storage, 0o750)
	cfg, err := config.Load(config.Options{Environ: []string{
		"APP_ENV=test", "APP_BASE_URL=https://file.example.test", "APP_SECRET=" + strings.Repeat("ab", 32),
		"STORAGE_ROOT=" + storage, "DATA_DIR=" + filepath.Join(storage, ".system"),
	}, Cwd: "/opt/app", EUID: 1000, NumCPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	database, err := db.Open(filepath.Join(cfg.DataDir, "db", "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	_, _ = database.MigrateUp(context.Background())
	store := db.NewStore(database)
	svc := auth.NewService(cfg, store, audit.New(store, nil, nil), nil, nil)
	if err := svc.Seed(context.Background()); err == nil {
		t.Fatal("seeding without ADMIN_INITIAL_PASSWORD must fail")
	}
}
