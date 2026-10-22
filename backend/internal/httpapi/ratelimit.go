package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"kppbwi.app/filemanager/internal/auth"
	"kppbwi.app/filemanager/internal/config"
)

// Central HTTP rate limiting (SPEC §10.5, Step 20). Every limit is a token
// bucket keyed by client IP, session or signed scope; the numbers come from
// config.RateLimits (env RATE_LIMIT_*). Service-level limiters that predate
// this file (login, share unlock, search, remote download, user suggest,
// download/thumb concurrency, SSE cap) keep their own enforcement but read
// the same configuration; every 429 carries Retry-After.
//
// Responses carry the IETF draft headers RateLimit-Limit / RateLimit-Remaining
// / RateLimit-Reset and RateLimit-Policy so clients can back off before the
// first 429.

type tokenBucket struct {
	tokens float64
	last   time.Time
}

// bucketLimiter is a per-key token bucket: capacity = burst, refill =
// limit/window tokens per second. Idle keys are pruned.
type bucketLimiter struct {
	mu        sync.Mutex
	capacity  float64
	refill    float64
	window    time.Duration
	limit     int
	now       func() time.Time
	buckets   map[string]*tokenBucket
	lastPrune time.Time
}

func newBucketLimiter(l config.RateLimit, now func() time.Time) *bucketLimiter {
	if now == nil {
		now = time.Now
	}
	burst := l.Burst
	if burst <= 0 {
		burst = l.Limit
	}
	return &bucketLimiter{
		capacity: float64(burst),
		refill:   float64(l.Limit) / l.Window.Seconds(),
		window:   l.Window,
		limit:    l.Limit,
		now:      now,
		buckets:  map[string]*tokenBucket{},
	}
}

// take consumes one token for key. remaining is the whole-token balance
// after the call; retry is how long to wait when denied; reset is the time
// until the bucket is full again.
func (l *bucketLimiter) take(key string) (ok bool, remaining int, retry, reset time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, found := l.buckets[key]
	if !found {
		b = &tokenBucket{tokens: l.capacity, last: now}
		l.buckets[key] = b
	} else if el := now.Sub(b.last).Seconds(); el > 0 {
		b.tokens = math.Min(l.capacity, b.tokens+el*l.refill)
		b.last = now
	}
	if now.Sub(l.lastPrune) > time.Minute {
		l.lastPrune = now
		for k, v := range l.buckets {
			if now.Sub(v.last) > 2*l.window && v.tokens >= l.capacity-1 {
				delete(l.buckets, k)
			}
		}
	}
	if b.tokens >= 1 {
		b.tokens--
		ok = true
	} else {
		retry = time.Duration(math.Ceil((1-b.tokens)/l.refill)) * time.Second
		if retry < time.Second {
			retry = time.Second
		}
	}
	remaining = int(math.Floor(b.tokens))
	reset = time.Duration(math.Ceil((l.capacity-b.tokens)/l.refill)) * time.Second
	return ok, remaining, retry, reset
}

// keyIP keys on the client address (after RealIP).
func keyIP(r *http.Request) string { return "ip:" + ClientIP(r) }

// keySession keys on the session cookie when present (hashed, so the token
// never sits in memory maps in clear) and on the client IP otherwise.
func keySession(r *http.Request) string {
	if c, err := r.Cookie(auth.CookieName); err == nil && c.Value != "" {
		sum := sha256.Sum256([]byte(c.Value))
		return "s:" + hex.EncodeToString(sum[:8])
	}
	return keyIP(r)
}

// keyScope keys signed media requests on their scope ("u:<id>" or
// "r:<share>:<user>") before verification; unsigned requests fall back to
// the IP so they cannot exhaust somebody else's quota.
func keyScope(r *http.Request) string {
	if sc := r.URL.Query().Get("scope"); sc != "" && len(sc) <= 128 {
		return "sc:" + sc
	}
	return keyIP(r)
}

// rateLimited writes the 429 envelope with Retry-After (seconds).
func rateLimited(w http.ResponseWriter, r *http.Request, retry time.Duration, name string) {
	secs := int(math.Ceil(retry.Seconds()))
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	WriteError(w, r, CodeRateLimited, 0, "too many requests, try again later", map[string]any{"retryAfter": secs, "limit": name})
}

// limitBy builds a middleware enforcing lim per key. A disabled limit
// returns a pass-through.
func (s *Server) limitBy(name string, lim config.RateLimit, key func(*http.Request) string) func(http.Handler) http.Handler {
	if !lim.Enabled() {
		return func(next http.Handler) http.Handler { return next }
	}
	bl := newBucketLimiter(lim, nil)
	policy := strconv.Itoa(lim.Limit) + ";w=" + strconv.Itoa(int(lim.Window.Seconds()))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ok, remaining, retry, reset := bl.take(key(r))
			h := w.Header()
			h.Set("RateLimit-Limit", strconv.Itoa(int(bl.capacity)))
			h.Set("RateLimit-Remaining", strconv.Itoa(remaining))
			h.Set("RateLimit-Reset", strconv.Itoa(int(reset.Seconds())))
			h.Set("RateLimit-Policy", policy)
			if !ok {
				rateLimited(w, r, retry, name)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// limitAPI is the coarse per-session limit on /api/v1 (SPEC §10.5 "API
// umum"). Streaming and signed media routes are exempt: they are governed by
// their own limits (thumb per scope, tus chunk size, download concurrency,
// public share per link+IP) and would otherwise starve a grid of
// thumbnails or a chunked upload.
func (s *Server) limitAPI(lim config.RateLimit, exemptPrefixes ...string) func(http.Handler) http.Handler {
	inner := s.limitBy("api", lim, keySession)
	return func(next http.Handler) http.Handler {
		limited := inner(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if hasAnyPrefix(r.URL.Path, exemptPrefixes) {
				next.ServeHTTP(w, r)
				return
			}
			limited.ServeHTTP(w, r)
		})
	}
}
