package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"kppbwi.app/filemanager/internal/auth"
	"kppbwi.app/filemanager/internal/config"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

const (
	ctxSession ctxKey = 100 + iota
	ctxUser
)

// SessionFrom returns the authenticated session stored by RequireAuth.
func SessionFrom(ctx context.Context) (sqlc.Session, bool) {
	v, ok := ctx.Value(ctxSession).(sqlc.Session)
	return v, ok
}

// UserFrom returns the authenticated user stored by RequireAuth.
func UserFrom(ctx context.Context) (sqlc.User, bool) {
	v, ok := ctx.Value(ctxUser).(sqlc.User)
	return v, ok
}

func (s *Server) secureCookies() bool { return s.cfg.AppEnv != config.EnvDevelopment }

func isSafeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

// RequireAuth resolves the fm_session cookie (SPEC §8.1 "Sesi"). Missing,
// invalid or expired sessions get 401 UNAUTHENTICATED; a disabled account
// gets 403 ACCOUNT_DISABLED and its cookie is cleared.
func (s *Server) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(auth.CookieName)
		if err != nil || c.Value == "" {
			WriteError(w, r, CodeUnauthenticated, 0, "authentication required", nil)
			return
		}
		sess, u, err := s.auth.Authenticate(r.Context(), c.Value)
		switch {
		case err == nil:
		case errors.Is(err, auth.ErrAccountDisabled):
			http.SetCookie(w, auth.ClearCookie(s.secureCookies()))
			WriteError(w, r, CodeAccountDisabled, 0, "account is disabled", nil)
			return
		case errors.Is(err, auth.ErrSessionInvalid), errors.Is(err, auth.ErrSessionExpired):
			http.SetCookie(w, auth.ClearCookie(s.secureCookies()))
			WriteError(w, r, CodeUnauthenticated, 0, "session invalid or expired", nil)
			return
		default:
			s.logger.ErrorContext(r.Context(), "session lookup failed", slog.String("error", err.Error()))
			WriteError(w, r, CodeInternal, 0, "internal server error", nil)
			return
		}
		ctx := context.WithValue(r.Context(), ctxSession, sess)
		ctx = context.WithValue(ctx, ctxUser, u)
		ctx = WithUserID(ctx, u.ID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// RequirePasswordChanged blocks every endpoint except the auth group while
// must_change_password is set (SPEC §8.1 step 7).
func RequirePasswordChanged(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, ok := UserFrom(r.Context()); ok && u.MustChangePassword != 0 {
			WriteError(w, r, CodePasswordChangeRequired, 0, "password change required", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// RequireAdmin allows only role=admin.
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, ok := UserFrom(r.Context()); !ok || u.Role != "admin" {
			WriteError(w, r, CodeForbidden, 0, "admin role required", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// OriginCheck rejects state-changing requests whose Fetch Metadata or Origin
// show they come from another site (SPEC §8.1 "CSRF"). It runs for every
// non-safe method, including login (which has no session token yet).
func OriginCheck(baseURL string) func(http.Handler) http.Handler {
	// Origin carries scheme://host only; a base URL with a path (review A
	// m-05) must not reject every non-GET request.
	base := strings.ToLower(strings.TrimRight(baseURL, "/"))
	if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
		base = strings.ToLower(u.Scheme + "://" + u.Host)
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !isSafeMethod(r.Method) {
				if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
					WriteError(w, r, CodeCSRFInvalid, 0, "cross-site request rejected", map[string]any{"reason": "sec-fetch-site"})
					return
				}
				if o := r.Header.Get("Origin"); o != "" && strings.ToLower(strings.TrimRight(o, "/")) != base {
					WriteError(w, r, CodeCSRFInvalid, 0, "origin not allowed", map[string]any{"reason": "origin"})
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// RequireCSRF checks X-CSRF-Token for non-safe methods of authenticated
// routes. Must run after RequireAuth.
func (s *Server) RequireCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isSafeMethod(r.Method) {
			sess, ok := SessionFrom(r.Context())
			if !ok || !s.auth.VerifyCSRF(sess, r.Header.Get("X-CSRF-Token")) {
				WriteError(w, r, CodeCSRFInvalid, 0, "missing or invalid CSRF token", map[string]any{"reason": "token"})
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
