package download

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"kppbwi.app/filemanager/internal/fsjail"
)

// ZipRequest is the payload behind a signed zip URL (SPEC §8.9): the path
// list lives in DATA_DIR/tmp/zip-<id>.json, the URL only carries the id.
type ZipRequest struct {
	ID        string    `json:"id"`
	UserID    string    `json:"userId"`
	Paths     []string  `json:"paths"`
	Name      string    `json:"name,omitempty"`
	Method    Method    `json:"method"`
	CreatedAt time.Time `json:"createdAt"`
}

// ErrZipRequestNotFound covers missing, expired and foreign payloads alike.
var ErrZipRequestNotFound = errors.New("zip request not found")

// ZipStore persists zip payloads for ZipPayloadTTL through a jail rooted at
// DATA_DIR/tmp (no os.* outside fsjail).
type ZipStore struct {
	jail      *fsjail.Jail
	ttl       time.Duration
	now       func() time.Time
	mu        sync.Mutex
	lastSweep time.Time
}

// ZipPayloadTTL keeps a payload a little longer than the signed URL so a
// download manager that starts late still finds it.
const ZipPayloadTTL = 10 * time.Minute

// NewZipStore opens DATA_DIR/tmp (created by config at start-up).
func NewZipStore(dataDir string, now func() time.Time) (*ZipStore, error) {
	if now == nil {
		now = time.Now
	}
	j, err := fsjail.Open(filepath.Join(dataDir, "tmp"))
	if err != nil {
		return nil, err
	}
	return &ZipStore{jail: j, ttl: ZipPayloadTTL, now: now}, nil
}

// Close releases the directory handle.
func (s *ZipStore) Close() error { return s.jail.Close() }

func zipFile(id string) (fsjail.VPath, error) {
	if len(id) != 32 {
		return "", ErrZipRequestNotFound
	}
	if _, err := hex.DecodeString(id); err != nil {
		return "", ErrZipRequestNotFound
	}
	return fsjail.Clean("/zip-" + id + ".json")
}

// Put stores req under a fresh random id and returns it.
func (s *ZipStore) Put(req ZipRequest) (string, error) {
	s.sweep()
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	req.ID = hex.EncodeToString(b[:])
	req.CreatedAt = s.now()
	vp, err := zipFile(req.ID)
	if err != nil {
		return "", err
	}
	f, err := s.jail.Create(vp, true)
	if err != nil {
		return "", err
	}
	enc := json.NewEncoder(f)
	if err := enc.Encode(req); err != nil {
		_ = f.Close()
		_ = s.jail.Remove(vp)
		return "", err
	}
	if err := f.Close(); err != nil {
		_ = s.jail.Remove(vp)
		return "", err
	}
	return req.ID, nil
}

// Get loads a payload and checks owner and age.
func (s *ZipStore) Get(id, userID string) (ZipRequest, error) {
	vp, err := zipFile(id)
	if err != nil {
		return ZipRequest{}, err
	}
	f, err := s.jail.Open(vp)
	if err != nil {
		return ZipRequest{}, ErrZipRequestNotFound
	}
	defer func() { _ = f.Close() }()
	var req ZipRequest
	if err := json.NewDecoder(io.LimitReader(f, 4<<20)).Decode(&req); err != nil {
		return ZipRequest{}, ErrZipRequestNotFound
	}
	if req.UserID != userID || s.now().Sub(req.CreatedAt) > s.ttl {
		return ZipRequest{}, ErrZipRequestNotFound
	}
	return req, nil
}

// sweep removes payloads older than the TTL, at most once a minute. File
// mtimes are wall-clock, so the comparison uses time.Now even when the
// store runs on an injected clock.
func (s *ZipStore) sweep() {
	s.mu.Lock()
	if s.now().Sub(s.lastSweep) < time.Minute {
		s.mu.Unlock()
		return
	}
	s.lastSweep = s.now()
	s.mu.Unlock()
	entries, err := s.jail.ReadDir(fsjail.Root)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name, "zip-") || !strings.HasSuffix(e.Name, ".json") || e.IsDir {
			continue
		}
		if time.Since(e.ModTime) > s.ttl {
			_ = s.jail.Remove(e.Path)
		}
	}
}
