package archive

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"sync"
	"time"
)

// Archive sessions (PROMPT Step 14b): the password of an encrypted archive
// is kept in memory for SessionTTL under a random id so the signed
// entry-content URL (used by <img>/fetch without a request body) never
// carries it. A session is bound to the user and the archive path; the id
// is a bearer secret with the same standing as a signed URL.

// SessionTTL is the idle lifetime of an archive session (refreshed on use).
const SessionTTL = 10 * time.Minute

// MaxSessionsPerUser bounds stored passwords per user (oldest evicted).
const MaxSessionsPerUser = 32

// ArchiveSession is one stored password.
type ArchiveSession struct {
	ID       string
	UserID   string
	Archive  string
	Password string
	Encoding string
	expires  time.Time
}

type sessionStore struct {
	mu  sync.Mutex
	m   map[string]*ArchiveSession
	ttl time.Duration
	now func() time.Time
}

func newSessionStore(now func() time.Time) *sessionStore {
	return &sessionStore{m: map[string]*ArchiveSession{}, ttl: SessionTTL, now: now}
}

// Put stores (or refreshes) the password of archive for userID and returns
// the session id. An existing session with the same password is reused.
func (s *sessionStore) Put(userID, archive, password, encoding string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	s.sweepLocked(now)
	var (
		count  int
		oldest *ArchiveSession
	)
	for _, sess := range s.m {
		if sess.UserID != userID {
			continue
		}
		count++
		if oldest == nil || sess.expires.Before(oldest.expires) {
			oldest = sess
		}
		if sess.Archive == archive && sess.Encoding == encoding && subtle.ConstantTimeCompare([]byte(sess.Password), []byte(password)) == 1 {
			sess.expires = now.Add(s.ttl)
			return sess.ID, nil
		}
	}
	if count >= MaxSessionsPerUser && oldest != nil {
		delete(s.m, oldest.ID)
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b[:])
	s.m[id] = &ArchiveSession{ID: id, UserID: userID, Archive: archive, Password: password, Encoding: encoding, expires: now.Add(s.ttl)}
	return id, nil
}

// Find returns the id of a live session of userID for archive with the
// same password and encoding (lifetime refreshed).
func (s *sessionStore) Find(userID, archive, password, encoding string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	for _, sess := range s.m {
		if sess.UserID != userID || sess.Archive != archive || sess.Encoding != encoding || now.After(sess.expires) {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(sess.Password), []byte(password)) == 1 {
			sess.expires = now.Add(s.ttl)
			return sess.ID, true
		}
	}
	return "", false
}

// Get returns the session when it exists, belongs to userID, names archive
// and has not expired; the lifetime is refreshed.
func (s *sessionStore) Get(id, userID, archive string) (ArchiveSession, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.m[id]
	if !ok {
		return ArchiveSession{}, false
	}
	now := s.now()
	if now.After(sess.expires) {
		delete(s.m, id)
		return ArchiveSession{}, false
	}
	if sess.UserID != userID || sess.Archive != archive {
		return ArchiveSession{}, false
	}
	sess.expires = now.Add(s.ttl)
	return *sess, true
}

// Drop forgets every session of an archive path (after it changed or moved).
func (s *sessionStore) Drop(userID, archive string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, sess := range s.m {
		if sess.UserID == userID && sess.Archive == archive {
			delete(s.m, id)
		}
	}
}

func (s *sessionStore) sweepLocked(now time.Time) {
	for id, sess := range s.m {
		if now.After(sess.expires) {
			delete(s.m, id)
		}
	}
}
