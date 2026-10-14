package files

import (
	"context"
	"crypto/md5"  //nolint:gosec // MD5 is offered as a checksum, not for security
	"crypto/sha1" //nolint:gosec // idem
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"kppbwi.app/filemanager/internal/fsjail"
)

// ChecksumSyncMax is the largest file hashed synchronously (SPEC §8.7);
// bigger files answer FileTooLargeError until the task engine (Step 11).
const ChecksumSyncMax int64 = 100 << 20

// ChecksumCacheTTL keeps digests for Properties/stat.
const ChecksumCacheTTL = 10 * time.Minute

const checksumCacheMax = 5000

// ChecksumAlgos lists the supported algorithms in canonical order.
var ChecksumAlgos = []string{"md5", "sha1", "sha256"}

// FileTooLargeError: the file exceeds the synchronous limit (API: 413 FILE_TOO_LARGE).
type FileTooLargeError struct {
	Size int64
	Max  int64
}

func (e *FileTooLargeError) Error() string {
	return fmt.Sprintf("files: file too large for synchronous checksum (%d > %d)", e.Size, e.Max)
}

// ParseAlgos validates and deduplicates algorithm names ("" = all).
func ParseAlgos(in []string) ([]string, bool) {
	if len(in) == 0 {
		return append([]string{}, ChecksumAlgos...), true
	}
	seen := map[string]bool{}
	var out []string
	for _, a := range in {
		a = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(a), "-", ""))
		switch a {
		case "md5", "sha1", "sha256":
		default:
			return nil, false
		}
		if !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, k int) bool { return algoRank(out[i]) < algoRank(out[k]) })
	return out, true
}

func algoRank(a string) int {
	for i, x := range ChecksumAlgos {
		if x == a {
			return i
		}
	}
	return len(ChecksumAlgos)
}

type checksumEntry struct {
	key     string // size|mtime
	sums    map[string]string
	expires time.Time
}

type checksumCache struct {
	mu  sync.Mutex
	m   map[string]checksumEntry
	now func() time.Time
}

func newChecksumCache(now func() time.Time) *checksumCache {
	if now == nil {
		now = time.Now
	}
	return &checksumCache{m: map[string]checksumEntry{}, now: now}
}

func (c *checksumCache) get(userID string, p fsjail.VPath, key string) map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[userID+"|"+string(p)]
	if !ok || e.key != key || !c.now().Before(e.expires) {
		return nil
	}
	out := map[string]string{}
	for k, v := range e.sums {
		out[k] = v
	}
	return out
}

func (c *checksumCache) put(userID string, p fsjail.VPath, key string, sums map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= checksumCacheMax {
		c.m = map[string]checksumEntry{}
	}
	id := userID + "|" + string(p)
	e, ok := c.m[id]
	if !ok || e.key != key {
		e = checksumEntry{key: key, sums: map[string]string{}}
	}
	for k, v := range sums {
		e.sums[k] = v
	}
	e.expires = c.now().Add(ChecksumCacheTTL)
	c.m[id] = e
}

func (c *checksumCache) invalidate(userID string, p fsjail.VPath) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.m, userID+"|"+string(p))
}

func statKey(info fsjail.Info) string {
	return fmt.Sprintf("%d|%d", info.Size, info.ModTime.UnixNano())
}

// Checksum computes the requested digests of a regular file ≤ ChecksumSyncMax
// (SPEC §8.7 "Checksum"); results are cached per size+mtime.
func (s *Service) Checksum(ctx context.Context, j *fsjail.Jail, userID string, p fsjail.VPath, algos []string) (map[string]string, error) {
	algos, ok := ParseAlgos(algos)
	if !ok {
		return nil, &ValidationError{Reason: "bad_algo", Fields: map[string]string{"algos": "md5, sha1 or sha256"}}
	}
	info, err := j.Stat(p)
	if err != nil {
		return nil, err
	}
	if info.IsDir {
		return nil, &ValidationError{Reason: "not_a_file", Path: string(p), Fields: map[string]string{"path": "not_a_file"}}
	}
	key := statKey(info)
	out := map[string]string{}
	var missing []string
	cached := s.checksums.get(userID, p, key)
	for _, a := range algos {
		if v, ok := cached[a]; ok {
			out[a] = v
		} else {
			missing = append(missing, a)
		}
	}
	if len(missing) == 0 {
		return out, nil
	}
	// Big files are hashed by a task (Step 11); a cached digest above is
	// still served synchronously.
	if info.Size > ChecksumSyncMax && !unbounded(ctx) {
		return nil, &FileTooLargeError{Size: info.Size, Max: ChecksumSyncMax}
	}
	f, err := j.Open(p)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	hashes := map[string]hash.Hash{}
	writers := make([]io.Writer, 0, len(missing))
	for _, a := range missing {
		var h hash.Hash
		switch a {
		case "md5":
			h = md5.New() //nolint:gosec // checksum feature
		case "sha1":
			h = sha1.New() //nolint:gosec // checksum feature
		default:
			h = sha256.New()
		}
		hashes[a] = h
		writers = append(writers, h)
	}
	rep := reporterOf(ctx)
	if rep != nil {
		rep.SetTotal(info.Size)
	}
	if _, err := io.Copy(io.MultiWriter(writers...), &ctxReader{ctx: ctx, r: f, rep: rep}); err != nil {
		return nil, err
	}
	for a, h := range hashes {
		out[a] = hex.EncodeToString(h.Sum(nil))
	}
	s.checksums.put(userID, p, key, out)
	s.logActivity(ctx, s.repo, userID, ActChecksum, string(p), "", map[string]any{"algos": missing, "size": info.Size}, "")
	return out, nil
}

// VerifyChecksum compares expected (hex, case-insensitive) with the digest.
func (s *Service) VerifyChecksum(ctx context.Context, j *fsjail.Jail, userID string, p fsjail.VPath, algo, expected string) (match bool, actual string, err error) {
	algos, ok := ParseAlgos([]string{algo})
	if !ok || len(algos) != 1 {
		return false, "", &ValidationError{Reason: "bad_algo", Fields: map[string]string{"algo": "md5, sha1 or sha256"}}
	}
	sums, err := s.Checksum(ctx, j, userID, p, algos)
	if err != nil {
		return false, "", err
	}
	actual = sums[algos[0]]
	expected = strings.ToLower(strings.TrimSpace(expected))
	return expected != "" && expected == actual, actual, nil
}

// CachedChecksums returns digests already computed for the file (stat view).
func (s *Service) CachedChecksums(userID string, p fsjail.VPath, info fsjail.Info) map[string]string {
	return s.checksums.get(userID, p, statKey(info))
}

// ctxReader stops a long hash when the request is cancelled.
type ctxReader struct {
	ctx context.Context
	r   io.Reader
	rep Reporter // task progress (bytes), nil outside tasks
}

func (c *ctxReader) Read(b []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := c.r.Read(b)
	if c.rep != nil && n > 0 {
		c.rep.Add(int64(n))
	}
	return n, err
}
