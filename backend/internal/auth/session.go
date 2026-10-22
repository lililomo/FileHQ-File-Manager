package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Session constants (SPEC §8.1, §10.4).
const (
	CookieName         = "fm_session"
	tokenBytes         = 32
	csrfSecretBytes    = 32
	touchInterval      = 5 * time.Minute
	MaxSessionsPerUser = 20
	CleanupInterval    = 10 * time.Minute
	MaxUserAgentLen    = 256 // sessions.user_agent, SPEC §4.2
)

// Session errors.
var (
	ErrSessionInvalid = errors.New("auth: session invalid")
	ErrSessionExpired = errors.New("auth: session expired")
)

// Sessions creates, validates and revokes server-side sessions.
type Sessions struct {
	repo     db.Repository
	idle     time.Duration
	absolute time.Duration
	now      func() time.Time
	// Live, when set, supplies the timeouts from the admin settings
	// snapshot (Step 19a) instead of the static values.
	Live func() (idle, absolute time.Duration)
}

func (s *Sessions) timeouts() (idle, absolute time.Duration) {
	if s.Live != nil {
		if i, a := s.Live(); i > 0 && a > 0 {
			return i, a
		}
	}
	return s.idle, s.absolute
}

// NewSessions wires the session store. now is injectable for tests.
func NewSessions(repo db.Repository, idle, absolute time.Duration, now func() time.Time) *Sessions {
	if now == nil {
		now = time.Now
	}
	return &Sessions{repo: repo, idle: idle, absolute: absolute, now: now}
}

// HashToken maps the cookie value to the stored session id (SHA-256 hex).
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Create issues a new session for userID and returns the cookie token. If the
// user already has MaxSessionsPerUser active sessions the oldest are revoked.
func (s *Sessions) Create(ctx context.Context, userID, ip, userAgent string) (string, sqlc.Session, error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", sqlc.Session{}, fmt.Errorf("auth: token: %w", err)
	}
	secret := make([]byte, csrfSecretBytes)
	if _, err := rand.Read(secret); err != nil {
		return "", sqlc.Session{}, fmt.Errorf("auth: csrf secret: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := s.now()
	nowStr := db.FormatTime(now)

	active, err := s.repo.ListUserSessions(ctx, userID, nowStr)
	if err != nil {
		return "", sqlc.Session{}, err
	}
	if len(active) >= MaxSessionsPerUser { // newest first → revoke the tail
		for _, old := range active[MaxSessionsPerUser-1:] {
			if _, err := s.repo.RevokeSession(ctx, old.ID, nowStr); err != nil {
				return "", sqlc.Session{}, err
			}
		}
	}
	label := DeviceLabel(userAgent)
	_, absolute := s.timeouts()
	sess, err := s.repo.CreateSession(ctx, sqlc.CreateSessionParams{
		ID:                HashToken(token),
		UserID:            userID,
		CsrfSecret:        secret,
		CreatedAt:         nowStr,
		LastSeenAt:        nowStr,
		IdleExpiresAt:     db.FormatTime(s.idleDeadline(now, now.Add(absolute))),
		AbsoluteExpiresAt: db.FormatTime(now.Add(absolute)),
		Ip:                ip,
		UserAgent:         truncate(userAgent, MaxUserAgentLen),
		DeviceLabel:       &label,
	})
	if err != nil {
		return "", sqlc.Session{}, err
	}
	return token, sess, nil
}

func (s *Sessions) idleDeadline(now, absolute time.Time) time.Time {
	idle, _ := s.timeouts()
	d := now.Add(idle)
	if d.After(absolute) {
		return absolute
	}
	return d
}

// Lookup validates a cookie token: the row must exist, not be revoked and be
// within both the idle and absolute windows. last_seen/idle_expires are
// refreshed at most once per touchInterval (sliding expiry).
func (s *Sessions) Lookup(ctx context.Context, token string) (sqlc.Session, error) {
	if len(token) < 32 || len(token) > 128 {
		return sqlc.Session{}, ErrSessionInvalid
	}
	sess, err := s.repo.GetSession(ctx, HashToken(token))
	if err != nil {
		if db.IsNotFound(err) {
			return sqlc.Session{}, ErrSessionInvalid
		}
		return sqlc.Session{}, err
	}
	if sess.RevokedAt != nil {
		return sqlc.Session{}, ErrSessionInvalid
	}
	now := s.now()
	abs, err1 := db.ParseTime(sess.AbsoluteExpiresAt)
	idle, err2 := db.ParseTime(sess.IdleExpiresAt)
	last, err3 := db.ParseTime(sess.LastSeenAt)
	if err1 != nil || err2 != nil || err3 != nil {
		return sqlc.Session{}, ErrSessionInvalid
	}
	if !now.Before(abs) || !now.Before(idle) {
		return sqlc.Session{}, ErrSessionExpired
	}
	if now.Sub(last) >= touchInterval {
		newIdle := s.idleDeadline(now, abs)
		if err := s.repo.TouchSession(ctx, sqlc.TouchSessionParams{
			LastSeenAt: db.FormatTime(now), IdleExpiresAt: db.FormatTime(newIdle), ID: sess.ID,
		}); err != nil {
			return sqlc.Session{}, err
		}
		sess.LastSeenAt = db.FormatTime(now)
		sess.IdleExpiresAt = db.FormatTime(newIdle)
	}
	return sess, nil
}

// Revoke marks one session as revoked.
func (s *Sessions) Revoke(ctx context.Context, id string) error {
	_, err := s.repo.RevokeSession(ctx, id, db.FormatTime(s.now()))
	return err
}

// RevokeAllExcept revokes every active session of userID except exceptID
// ("" = all).
func (s *Sessions) RevokeAllExcept(ctx context.Context, userID, exceptID string) (int64, error) {
	return s.repo.RevokeUserSessions(ctx, userID, exceptID, db.FormatTime(s.now()))
}

// Cleanup deletes expired and long-revoked rows; returns rows removed.
func (s *Sessions) Cleanup(ctx context.Context) (int64, error) {
	return s.repo.DeleteExpiredSessions(ctx, db.FormatTime(s.now()))
}

// RunCleanup deletes expired sessions and old login attempts every interval
// until ctx is done.
func (s *Sessions) RunCleanup(ctx context.Context, logger *slog.Logger, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := s.Cleanup(ctx); err != nil {
				logger.Warn("session cleanup failed", slog.String("error", err.Error()))
			} else if n > 0 {
				logger.Debug("sessions cleaned", slog.Int64("removed", n))
			}
			cutoff := db.FormatTime(s.now().Add(-30 * 24 * time.Hour))
			if _, err := s.repo.DeleteLoginAttemptsOlderThan(ctx, cutoff); err != nil {
				logger.Warn("login attempt cleanup failed", slog.String("error", err.Error()))
			}
		}
	}
}

// Cookie builds the session cookie. Secure is dropped only in development
// (SPEC §8.1); Max-Age equals the absolute timeout.
func (s *Sessions) Cookie(token string, secure bool) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // Secure is a parameter: true except APP_ENV=development (SPEC §8.1)
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(s.maxAge().Seconds()),
	}
}

func (s *Sessions) maxAge() time.Duration {
	_, absolute := s.timeouts()
	return absolute
}

// ClearCookie expires the session cookie.
func ClearCookie(secure bool) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // see Cookie
		Name: CookieName, Value: "", Path: "/", HttpOnly: true, Secure: secure,
		SameSite: http.SameSiteLaxMode, MaxAge: -1,
	}
}

// DeviceLabel derives a short "Browser · OS" label from a User-Agent for the
// sessions list (SPEC §6.3 /me/sessions).
func DeviceLabel(ua string) string {
	browser := "Browser"
	switch {
	case strings.Contains(ua, "Edg/"):
		browser = "Edge"
	case strings.Contains(ua, "OPR/"):
		browser = "Opera"
	case strings.Contains(ua, "Firefox/"):
		browser = "Firefox"
	case strings.Contains(ua, "Chrome/"):
		browser = "Chrome"
	case strings.Contains(ua, "Safari/"):
		browser = "Safari"
	case strings.HasPrefix(ua, "curl/"):
		browser = "curl"
	case ua == "":
		browser = "Unknown"
	}
	osName := "Unknown"
	switch {
	case strings.Contains(ua, "Windows"):
		osName = "Windows"
	case strings.Contains(ua, "Android"):
		osName = "Android"
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"):
		osName = "iOS"
	case strings.Contains(ua, "Mac OS X"):
		osName = "macOS"
	case strings.Contains(ua, "CrOS"):
		osName = "ChromeOS"
	case strings.Contains(ua, "Linux"):
		osName = "Linux"
	}
	return browser + " · " + osName
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
