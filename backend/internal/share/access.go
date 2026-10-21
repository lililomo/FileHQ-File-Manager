package share

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"kppbwi.app/filemanager/internal/auth"
	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Rate limits of the public endpoints (SPEC §10.5).
const (
	UnlockPerLinkIP   = 10 // per 10 minutes
	UnlockPerIP       = 30 // per 10 minutes
	NotFoundPerIP     = 30 // per minute
	TrafficPerLinkIP  = 120
	FailsBeforeLock   = 50 // wrong passwords per hour per link
	TempLockDuration  = time.Hour
	MaxUserAgentChars = 200
	viewDedupeTTL     = 24 * time.Hour
)

// Access-log actions (SPEC §4.2 share_access_logs.action).
const (
	ActView       = "view"
	ActList       = "list"
	ActDownload   = "download"
	ActZip        = "zip"
	ActUnlockOK   = "unlock_ok"
	ActUnlockFail = "unlock_fail"
	ActUpload     = "upload"
	ActDenied     = "denied"
)

type guards struct {
	unlockLinkIP *auth.RateLimiter
	unlockIP     *auth.RateLimiter
	notFound     *auth.RateLimiter
	traffic      *auth.RateLimiter
	fails        *auth.WindowLimiter
	now          func() time.Time

	mu       sync.Mutex
	locked   map[string]time.Time // share id → temporary lock end
	viewed   map[string]time.Time // pv|share → first view
	lastSwep time.Time
}

func newGuards(now func() time.Time) *guards {
	return &guards{
		unlockLinkIP: auth.NewRateLimiter(UnlockPerLinkIP, 10*time.Minute, now),
		unlockIP:     auth.NewRateLimiter(UnlockPerIP, 10*time.Minute, now),
		notFound:     auth.NewRateLimiter(NotFoundPerIP, time.Minute, now),
		traffic:      auth.NewRateLimiter(TrafficPerLinkIP, time.Minute, now),
		fails:        auth.NewWindowLimiter(FailsBeforeLock, time.Hour, now),
		now:          now,
		locked:       map[string]time.Time{},
		viewed:       map[string]time.Time{},
	}
}

func (g *guards) lockedUntil(shareID string) (time.Time, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	t, ok := g.locked[shareID]
	if !ok {
		return t, false
	}
	if !g.now().Before(t) {
		delete(g.locked, shareID)
		return t, false
	}
	return t, true
}

func (g *guards) lock(shareID string) time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()
	until := g.now().Add(TempLockDuration)
	g.locked[shareID] = until
	return until
}

// firstView reports whether (pv, share) has not been counted in the last 24 h.
func (g *guards) firstView(pv, shareID string) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if now.Sub(g.lastSwep) > time.Hour {
		for k, t := range g.viewed {
			if now.Sub(t) > viewDedupeTTL {
				delete(g.viewed, k)
			}
		}
		g.lastSwep = now
	}
	k := pv + "|" + shareID
	if t, ok := g.viewed[k]; ok && now.Sub(t) <= viewDedupeTTL {
		return false
	}
	g.viewed[k] = now
	return true
}

func rateLimited(wait time.Duration, reason string) *Error {
	secs := int(wait.Seconds()) + 1
	return &Error{Code: "RATE_LIMITED", Status: http.StatusTooManyRequests, Message: "too many requests",
		Details: map[string]any{"reason": reason, "retryAfter": secs}}
}

// Resolve finds the public share behind {token} (a 43-char token or a
// slug). Misses are rate limited per IP (anti-enumeration); a users-mode
// share is never reachable here.
func (s *Service) Resolve(ctx context.Context, token, ip string) (sqlc.Share, *Error) {
	var row sqlc.Share
	var err error
	if IsToken(token) {
		row, err = s.repo.GetShareByTokenHash(ctx, HashToken(token))
	} else if _, ok := ValidateSlug(token); ok {
		row, err = s.repo.GetShareBySlug(ctx, token)
	} else {
		err = sql.ErrNoRows
	}
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			s.logger.ErrorContext(ctx, "share lookup failed", slog.String("error", err.Error()))
			return row, newErr("INTERNAL", http.StatusInternalServerError, "internal server error", nil)
		}
		if ok, wait := s.guards.notFound.Allow(ip); !ok {
			return row, rateLimited(wait, "share_lookup")
		}
		return row, errNotFound
	}
	if row.Mode != ModePublic {
		return row, errNotFound
	}
	return row, nil
}

// Gate applies the access order of SPEC §6.6: disabled → owner inactive →
// temporary lock → broken → expired → password.
func (s *Service) Gate(row sqlc.Share, owner sqlc.User, unlocked bool) *Error {
	if row.DisabledAt != nil {
		reason := ReasonManual
		if row.DisabledReason != nil {
			reason = *row.DisabledReason
		}
		return newErr("SHARE_DISABLED", http.StatusGone, "this link has been disabled", map[string]any{"reason": reason})
	}
	if owner.Status != "active" || owner.DeletedAt != nil {
		return newErr("SHARE_DISABLED", http.StatusGone, "this link has been disabled", map[string]any{"reason": ReasonOwnerDisabled})
	}
	if until, locked := s.guards.lockedUntil(row.ID); locked {
		return newErr("SHARE_DISABLED", http.StatusGone, "this link is temporarily locked", map[string]any{"reason": "locked", "until": db.FormatTime(until)})
	}
	if row.BrokenAt != nil {
		return newErr("SHARE_DISABLED", http.StatusGone, "the shared item is no longer available", map[string]any{"reason": "broken"})
	}
	if s.expired(row) {
		return newErr("SHARE_EXPIRED", http.StatusGone, "this link has expired", map[string]any{"expiresAt": *row.ExpiresAt})
	}
	if row.PasswordHash != nil && *row.PasswordHash != "" && !unlocked {
		return newErr("SHARE_PASSWORD_REQUIRED", http.StatusUnauthorized, "this link needs a password", nil)
	}
	return nil
}

// HasPassword reports whether the share is password protected.
func HasPassword(row sqlc.Share) bool { return row.PasswordHash != nil && *row.PasswordHash != "" }

// Unlock verifies a password (SPEC §10.5 limits: 10 / 10 min per link+IP,
// 30 / 10 min per IP; 50 failures / h lock the link for an hour and notify
// the owner). Returns the cookie value on success.
func (s *Service) Unlock(ctx context.Context, row sqlc.Share, owner sqlc.User, password, ip, ua string) (cookie string, exp time.Time, err *Error) {
	if !HasPassword(row) {
		return "", time.Time{}, fieldErr("password", "not_required", "this link has no password")
	}
	if ok, wait := s.guards.unlockIP.Allow(ip); !ok {
		return "", time.Time{}, rateLimited(wait, "unlock_ip")
	}
	if ok, wait := s.guards.unlockLinkIP.Allow(row.ID + "|" + ip); !ok {
		return "", time.Time{}, rateLimited(wait, "unlock_link")
	}
	ok, _, verr := auth.VerifyPassword(*row.PasswordHash, password, auth.ShareParams)
	if verr != nil || !ok {
		s.Log(ctx, row.ID, ActUnlockFail, ip, ua, nil, nil)
		if allowed, _ := s.guards.fails.Allow(row.ID); !allowed {
			if _, already := s.guards.lockedUntil(row.ID); !already {
				until := s.guards.lock(row.ID)
				s.logger.WarnContext(ctx, "share temporarily locked after repeated wrong passwords", slog.String("share", row.ID), slog.String("until", db.FormatTime(until)))
				if s.notify != nil {
					s.notify(ctx, owner.ID, "system", "Link dibagikan dikunci sementara",
						fmt.Sprintf("Terlalu banyak percobaan password salah pada link %q; link dikunci selama 1 jam.", s.View(row).Name), "/shares")
				}
			}
		}
		return "", time.Time{}, newErr("SHARE_PASSWORD_WRONG", http.StatusUnauthorized, "wrong password", nil)
	}
	exp = s.now().Add(CookieTTL)
	s.Log(ctx, row.ID, ActUnlockOK, ip, ua, nil, nil)
	return s.keys.CookieValue(row.ID, *row.PasswordHash, exp), exp, nil
}

// Unlocked reports whether a cookie value opens the share now.
func (s *Service) Unlocked(row sqlc.Share, cookie string) bool {
	if !HasPassword(row) {
		return true
	}
	if cookie == "" {
		return false
	}
	return s.keys.VerifyCookie(cookie, row.ID, *row.PasswordHash, s.now())
}

// AllowTraffic enforces 120 requests / minute per (IP, link) on listing,
// raw, download, thumb and content.
func (s *Service) AllowTraffic(ip, shareID string) *Error {
	if ok, wait := s.guards.traffic.Allow(ip + "|" + shareID); !ok {
		return rateLimited(wait, "share_traffic")
	}
	return nil
}

// CountDownload increments download_count unless max_downloads is reached.
func (s *Service) CountDownload(ctx context.Context, row sqlc.Share) *Error {
	n, err := s.repo.IncrementShareDownload(ctx, row.ID, db.FormatTime(s.now()))
	if err != nil {
		s.logger.ErrorContext(ctx, "share download count failed", slog.String("share", row.ID), slog.String("error", err.Error()))
		return nil
	}
	if n == 0 {
		return newErr("SHARE_LIMIT_REACHED", http.StatusGone, "the download limit of this link has been reached", map[string]any{"maxDownloads": row.MaxDownloads})
	}
	return nil
}

// LimitReached reports whether downloads are exhausted (meta/page state).
func LimitReached(row sqlc.Share) bool {
	return row.MaxDownloads != nil && row.DownloadCount >= *row.MaxDownloads
}

// RecordView counts one view per anonymous visitor per 24 h (cookie fm_pv).
func (s *Service) RecordView(ctx context.Context, shareID, pv, ip, ua string) {
	if pv != "" && !s.guards.firstView(pv, shareID) {
		return
	}
	if err := s.repo.IncrementShareView(ctx, shareID, db.FormatTime(s.now())); err != nil {
		s.logger.WarnContext(ctx, "share view count failed", slog.String("share", shareID), slog.String("error", err.Error()))
	}
	s.Log(ctx, shareID, ActView, ip, ua, nil, nil)
}

// Touch updates last_access_at.
func (s *Service) Touch(ctx context.Context, shareID string) {
	_ = s.repo.TouchShareAccess(ctx, shareID, db.FormatTime(s.now()))
}

// Log appends an access-log row (IP truncated, user agent clipped).
func (s *Service) Log(ctx context.Context, shareID, action, ip, ua string, path *string, userID *string) {
	if len(ua) > MaxUserAgentChars {
		ua = ua[:MaxUserAgentChars]
	}
	err := s.repo.InsertShareAccessLog(ctx, sqlc.InsertShareAccessLogParams{
		ShareID: shareID, At: db.FormatTime(s.now()), IpTrunc: TruncateIP(ip), Action: action, UserID: userID, UserAgent: ua, Path: path,
	})
	if err != nil {
		s.logger.WarnContext(ctx, "share access log failed", slog.String("share", shareID), slog.String("error", err.Error()))
	}
}

// PruneAccessLogs deletes rows older than retention (prune_logs job).
func (s *Service) PruneAccessLogs(ctx context.Context, retention time.Duration) (int64, error) {
	return s.repo.DeleteShareAccessLogsBefore(ctx, db.FormatTime(s.now().Add(-retention)))
}

// RecordUpload is wired into upload.Options.OnShareUpload: access log,
// last access and the owner's share_upload notification (SPEC §8.16).
// The request context has ended with the tus PATCH, hence Background.
func (s *Service) RecordUpload(shareID, ownerID, ip, ua, dest, path string, size int64) {
	ctx := context.Background()
	rel := strings.TrimPrefix(path, strings.TrimSuffix(dest, "/"))
	s.Log(ctx, shareID, ActUpload, ip, ua, &rel, nil)
	s.Touch(ctx, shareID)
	if s.notify != nil {
		name := dest
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		s.notify(ctx, ownerID, "share_upload", "Unggahan baru lewat file request",
			fmt.Sprintf("Seseorang mengunggah %s (%s) ke folder %q.", strings.TrimPrefix(rel, "/"), humanBytes(size), name), "/files"+dest)
	}
}

// humanBytes formats a size for notification text.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit && exp < 4; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %s", float64(n)/float64(div), []string{"KB", "MB", "GB", "TB", "PB"}[exp])
}
