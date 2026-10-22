package auth

import (
	"math"
	"sync"
	"time"

	"kppbwi.app/filemanager/internal/config"
)

// RateLimiter is an in-memory token bucket per key (client IP, session ...).
// Buckets refill at capacity/window and are pruned when idle (SPEC §10.5).
type RateLimiter struct {
	mu        sync.Mutex
	capacity  float64
	refill    float64 // tokens per second
	now       func() time.Time
	buckets   map[string]*bucket
	lastPrune time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

const pruneEvery = time.Minute

// NewRateLimiter allows `capacity` requests per `window` per key (bucket size
// = capacity, continuous refill).
func NewRateLimiter(capacity int, window time.Duration, now func() time.Time) *RateLimiter {
	if now == nil {
		now = time.Now
	}
	return &RateLimiter{
		capacity: float64(capacity),
		refill:   float64(capacity) / window.Seconds(),
		now:      now,
		buckets:  map[string]*bucket{},
	}
}

// Allow consumes one token for key. When denied it returns how long the
// caller should wait before retrying (rounded up to whole seconds, min 1 s).
func (l *RateLimiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.capacity, last: now}
		l.buckets[key] = b
	} else {
		elapsed := now.Sub(b.last).Seconds()
		if elapsed > 0 {
			b.tokens = math.Min(l.capacity, b.tokens+elapsed*l.refill)
			b.last = now
		}
	}
	if now.Sub(l.lastPrune) > pruneEvery {
		l.prune(now)
	}
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := time.Duration(math.Ceil((1-b.tokens)/l.refill)) * time.Second
	if wait < time.Second {
		wait = time.Second
	}
	return false, wait
}

func (l *RateLimiter) prune(now time.Time) {
	l.lastPrune = now
	for k, b := range l.buckets {
		if now.Sub(b.last) > 10*time.Minute {
			delete(l.buckets, k)
		}
	}
}

// WindowLimiter allows at most max events per sliding window per key
// (used for change-password: 5 / 15 minutes per session).
type WindowLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	now    func() time.Time
	events map[string][]time.Time
}

// NewWindowLimiter builds a sliding-window limiter.
func NewWindowLimiter(maxEvents int, window time.Duration, now func() time.Time) *WindowLimiter {
	if now == nil {
		now = time.Now
	}
	return &WindowLimiter{max: maxEvents, window: window, now: now, events: map[string][]time.Time{}}
}

// Allow records an event for key and reports whether it is within the limit.
func (w *WindowLimiter) Allow(key string) (bool, time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	cutoff := now.Add(-w.window)
	ev := w.events[key][:0]
	for _, t := range w.events[key] {
		if t.After(cutoff) {
			ev = append(ev, t)
		}
	}
	if len(ev) >= w.max {
		w.events[key] = ev
		return false, ev[0].Add(w.window).Sub(now).Round(time.Second) + time.Second
	}
	w.events[key] = append(ev, now)
	if len(w.events) > 10000 { // defensive bound
		for k, v := range w.events {
			if len(v) == 0 || !v[len(v)-1].After(cutoff) {
				delete(w.events, k)
			}
		}
	}
	return true, 0
}

// Lockout policy (SPEC §8.1 step 4, §10.5): after 5 failures within the
// 15-minute window the account is locked; the duration escalates with the
// cumulative failure count since the last successful login.
const (
	LockoutThreshold = 5
	LockoutWindow    = 15 * time.Minute
)

// LockoutDuration returns how long to lock after the n-th consecutive failure
// (1 → 5 → 15 → 60 minutes at 5 / 10 / 15 / 20+ failures; 0 below 5).
func LockoutDuration(failures int64) time.Duration {
	switch {
	case failures >= 20:
		return 60 * time.Minute
	case failures >= 15:
		return 15 * time.Minute
	case failures >= 10:
		return 5 * time.Minute
	case failures >= LockoutThreshold:
		return time.Minute
	default:
		return 0
	}
}

// SpecOr returns the configured limit (RATE_LIMIT_*, SPEC §10.5) or the SPEC
// default when the config carries none (tests building Config by hand).
func SpecOr(l config.RateLimit, n int, w time.Duration) (int, time.Duration) {
	if l.Enabled() {
		return l.Limit, l.Window
	}
	return n, w
}

// Known reports whether key has in-memory events; Preload seeds n events at
// now for an unknown key so a limit survives a restart (login reads the
// recent failures from login_attempts, SPEC §10.2 item 6).
func (w *WindowLimiter) Known(key string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	_, ok := w.events[key]
	return ok
}

// Preload records n synthetic events for key at now (capped at max).
func (w *WindowLimiter) Preload(key string, n int, now time.Time) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if n > w.max {
		n = w.max
	}
	ev := make([]time.Time, n)
	for i := range ev {
		ev[i] = now
	}
	w.events[key] = ev
}
