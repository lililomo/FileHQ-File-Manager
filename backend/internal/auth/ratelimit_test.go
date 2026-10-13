package auth

import (
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{t: time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)} }
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

func TestRateLimiterBucket(t *testing.T) {
	clock := newFakeClock()
	l := NewRateLimiter(20, time.Minute, clock.Now)
	for i := 0; i < 20; i++ {
		if ok, _ := l.Allow("1.2.3.4"); !ok {
			t.Fatalf("request %d should pass", i+1)
		}
	}
	ok, wait := l.Allow("1.2.3.4")
	if ok || wait < time.Second || wait > 4*time.Second {
		t.Fatalf("21st request: ok=%v wait=%v", ok, wait)
	}
	if ok, _ := l.Allow("5.6.7.8"); !ok {
		t.Fatal("other key must be independent")
	}
	clock.Advance(3 * time.Second) // refills 1 token
	if ok, _ := l.Allow("1.2.3.4"); !ok {
		t.Fatal("token should have refilled")
	}
	if ok, _ := l.Allow("1.2.3.4"); ok {
		t.Fatal("bucket should be empty again")
	}
	clock.Advance(11 * time.Minute) // prune idle buckets
	l.Allow("9.9.9.9")
	l.mu.Lock()
	n := len(l.buckets)
	l.mu.Unlock()
	if n != 1 {
		t.Fatalf("idle buckets not pruned: %d", n)
	}
}

func TestWindowLimiter(t *testing.T) {
	clock := newFakeClock()
	w := NewWindowLimiter(5, 15*time.Minute, clock.Now)
	for i := 0; i < 5; i++ {
		if ok, _ := w.Allow("sess"); !ok {
			t.Fatalf("event %d should pass", i+1)
		}
	}
	if ok, wait := w.Allow("sess"); ok || wait <= 0 {
		t.Fatalf("6th event: ok=%v wait=%v", ok, wait)
	}
	clock.Advance(16 * time.Minute)
	if ok, _ := w.Allow("sess"); !ok {
		t.Fatal("window should have slid")
	}
}

func TestLockoutDuration(t *testing.T) {
	cases := map[int64]time.Duration{1: 0, 4: 0, 5: time.Minute, 9: time.Minute, 10: 5 * time.Minute,
		15: 15 * time.Minute, 20: time.Hour, 100: time.Hour}
	for n, want := range cases {
		if got := LockoutDuration(n); got != want {
			t.Errorf("LockoutDuration(%d)=%v want %v", n, got, want)
		}
	}
}

func TestDeviceLabel(t *testing.T) {
	cases := map[string]string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36":        "Chrome · Windows",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_0) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15": "Safari · macOS",
		"Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0":                                             "Firefox · Linux",
		"curl/8.5.0": "curl · Unknown",
		"":           "Unknown · Unknown",
	}
	for ua, want := range cases {
		if got := DeviceLabel(ua); got != want {
			t.Errorf("DeviceLabel(%q)=%q want %q", ua, got, want)
		}
	}
}
