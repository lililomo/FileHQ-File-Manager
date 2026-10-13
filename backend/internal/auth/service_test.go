package auth

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/config"
	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

const seedPassword = "IB+35t3U5+I_p5ZJcHLodfcr"

type svcEnv struct {
	svc   *Service
	store *db.Store
	cfg   *config.Config
	clock *fakeClock
	logs  *bytes.Buffer
}

func newSvcEnv(t *testing.T, extra ...string) *svcEnv {
	t.Helper()
	storage := filepath.Join(t.TempDir(), "storage")
	if err := os.MkdirAll(storage, 0o750); err != nil {
		t.Fatal(err)
	}
	environ := append([]string{
		"APP_ENV=test", "APP_BASE_URL=https://file.example.test", "APP_SECRET=" + strings.Repeat("ab", 32),
		"STORAGE_ROOT=" + storage, "DATA_DIR=" + filepath.Join(storage, ".system"),
		"ADMIN_USERNAME=admin", "ADMIN_INITIAL_PASSWORD=" + seedPassword,
	}, extra...)
	cfg, err := config.Load(config.Options{Environ: environ, Cwd: "/opt/app", EUID: 1000, NumCPU: 1})
	if err != nil {
		t.Fatal(err)
	}
	store := testStore(t)
	logs := &bytes.Buffer{}
	logger := config.NewLogger(logs, cfg.AppEnv, slog.LevelDebug)
	clock := newFakeClock()
	svc := NewService(cfg, store, audit.New(store, logger, clock.Now), logger, clock.Now)
	return &svcEnv{svc: svc, store: store, cfg: cfg, clock: clock, logs: logs}
}

func (e *svcEnv) createUser(t *testing.T, username, password string, params Params) sqlc.User {
	t.Helper()
	hash, err := HashPasswordWith(password, params)
	if err != nil {
		t.Fatal(err)
	}
	now := db.Now()
	u, err := e.store.CreateUser(context.Background(), sqlc.CreateUserParams{
		ID: db.NewID(), Username: username, DisplayName: username, Role: "user", Status: "active",
		PasswordHash: hash, QuotaBytes: 1 << 30, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestSeedService(t *testing.T) {
	ctx := context.Background()
	e := newSvcEnv(t)
	if err := e.svc.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	u, err := e.store.GetUserByUsername(ctx, "admin")
	if err != nil || u.Role != "admin" || u.MustChangePassword != 1 || u.QuotaBytes != e.cfg.DefaultUserQuota {
		t.Fatalf("seeded admin: %+v %v", u, err)
	}
	if _, err := e.store.GetSetting(ctx, SettingInitialPasswordHash); err != nil {
		t.Fatalf("initial password marker: %v", err)
	}
	if err := e.svc.Seed(ctx); err != nil { // idempotent
		t.Fatal(err)
	}
	if n, _ := e.store.CountAllUsers(ctx); n != 1 {
		t.Fatalf("users=%d", n)
	}
	if strings.Contains(e.logs.String(), seedPassword) {
		t.Fatal("seed logged the password")
	}

	// Too-short initial password is refused; a weak-but-long one is accepted with a warning.
	short := newSvcEnv(t, "ADMIN_INITIAL_PASSWORD=short")
	if err := short.svc.Seed(ctx); err == nil || !strings.Contains(err.Error(), "ADMIN_INITIAL_PASSWORD") {
		t.Fatalf("short initial password: %v", err)
	}
	weak := newSvcEnv(t, "ADMIN_INITIAL_PASSWORD=password12345")
	if err := weak.svc.Seed(ctx); err != nil || !strings.Contains(weak.logs.String(), "weak") {
		t.Fatalf("weak initial password: %v logs=%s", err, weak.logs.String())
	}
	missing := newSvcEnv(t, "ADMIN_INITIAL_PASSWORD=")
	if err := missing.svc.Seed(ctx); !errors.Is(err, ErrSeedPasswordUnset) {
		t.Fatalf("missing initial password: %v", err)
	}
	badName := newSvcEnv(t, "ADMIN_USERNAME=www")
	if err := badName.svc.Seed(ctx); !errors.Is(err, ErrUsernameForbidden) {
		t.Fatalf("forbidden admin username: %v", err)
	}
}

func TestLoginService(t *testing.T) {
	ctx := context.Background()
	e := newSvcEnv(t)
	if err := e.svc.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	in := LoginInput{Username: "admin", Password: seedPassword, IP: "10.1.1.1", UserAgent: "test"}

	if _, err := e.svc.Login(ctx, LoginInput{Username: "ghost", Password: "x", IP: "10.1.1.1"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("unknown user: %v", err)
	}
	if _, err := e.svc.Login(ctx, LoginInput{Username: "", Password: "x", IP: "10.1.1.1"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("empty user: %v", err)
	}
	res, err := e.svc.Login(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	if !res.Home.Created || res.CSRFToken == "" || res.User.LastLoginAt == nil || res.User.HomeProvisionedAt == nil {
		t.Fatalf("login result: %+v", res)
	}
	if !e.svc.VerifyCSRF(res.Session, res.CSRFToken) || e.svc.CSRF(res.Session) != res.CSRFToken {
		t.Fatal("csrf token mismatch")
	}
	sess, u, err := e.svc.Authenticate(ctx, res.Token)
	if err != nil || sess.ID != res.Session.ID || u.ID != res.User.ID {
		t.Fatalf("authenticate: %v", err)
	}
	if cached, err := e.svc.UserByID(ctx, u.ID); err != nil || cached.ID != u.ID {
		t.Fatalf("cache: %v", err)
	}
	res2, err := e.svc.Login(ctx, in)
	if err != nil || res2.Home.Created || res2.Home.Reused {
		t.Fatalf("second login should be steady state: %+v %v", res2.Home, err)
	}
	if err := e.svc.Logout(ctx, res.Session, res.User, "10.1.1.1", "test"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.Authenticate(ctx, res.Token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("after logout: %v", err)
	}
	if _, _, err := e.svc.Authenticate(ctx, "nonsense-token-nonsense-token-nonsense"); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("bad token: %v", err)
	}
}

func TestLoginRehashesOldParameters(t *testing.T) {
	ctx := context.Background()
	e := newSvcEnv(t)
	old := Params{Memory: 8 * 1024, Time: 2, Threads: 1, SaltLen: 16, KeyLen: 32} // differs from fastParams (t=1)
	u := e.createUser(t, "lama", "Kelapa muda 2026 segar!", old)
	if _, err := e.svc.Login(ctx, LoginInput{Username: "lama", Password: "Kelapa muda 2026 segar!", IP: "10.1.1.2"}); err != nil {
		t.Fatal(err)
	}
	after, _ := e.store.GetUserByID(ctx, u.ID)
	p, err := HashParams(after.PasswordHash)
	if err != nil || p.Time != DefaultParams.Time || after.PasswordHash == u.PasswordHash {
		t.Fatalf("hash not upgraded: %+v %v", p, err)
	}
	if ok, _, _ := VerifyPassword(after.PasswordHash, "Kelapa muda 2026 segar!", DefaultParams); !ok {
		t.Fatal("upgraded hash does not verify")
	}
}

func TestLoginFailuresLockoutDisabledAndBrokenHash(t *testing.T) {
	ctx := context.Background()
	e := newSvcEnv(t)
	u := e.createUser(t, "budi", "Rahasia sekali 2026!", fastParams)
	for i := 0; i < 4; i++ {
		if _, err := e.svc.Login(ctx, LoginInput{Username: "Budi", Password: "salah", IP: "10.1.1.3"}); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("failure %d: %v", i+1, err)
		}
		if row, _ := e.store.GetUserByID(ctx, u.ID); row.LockedUntil != nil {
			t.Fatalf("locked too early at failure %d", i+1)
		}
	}
	// 5th failure locks for 1 minute but still answers with the generic error.
	if _, err := e.svc.Login(ctx, LoginInput{Username: "budi", Password: "salah", IP: "10.1.1.3"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("5th failure: %v", err)
	}
	var locked *LockedError
	if _, err := e.svc.Login(ctx, LoginInput{Username: "budi", Password: "Rahasia sekali 2026!", IP: "10.1.1.3"}); !errors.As(err, &locked) || locked.RetryAfter <= 0 || locked.RetryAfter > time.Minute {
		t.Fatalf("locked login: %v", err)
	}
	e.clock.Advance(time.Minute + time.Second)
	// After expiry, more failures within the window re-lock immediately with the escalated tier.
	for i := 0; i < 5; i++ {
		_, _ = e.svc.Login(ctx, LoginInput{Username: "budi", Password: "salah", IP: "10.1.1.3"})
	}
	row, _ := e.store.GetUserByID(ctx, u.ID)
	if row.FailedLoginCount != 10 || row.LockedUntil == nil {
		t.Fatalf("escalation: %+v", row)
	}
	until, _ := db.ParseTime(*row.LockedUntil)
	if d := until.Sub(e.clock.Now()); d != 5*time.Minute {
		t.Fatalf("second lock duration=%v want 5m", d)
	}
	e.clock.Advance(6 * time.Minute)
	if _, err := e.svc.Login(ctx, LoginInput{Username: "budi", Password: "Rahasia sekali 2026!", IP: "10.1.1.3"}); err != nil {
		t.Fatalf("after second lock: %v", err)
	}
	row, _ = e.store.GetUserByID(ctx, u.ID)
	if row.FailedLoginCount != 0 || row.LockedUntil != nil {
		t.Fatal("success must reset counters")
	}

	// Disabled account.
	_ = e.store.UpdateUserStatus(ctx, sqlc.UpdateUserStatusParams{Status: "disabled", UpdatedAt: db.Now(), ID: u.ID})
	if _, err := e.svc.Login(ctx, LoginInput{Username: "budi", Password: "Rahasia sekali 2026!", IP: "10.1.1.3"}); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("disabled: %v", err)
	}

	// Unreadable stored hash never logs in and never panics.
	now := db.Now()
	_, _ = e.store.CreateUser(ctx, sqlc.CreateUserParams{ID: db.NewID(), Username: "rusak", DisplayName: "rusak", Role: "user", Status: "active",
		PasswordHash: "not-a-hash", QuotaBytes: 1, CreatedAt: now, UpdatedAt: now})
	if _, err := e.svc.Login(ctx, LoginInput{Username: "rusak", Password: "apa saja", IP: "10.1.1.3"}); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("broken hash: %v", err)
	}

	// IP rate limit.
	for i := 0; i < 25; i++ {
		_, err := e.svc.Login(ctx, LoginInput{Username: "nobody", Password: "x", IP: "10.9.9.9"})
		var limited *RateLimitedError
		if i < 20 && errors.As(err, &limited) {
			t.Fatalf("limited too early at %d", i+1)
		}
		if i >= 20 && !errors.As(err, &limited) {
			t.Fatalf("not limited at %d: %v", i+1, err)
		}
	}
}

func TestLoginProvisioningErrorFailsClosed(t *testing.T) {
	ctx := context.Background()
	e := newSvcEnv(t)
	e.createUser(t, "berkas", "Rahasia sekali 2026!", fastParams)
	if err := os.WriteFile(filepath.Join(e.cfg.StorageRoot, "berkas"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Login(ctx, LoginInput{Username: "berkas", Password: "Rahasia sekali 2026!", IP: "10.1.1.4"}); !errors.Is(err, ErrHomeNotDir) {
		t.Fatalf("expected ErrHomeNotDir, got %v", err)
	}
	if n, _ := e.store.CountActiveSessions(ctx, db.Now()); n != 0 {
		t.Fatal("no session may be created when provisioning fails")
	}
}

func TestChangePasswordService(t *testing.T) {
	ctx := context.Background()
	e := newSvcEnv(t)
	if err := e.svc.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := e.svc.Login(ctx, LoginInput{Username: "admin", Password: seedPassword, IP: "10.1.1.5"})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := e.svc.Login(ctx, LoginInput{Username: "admin", Password: seedPassword, IP: "10.1.1.6"})
	u := res.User
	check := func(current, next, wantReason string, wantErr error) {
		t.Helper()
		err := e.svc.ChangePassword(ctx, u, res.Session, current, next, "10.1.1.5", "test")
		var pe *PolicyError
		switch {
		case wantReason != "":
			if !errors.As(err, &pe) || pe.Reason != wantReason {
				t.Fatalf("%q→%q: want reason %s got %v", current, next, wantReason, err)
			}
		case wantErr != nil:
			if !errors.Is(err, wantErr) {
				t.Fatalf("%q→%q: want %v got %v", current, next, wantErr, err)
			}
		default:
			if err != nil {
				t.Fatalf("%q→%q: %v", current, next, err)
			}
		}
	}
	check("wrong", "Kelapa muda 2026 segar!", "", ErrInvalidCredentials)
	check(seedPassword, seedPassword, ReasonSameAsInitial, nil)
	check(seedPassword, "short", ReasonTooShort, nil)
	check(seedPassword, "Kelapa muda 2026 segar!", "", nil)
	u, _ = e.store.GetUserByID(ctx, u.ID)
	if u.MustChangePassword != 0 || u.PasswordChangedAt == nil {
		t.Fatalf("flags after change: %+v", u)
	}
	if _, _, err := e.svc.Authenticate(ctx, other.Token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("other session must be revoked: %v", err)
	}
	if _, _, err := e.svc.Authenticate(ctx, res.Token); err != nil {
		t.Fatalf("current session must survive: %v", err)
	}
	if _, err := e.store.GetSetting(ctx, SettingInitialPasswordHash); !db.IsNotFound(err) {
		t.Fatalf("initial marker should be gone: %v", err)
	}
	check("Kelapa muda 2026 segar!", "Kelapa muda 2026 segar!", ReasonSameAsCurrent, nil)
	// Rate limit: 5 verified attempts per 15 minutes per session. Policy
	// failures do not count; the wrong-current attempt and the success did
	// (2), so three more wrong guesses exhaust the budget.
	for i := 0; i < 3; i++ {
		check("wrong-guess", "Durian runtuh 2026!", "", ErrInvalidCredentials)
	}
	var limited *RateLimitedError
	if err := e.svc.ChangePassword(ctx, u, res.Session, "Kelapa muda 2026 segar!", "Durian runtuh 2026!", "10.1.1.5", "test"); !errors.As(err, &limited) {
		t.Fatalf("expected rate limit, got %v", err)
	}
	e.clock.Advance(16 * time.Minute)
	if err := e.svc.ChangePassword(ctx, u, res.Session, "Kelapa muda 2026 segar!", "Durian runtuh 2026!", "10.1.1.5", "test"); err != nil {
		t.Fatalf("after window: %v", err)
	}
	if err := e.svc.Seed(ctx); err != nil || !strings.Contains(e.logs.String(), "still set") {
		t.Fatalf("leftover warning: %v", err)
	}
}

func TestAuthenticateRevokesDisabledUser(t *testing.T) {
	ctx := context.Background()
	e := newSvcEnv(t)
	u := e.createUser(t, "sari", "Rahasia sekali 2026!", fastParams)
	res, err := e.svc.Login(ctx, LoginInput{Username: "sari", Password: "Rahasia sekali 2026!", IP: "10.1.1.7"})
	if err != nil {
		t.Fatal(err)
	}
	_ = e.store.UpdateUserStatus(ctx, sqlc.UpdateUserStatusParams{Status: "disabled", UpdatedAt: db.Now(), ID: u.ID})
	e.svc.InvalidateUser(u.ID)
	if _, _, err := e.svc.Authenticate(ctx, res.Token); !errors.Is(err, ErrAccountDisabled) {
		t.Fatalf("disabled: %v", err)
	}
	if _, _, err := e.svc.Authenticate(ctx, res.Token); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("session should have been revoked: %v", err)
	}
	// Cache: without invalidation the stale (active) user is served for up to 5 s.
	_ = e.store.UpdateUserStatus(ctx, sqlc.UpdateUserStatusParams{Status: "active", UpdatedAt: db.Now(), ID: u.ID})
	e.svc.InvalidateUser(u.ID)
	got, _ := e.svc.UserByID(ctx, u.ID)
	_ = e.store.UpdateUserStatus(ctx, sqlc.UpdateUserStatusParams{Status: "disabled", UpdatedAt: db.Now(), ID: u.ID})
	if again, _ := e.svc.UserByID(ctx, u.ID); again.Status != got.Status {
		t.Fatal("expected cached user within TTL")
	}
	e.clock.Advance(6 * time.Second)
	if fresh, _ := e.svc.UserByID(ctx, u.ID); fresh.Status != "disabled" {
		t.Fatal("cache should expire after 5 s")
	}
}

func TestRunMaintenanceStops(t *testing.T) {
	e := newSvcEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.svc.RunMaintenance(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("maintenance loop did not stop")
	}
}
