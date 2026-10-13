package httpapi

import (
	"net/http"
	"sync"
	"time"

	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/fsjail"
)

// Jail handles are cheap but opening one per request still costs an
// openat + fstat; handlers reuse one per user and the cache closes handles
// idle for jailIdle (PROMPT Step 5 task 6). Sweeps run opportunistically on
// access, so there is no background goroutine to leak in tests.
const (
	jailIdle       = 5 * time.Minute
	jailSweepEvery = time.Minute
)

type jailEntry struct {
	jail     *fsjail.Jail
	username string
	lastUsed time.Time
	inUse    int
}

type jailCache struct {
	mu        sync.Mutex
	m         map[string]*jailEntry
	lastSweep time.Time
	now       func() time.Time
}

func newJailCache(now func() time.Time) *jailCache {
	if now == nil {
		now = time.Now
	}
	return &jailCache{m: map[string]*jailEntry{}, now: now, lastSweep: now()}
}

// acquire returns the user's jail and a release func the caller must defer.
// Entries in use are never closed by a sweep.
func (c *jailCache) acquire(storageRoot string, u sqlc.User) (*fsjail.Jail, func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if now.Sub(c.lastSweep) >= jailSweepEvery {
		c.sweepLocked(now)
	}
	e, ok := c.m[u.ID]
	if !ok || e.username != u.Username {
		j, err := fsjail.OpenUser(storageRoot, u.Username, u.ID)
		if err != nil {
			return nil, nil, err
		}
		if ok && e.inUse == 0 {
			_ = e.jail.Close()
		}
		e = &jailEntry{jail: j, username: u.Username}
		c.m[u.ID] = e
	}
	e.lastUsed = now
	e.inUse++
	release := func() {
		c.mu.Lock()
		e.inUse--
		e.lastUsed = c.now()
		c.mu.Unlock()
	}
	return e.jail, release, nil
}

func (c *jailCache) sweepLocked(now time.Time) {
	for id, e := range c.m {
		if e.inUse == 0 && now.Sub(e.lastUsed) > jailIdle {
			_ = e.jail.Close()
			delete(c.m, id)
		}
	}
	c.lastSweep = now
}

// evict closes a user's handle (account deleted/disabled).
func (c *jailCache) evict(userID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.m[userID]; ok && e.inUse == 0 {
		_ = e.jail.Close()
		delete(c.m, userID)
	}
}

// userJail resolves the authenticated user's jail. On failure the error
// envelope is already written and ok is false.
func (s *Server) userJail(w http.ResponseWriter, r *http.Request) (j *fsjail.Jail, u sqlc.User, release func(), ok bool) {
	u, ok = UserFrom(r.Context())
	if !ok {
		WriteError(w, r, CodeUnauthenticated, 0, "authentication required", nil)
		return nil, u, nil, false
	}
	j, release, err := s.jails.acquire(s.cfg.StorageRoot, u)
	if err != nil {
		if !WriteFSError(w, r, err) {
			WriteError(w, r, CodeInternal, 0, "storage unavailable", nil)
		}
		return nil, u, nil, false
	}
	return j, u, release, true
}
