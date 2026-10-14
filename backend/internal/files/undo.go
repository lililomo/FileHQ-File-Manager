package files

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"
)

// UndoTTL is how long an undo token stays valid (PROMPT Step 9a task 8).
const UndoTTL = 60 * time.Second

// UndoItem is one reversible pair.
type UndoItem struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// UndoTrashItem identifies one Recycle Bin row to restore.
type UndoTrashItem struct {
	ID   string `json:"id"`
	Path string `json:"path"`
}

// UndoInfo describes the inverse operation; it is returned to the client
// (without the token) so the UI can label the toast.
type UndoInfo struct {
	Kind    string          `json:"kind"`              // rename | move | restore | delete_created | batch_rename
	Path    string          `json:"path,omitempty"`    // rename: current path
	NewName string          `json:"newName,omitempty"` // rename: name to restore
	Items   []UndoItem      `json:"items,omitempty"`   // move / batch_rename pairs (from = current, to = original)
	Trash   []UndoTrashItem `json:"trash,omitempty"`   // restore
	BatchID string          `json:"batchId,omitempty"`
	Created []string        `json:"created,omitempty"` // delete_created (copy, duplicate, symlink)
}

type undoEntry struct {
	userID  string
	expires time.Time
	info    UndoInfo
}

// UndoStore keeps one-time undo tokens in memory per user (SPEC §8.5, PROMPT
// Step 9a: random token, 60 s, single use). Tokens of a restarted process
// are gone, which only disables "Urungkan" for the last minute.
type UndoStore struct {
	mu  sync.Mutex
	m   map[string]undoEntry
	now func() time.Time
	ttl time.Duration
}

// NewUndoStore builds an empty store.
func NewUndoStore(now func() time.Time) *UndoStore {
	if now == nil {
		now = time.Now
	}
	return &UndoStore{m: map[string]undoEntry{}, now: now, ttl: UndoTTL}
}

// Put stores info for userID and returns the token.
func (u *UndoStore) Put(userID string, info UndoInfo) string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	token := base64.RawURLEncoding.EncodeToString(b[:])
	u.mu.Lock()
	defer u.mu.Unlock()
	u.sweepLocked()
	u.m[token] = undoEntry{userID: userID, expires: u.now().Add(u.ttl), info: info}
	return token
}

// Take removes and returns the entry for token when it belongs to userID
// and has not expired.
func (u *UndoStore) Take(userID, token string) (UndoInfo, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	e, ok := u.m[token]
	if !ok {
		return UndoInfo{}, false
	}
	delete(u.m, token)
	if e.userID != userID || !u.now().Before(e.expires) {
		return UndoInfo{}, false
	}
	return e.info, true
}

// Len reports live tokens (tests).
func (u *UndoStore) Len() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.sweepLocked()
	return len(u.m)
}

func (u *UndoStore) sweepLocked() {
	now := u.now()
	for k, e := range u.m {
		if !now.Before(e.expires) {
			delete(u.m, k)
		}
	}
}
