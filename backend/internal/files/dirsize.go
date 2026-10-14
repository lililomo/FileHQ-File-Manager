package files

import (
	"context"
	"errors"
	"sync"
	"time"

	"kppbwi.app/filemanager/internal/fsjail"
)

// DirSizeBudget bounds one synchronous folder-size walk (PROMPT Step 10a
// task 5); longer walks return partial numbers until Step 11 turns them
// into a task.
const DirSizeBudget = 60 * time.Second

// DirSizeCacheTTL keeps results for the Ukuran column and Properties.
const DirSizeCacheTTL = 10 * time.Minute

const dirSizeCacheMax = 10000

// DirSizeResult is the response of POST /files/dir-size.
type DirSizeResult struct {
	Path         string `json:"path"`
	Size         int64  `json:"size"`
	Bytes        int64  `json:"bytes"` // alias of Size (SPEC task result shape)
	Files        int64  `json:"files"`
	Dirs         int64  `json:"dirs"`
	Partial      bool   `json:"partial"`
	CalculatedAt string `json:"calculatedAt"`
	Cached       bool   `json:"cached"`
}

type dirSizeEntry struct {
	res     DirSizeResult
	expires time.Time
}

type dirSizeCache struct {
	mu  sync.Mutex
	m   map[string]dirSizeEntry
	now func() time.Time
}

func newDirSizeCache(now func() time.Time) *dirSizeCache {
	if now == nil {
		now = time.Now
	}
	return &dirSizeCache{m: map[string]dirSizeEntry{}, now: now}
}

func (c *dirSizeCache) key(userID string, p fsjail.VPath) string { return userID + "|" + string(p) }

func (c *dirSizeCache) get(userID string, p fsjail.VPath) (DirSizeResult, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[c.key(userID, p)]
	if !ok || !c.now().Before(e.expires) {
		return DirSizeResult{}, false
	}
	return e.res, true
}

func (c *dirSizeCache) put(userID string, p fsjail.VPath, res DirSizeResult) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= dirSizeCacheMax {
		now := c.now()
		for k, e := range c.m {
			if !now.Before(e.expires) {
				delete(c.m, k)
			}
		}
		if len(c.m) >= dirSizeCacheMax {
			c.m = map[string]dirSizeEntry{}
		}
	}
	c.m[c.key(userID, p)] = dirSizeEntry{res: res, expires: c.now().Add(DirSizeCacheTTL)}
}

// invalidate drops the entries of every folder that contains p (its own
// size and all ancestors changed).
func (c *dirSizeCache) invalidate(userID string, p fsjail.VPath) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) == 0 {
		return
	}
	for d := p; ; d = d.Dir() {
		delete(c.m, c.key(userID, d))
		if d.IsRoot() {
			break
		}
	}
}

// DirSize walks a folder (SPEC §8.7 "Hitung"): files, dirs and bytes below
// p, cached DirSizeCacheTTL per path, partial after DirSizeBudget.
func (s *Service) DirSize(ctx context.Context, j *fsjail.Jail, userID string, p fsjail.VPath) (DirSizeResult, error) {
	info, err := j.Stat(p)
	if err != nil {
		return DirSizeResult{}, err
	}
	if !info.IsDir {
		return DirSizeResult{}, &ValidationError{Reason: "not_a_directory", Path: string(p), Fields: map[string]string{"path": "not_a_directory"}}
	}
	if res, ok := s.dirSizes.get(userID, p); ok {
		res.Cached = true
		return res, nil
	}
	// Inside a task (Step 11) the walk has no budget and reports items.
	wctx, cancel := ctx, context.CancelFunc(func() {})
	if !unbounded(ctx) {
		wctx, cancel = context.WithTimeout(ctx, DirSizeBudget)
	}
	defer cancel()
	rep := reporterOf(ctx)
	var last fsjail.DirStats
	st, err := j.DirSize(wctx, p, func(files, dirs, b int64) {
		if rep != nil {
			rep.Add(files + dirs - last.Files - last.Dirs)
		}
		last = fsjail.DirStats{Files: files, Dirs: dirs, Bytes: b}
	})
	res := DirSizeResult{Path: string(p), CalculatedAt: s.now().UTC().Format("2006-01-02T15:04:05.000Z")}
	switch {
	case err == nil:
		res.Size, res.Files, res.Dirs = st.Bytes, st.Files, st.Dirs
	case errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil:
		res.Size, res.Files, res.Dirs, res.Partial = last.Bytes, last.Files, last.Dirs, true
	default:
		return DirSizeResult{}, err
	}
	res.Bytes = res.Size
	if !res.Partial {
		s.dirSizes.put(userID, p, res)
	}
	s.logActivity(ctx, s.repo, userID, ActDirSize, string(p), "", map[string]any{"size": res.Size, "files": res.Files, "dirs": res.Dirs, "partial": res.Partial}, "")
	return res, nil
}

// CachedDirSize returns a cached folder size without walking (listing column).
func (s *Service) CachedDirSize(userID string, p fsjail.VPath) (DirSizeResult, bool) {
	return s.dirSizes.get(userID, p)
}
