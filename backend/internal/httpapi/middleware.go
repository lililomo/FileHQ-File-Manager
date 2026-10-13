package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"kppbwi.app/filemanager/internal/config"
)

type ctxKey int

const (
	ctxRequestID ctxKey = iota
	ctxPeerTrusted
	ctxUserID
)

var requestIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// RequestIDFrom returns the request ID stored by RequestID middleware.
func RequestIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(ctxRequestID).(string)
	return v
}

// PeerTrustedFrom reports whether the direct TCP peer is a trusted proxy.
func PeerTrustedFrom(ctx context.Context) bool {
	v, _ := ctx.Value(ctxPeerTrusted).(bool)
	return v
}

// UserIDFrom returns the authenticated user ID, if auth middleware (Step 3)
// stored one; "" otherwise.
func UserIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(ctxUserID).(string)
	return v
}

// WithUserID stores the user ID for access logging. Used by auth middleware.
func WithUserID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxUserID, id)
}

// ClientIP returns the client address after RealIP (host only).
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func peerAddr(r *http.Request) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return a.Unmap(), true
}

// TrustedPeer records once per request whether the TCP peer is in
// TRUSTED_PROXIES. It must run before RequestID, RealIP and SecurityHeaders.
func TrustedPeer(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			trusted := false
			if a, ok := peerAddr(r); ok {
				trusted = cfg.IsTrustedProxy(a)
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxPeerTrusted, trusted)))
		})
	}
}

// RequestID accepts X-Request-ID only from a trusted proxy; otherwise it
// generates a 128-bit hex ID. The ID is echoed in the response header.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := ""
		if PeerTrustedFrom(r.Context()) {
			if h := r.Header.Get("X-Request-ID"); requestIDRe.MatchString(h) {
				id = h
			}
		}
		if id == "" {
			id = newID()
		}
		w.Header().Set("X-Request-ID", id)
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxRequestID, id)))
	})
}

func newID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b[:])
}

// RealIP rewrites RemoteAddr from X-Forwarded-For when, and only when, the
// peer is a trusted proxy. The client is the right-most address that is not
// itself a trusted proxy.
func RealIP(cfg *config.Config) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if PeerTrustedFrom(r.Context()) {
				if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
					parts := strings.Split(xff, ",")
					for i := len(parts) - 1; i >= 0; i-- {
						a, err := netip.ParseAddr(strings.TrimSpace(parts[i]))
						if err != nil {
							break
						}
						a = a.Unmap()
						if !cfg.IsTrustedProxy(a) || i == 0 {
							r.RemoteAddr = net.JoinHostPort(a.String(), "0")
							break
						}
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// InitialCSP is the Step 1 policy; the final policy with nonces is Step 20.
const InitialCSP = "default-src 'self'; object-src 'none'; frame-ancestors 'none'; base-uri 'self'"

// SecurityHeaders sets the baseline response headers (SPEC §10.3). HSTS is
// added only when the request is known to be HTTPS (TLS or trusted
// X-Forwarded-Proto).
func SecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=(), usb=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("X-Robots-Tag", "noindex, nofollow")
		h.Set("Content-Security-Policy", InitialCSP)
		https := r.TLS != nil || (PeerTrustedFrom(r.Context()) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"))
		if https {
			h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		}
		next.ServeHTTP(w, r)
	})
}

// Recoverer converts panics into a 500 INTERNAL envelope and logs the stack.
// Nothing from the panic value reaches the client.
func Recoverer(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ww, ok := w.(middleware.WrapResponseWriter)
			if !ok {
				ww = middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			}
			defer func() {
				if rec := recover(); rec != nil {
					if rec == http.ErrAbortHandler { //nolint:errorlint // sentinel comparison is the documented idiom
						panic(rec)
					}
					logger.ErrorContext(r.Context(), "panic recovered",
						slog.Any("panic", rec),
						slog.String("request_id", RequestIDFrom(r.Context())),
						slog.String("stack", string(debug.Stack())),
					)
					if ww.Status() == 0 {
						WriteError(ww, r, CodeInternal, 0, "internal server error", nil)
					}
				}
			}()
			next.ServeHTTP(ww, r)
		})
	}
}

// AccessLog writes one structured line per request. Query strings are never
// logged (they may contain signed-URL parameters).
func AccessLog(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			attrs := []slog.Attr{
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", ww.Status()),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.Int("bytes", ww.BytesWritten()),
				slog.String("ip", ClientIP(r)),
				slog.String("request_id", RequestIDFrom(r.Context())),
			}
			if uid := UserIDFrom(r.Context()); uid != "" {
				attrs = append(attrs, slog.String("user_id", uid))
			}
			level := slog.LevelInfo
			if ww.Status() >= 500 {
				level = slog.LevelError
			}
			logger.LogAttrs(r.Context(), level, "request", attrs...)
		})
	}
}

// MaxBytes limits request bodies (JSON API routes). Streaming routes (tus,
// upload) are mounted outside this middleware.
func MaxBytes(n int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil && r.Body != http.NoBody {
				r.Body = http.MaxBytesReader(w, r.Body, n)
			}
			next.ServeHTTP(w, r)
		})
	}
}
