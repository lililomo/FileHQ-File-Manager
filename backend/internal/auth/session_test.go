package auth

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

func testStore(t *testing.T) *db.Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := d.MigrateUp(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db.NewStore(d)
}

func createUser(t *testing.T, store *db.Store, username string) sqlc.User {
	t.Helper()
	now := db.Now()
	u, err := store.CreateUser(context.Background(), sqlc.CreateUserParams{
		ID: db.NewID(), Username: username, DisplayName: username, Role: "user", Status: "active",
		PasswordHash:       "$argon2id$v=19$m=8192,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		MustChangePassword: 0, QuotaBytes: 1 << 30, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func TestSessionLifecycle(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	u := createUser(t, store, "budi")
	clock := newFakeClock()
	s := NewSessions(store, 2*time.Hour, 168*time.Hour, clock.Now)

	token, sess, err := s.Create(ctx, u.ID, "10.0.0.5", "curl/8.5.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(token) != 43 || sess.ID != HashToken(token) || len(sess.CsrfSecret) != 32 {
		t.Fatalf("token/session shape: len=%d id=%s", len(token), sess.ID)
	}
	if sess.DeviceLabel == nil || *sess.DeviceLabel != "curl · Unknown" {
		t.Fatalf("device label: %v", sess.DeviceLabel)
	}

	got, err := s.Lookup(ctx, token)
	if err != nil || got.ID != sess.ID {
		t.Fatalf("lookup: %v", err)
	}
	if got.LastSeenAt != sess.LastSeenAt {
		t.Fatal("last_seen must not be touched within 5 minutes")
	}

	clock.Advance(6 * time.Minute)
	got, err = s.Lookup(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastSeenAt == sess.LastSeenAt || got.IdleExpiresAt == sess.IdleExpiresAt {
		t.Fatal("sliding refresh expected after 5 minutes")
	}
	wantIdle := db.FormatTime(clock.Now().Add(2 * time.Hour))
	if got.IdleExpiresAt != wantIdle {
		t.Fatalf("idle_expires_at=%s want %s", got.IdleExpiresAt, wantIdle)
	}

	// Idle timeout: 2 h without activity.
	clock.Advance(2*time.Hour + time.Second)
	if _, err := s.Lookup(ctx, token); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("idle expiry: %v", err)
	}

	// Absolute timeout caps the idle deadline.
	token2, _, err := s.Create(ctx, u.ID, "10.0.0.5", "")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 170; i++ { // keep touching every hour
		clock.Advance(time.Hour)
		if _, err := s.Lookup(ctx, token2); err != nil {
			if i < 167 {
				t.Fatalf("hour %d: %v", i, err)
			}
			if !errors.Is(err, ErrSessionExpired) {
				t.Fatalf("absolute expiry: %v", err)
			}
			break
		}
	}

	// Revoke.
	token3, sess3, _ := s.Create(ctx, u.ID, "10.0.0.5", "")
	if err := s.Revoke(ctx, sess3.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(ctx, token3); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("revoked: %v", err)
	}
	if _, err := s.Lookup(ctx, "garbage"); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("garbage token: %v", err)
	}
	if _, err := s.Lookup(ctx, token+"x"); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("unknown token: %v", err)
	}

	// RevokeAllExcept keeps the current one.
	tk1, s1, _ := s.Create(ctx, u.ID, "10.0.0.5", "")
	tk2, _, _ := s.Create(ctx, u.ID, "10.0.0.6", "")
	if _, err := s.RevokeAllExcept(ctx, u.ID, s1.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Lookup(ctx, tk1); err != nil {
		t.Fatalf("current session revoked: %v", err)
	}
	if _, err := s.Lookup(ctx, tk2); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("other session still valid: %v", err)
	}

	// Cleanup removes expired/revoked rows.
	n, err := s.Cleanup(ctx)
	if err != nil || n == 0 {
		t.Fatalf("cleanup: n=%d err=%v", n, err)
	}
}

func TestMaxSessionsPerUser(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	u := createUser(t, store, "sari")
	clock := newFakeClock()
	s := NewSessions(store, 2*time.Hour, 168*time.Hour, clock.Now)
	tokens := make([]string, 0, MaxSessionsPerUser+1)
	for i := 0; i <= MaxSessionsPerUser; i++ {
		clock.Advance(time.Second)
		tk, _, err := s.Create(ctx, u.ID, "10.0.0.1", "")
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, tk)
	}
	if _, err := s.Lookup(ctx, tokens[0]); !errors.Is(err, ErrSessionInvalid) {
		t.Fatalf("oldest session should be revoked: %v", err)
	}
	for _, tk := range tokens[1:] {
		if _, err := s.Lookup(ctx, tk); err != nil {
			t.Fatalf("recent session invalid: %v", err)
		}
	}
	active, _ := store.ListUserSessions(ctx, u.ID, db.FormatTime(clock.Now()))
	if len(active) != MaxSessionsPerUser {
		t.Fatalf("active sessions=%d want %d", len(active), MaxSessionsPerUser)
	}
}

func TestCookieFlags(t *testing.T) {
	s := NewSessions(nil, 2*time.Hour, 168*time.Hour, nil)
	c := s.Cookie("tok", true)
	if c.Name != CookieName || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge != 168*3600 {
		t.Fatalf("cookie flags: %+v", c)
	}
	if s.Cookie("tok", false).Secure {
		t.Fatal("development cookie must not be Secure")
	}
	cl := ClearCookie(true)
	if cl.MaxAge != -1 || cl.Value != "" || !cl.HttpOnly {
		t.Fatalf("clear cookie: %+v", cl)
	}
}
