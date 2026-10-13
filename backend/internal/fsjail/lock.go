package fsjail

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Mode of a path lock.
type Mode int

// Lock modes.
const (
	Read  Mode = iota // compatible with other readers
	Write             // exclusive against everything on the same path, ancestors and descendants
)

// SyncLockTimeout is how long a synchronous HTTP handler waits for a lock
// before answering OPERATION_IN_PROGRESS (SPEC §5.5).
const SyncLockTimeout = 2 * time.Second

// TaskLockTimeout is the wait budget of background tasks.
const TaskLockTimeout = 30 * time.Second

type heldLock struct {
	path  VPath
	write bool
	owner string
	grant uint64 // id of the Acquire that created it; release removes by grant id
}

// LockManager serializes conflicting operations per user (SPEC §5.5, ADR-19).
// Locks are in-memory; all paths of one Acquire are taken atomically under a
// single mutex, so there is no lock-ordering deadlock.
type LockManager struct {
	mu    sync.Mutex
	cond  *sync.Cond
	held  map[string][]heldLock // userID → active locks
	seq   uint64
	waits int // number of goroutines blocked in Acquire (tests)
}

// NewLockManager returns an empty manager.
func NewLockManager() *LockManager {
	m := &LockManager{held: map[string][]heldLock{}}
	m.cond = sync.NewCond(&m.mu)
	return m
}

func conflicts(a, b heldLock) bool {
	if !a.write && !b.write {
		return false
	}
	return a.path == b.path || a.path.HasPrefix(b.path) || b.path.HasPrefix(a.path)
}

func normalizePaths(paths []VPath) []VPath {
	seen := map[VPath]struct{}{}
	out := make([]VPath, 0, len(paths))
	for _, p := range paths {
		if p == "" {
			p = Root
		}
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// blockedBy returns the owner of the first conflicting active lock.
func (m *LockManager) blockedBy(userID string, want []heldLock) (string, bool) {
	for _, h := range m.held[userID] {
		for _, w := range want {
			if conflicts(h, w) {
				return h.owner, true
			}
		}
	}
	return "", false
}

// grant records want under a fresh grant id and returns its release func.
// Must be called with m.mu held.
func (m *LockManager) grant(userID string, want []heldLock) func() {
	m.seq++
	id := m.seq
	for i := range want {
		want[i].grant = id
	}
	m.held[userID] = append(m.held[userID], want...)
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			active := m.held[userID]
			kept := active[:0]
			for _, h := range active {
				if h.grant != id {
					kept = append(kept, h)
				}
			}
			if len(kept) == 0 {
				delete(m.held, userID)
			} else {
				m.held[userID] = kept
			}
			m.cond.Broadcast()
		})
	}
}

func makeWant(mode Mode, owner string, paths []VPath) []heldLock {
	ps := normalizePaths(paths)
	if len(ps) == 0 {
		ps = []VPath{Root}
	}
	want := make([]heldLock, 0, len(ps))
	for _, p := range ps {
		want = append(want, heldLock{path: p, write: mode == Write, owner: owner})
	}
	return want
}

func lockedErr(p VPath, reason, by string, cause error) error {
	return &Error{Code: CodeLocked, Op: "lock", Path: p, Reason: reason, By: by, Err: cause}
}

// TryAcquire takes the locks now or fails with ErrLocked (By = owner of the
// conflicting lock). owner is a label shown to users ("task:compress 1234").
func (m *LockManager) TryAcquire(userID string, mode Mode, owner string, paths ...VPath) (release func(), err error) {
	want := makeWant(mode, owner, paths)
	m.mu.Lock()
	defer m.mu.Unlock()
	if by, blocked := m.blockedBy(userID, want); blocked {
		return nil, lockedErr(want[0].path, "locked", by, ErrLocked)
	}
	return m.grant(userID, want), nil
}

// Acquire waits until the locks are free or ctx ends. Synchronous handlers
// pass a ctx with SyncLockTimeout; on timeout the error is ErrLocked with
// Reason "timeout" and By naming the holder.
func (m *LockManager) Acquire(ctx context.Context, userID string, mode Mode, owner string, paths ...VPath) (release func(), err error) {
	want := makeWant(mode, owner, paths)
	m.mu.Lock()
	defer m.mu.Unlock()
	stop := context.AfterFunc(ctx, func() {
		m.mu.Lock()
		m.cond.Broadcast()
		m.mu.Unlock()
	})
	defer stop()
	for {
		by, blocked := m.blockedBy(userID, want)
		if !blocked {
			return m.grant(userID, want), nil
		}
		if cerr := ctx.Err(); cerr != nil {
			return nil, lockedErr(want[0].path, "timeout", by, cerr)
		}
		m.waits++
		m.cond.Wait()
		m.waits--
	}
}

// LockInfo is a public view of one held lock.
type LockInfo struct {
	Path  VPath
	Write bool
	Owner string
}

// Held lists the active locks of a user (task display, tests).
func (m *LockManager) Held(userID string) []LockInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]LockInfo, 0, len(m.held[userID]))
	for _, h := range m.held[userID] {
		out = append(out, LockInfo{Path: h.path, Write: h.write, Owner: h.owner})
	}
	return out
}
