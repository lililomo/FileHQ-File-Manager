package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
)

func TestErrorStringsAndDefaults(t *testing.T) {
	if (&LockedError{RetryAfter: time.Minute}).Error() == "" || (&RateLimitedError{}).Error() == "" || (&PolicyError{Reason: "x"}).Error() == "" {
		t.Fatal("error strings")
	}
	if NewRateLimiter(1, time.Second, nil).now == nil || NewWindowLimiter(1, time.Second, nil).now == nil {
		t.Fatal("nil clock must default to time.Now")
	}
	if _, err := HashParams("garbage"); !errors.Is(err, ErrInvalidHash) {
		t.Fatalf("HashParams garbage: %v", err)
	}
	h, err := HashPassword("Kelapa muda 2026 segar!")
	if err != nil || !strings.HasPrefix(h, "$argon2id$") {
		t.Fatalf("HashPassword: %v", err)
	}
	if truncate("abc", 2) != "ab" || truncate("abc", 5) != "abc" {
		t.Fatal("truncate")
	}
}

func TestDeviceLabelMore(t *testing.T) {
	cases := map[string]string{
		"Mozilla/5.0 (Windows NT 10.0) Chrome/130.0 Safari/537.36 Edg/130.0":                "Edge · Windows",
		"Mozilla/5.0 (Linux; Android 14) Chrome/130.0 Mobile Safari/537.36 OPR/90.0":        "Opera · Android",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Version/17.0 Safari/604.1":  "Safari · iOS",
		"Mozilla/5.0 (X11; CrOS x86_64 14541.0.0) Chrome/130.0 Safari/537.36":               "Chrome · ChromeOS",
		"Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) Version/17.0 Mobile/15E148 Safari/1": "Safari · iOS",
		"SomethingElse/1.0": "Browser · Unknown",
	}
	for ua, want := range cases {
		if got := DeviceLabel(ua); got != want {
			t.Errorf("DeviceLabel(%q)=%q want %q", ua, got, want)
		}
	}
}

func TestSessionCreateTruncatesUserAgent(t *testing.T) {
	store := testStore(t)
	u := createUser(t, store, "ua")
	s := NewSessions(store, time.Hour, 2*time.Hour, nil)
	_, sess, err := s.Create(context.Background(), u.ID, "10.0.0.1", strings.Repeat("x", 2000))
	if err != nil || len(sess.UserAgent) != MaxUserAgentLen {
		t.Fatalf("user agent not truncated: %d %v", len(sess.UserAgent), err)
	}
}

func TestRunCleanupLoop(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	u := createUser(t, store, "clean")
	clock := newFakeClock()
	s := NewSessions(store, time.Minute, time.Hour, clock.Now)
	token, _, err := s.Create(ctx, u.ID, "10.0.0.1", "")
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(2 * time.Hour) // both idle and absolute expired
	if _, err := s.Lookup(ctx, token); !errors.Is(err, ErrSessionExpired) {
		t.Fatalf("expected expiry: %v", err)
	}
	loopCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { s.RunCleanup(loopCtx, discardLogger(), 10*time.Millisecond); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := store.GetSession(ctx, HashToken(token)); db.IsNotFound(err) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if _, err := store.GetSession(ctx, HashToken(token)); !db.IsNotFound(err) {
		t.Fatalf("expired session not cleaned: %v", err)
	}
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
