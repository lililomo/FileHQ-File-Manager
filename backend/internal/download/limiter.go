package download

import "sync"

// MaxConcurrentPerUser bounds active downloads and zip streams per user
// (SPEC §8.9, §10.5 "concurrent_downloads"); inline media is not counted.
const MaxConcurrentPerUser = 3

// Limiter is a per-user counting semaphore.
type Limiter struct {
	mu    sync.Mutex
	n     map[string]int
	limit int
}

// NewLimiter builds a limiter; limit ≤ 0 means MaxConcurrentPerUser.
func NewLimiter(limit int) *Limiter {
	if limit <= 0 {
		limit = MaxConcurrentPerUser
	}
	return &Limiter{n: map[string]int{}, limit: limit}
}

// Limit is the configured maximum.
func (l *Limiter) Limit() int { return l.limit }

// Acquire takes a slot for userID; the release func must be called once.
func (l *Limiter) Acquire(userID string) (release func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n[userID] >= l.limit {
		return nil, false
	}
	l.n[userID]++
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if l.n[userID] <= 1 {
				delete(l.n, userID)
			} else {
				l.n[userID]--
			}
		})
	}, true
}

// Active reports the slots held by userID.
func (l *Limiter) Active(userID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.n[userID]
}
