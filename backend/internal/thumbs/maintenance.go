package thumbs

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"sort"
	"syscall"
	"time"

	"kppbwi.app/filemanager/internal/fsjail"
)

// Cache maintenance (Step 19b, SPEC §8.17 "clear-thumbs"): drop one user's
// or every cached thumbnail, and keep the cache under a size budget by
// removing the least recently used files (weekly job). Rendering is lazy,
// so a cleared thumbnail is produced again the next time it is visible.

// DefaultCacheBudget is the LRU limit of the weekly prune (2 GB).
const DefaultCacheBudget int64 = 2 << 30

// CacheStats summarises one maintenance pass.
type CacheStats struct {
	Files        int64 `json:"files"`
	Bytes        int64 `json:"bytes"`
	RemovedFiles int64 `json:"removedFiles"`
	FreedBytes   int64 `json:"freedBytes"`
}

type cacheFile struct {
	rel  string
	size int64
	used time.Time
}

// scan walks the cache (below sub, "." = everything).
func (s *Service) scan(ctx context.Context, sub string) ([]cacheFile, error) {
	var out []cacheFile
	err := fs.WalkDir(s.sys.FS(), sub, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil //nolint:nilerr // vanished while walking
		}
		out = append(out, cacheFile{rel: p, size: info.Size(), used: lastUse(info)})
		return nil
	})
	return out, err
}

// lastUse is max(atime, mtime): relatime still refreshes atime daily.
func lastUse(info os.FileInfo) time.Time {
	t := info.ModTime()
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		if at := time.Unix(st.Atim.Sec, st.Atim.Nsec); at.After(t) {
			t = at
		}
	}
	return t
}

// Clear removes every cached thumbnail of userID ("" = all users).
func (s *Service) Clear(ctx context.Context, userID string) (CacheStats, error) {
	sub := "."
	if userID != "" {
		sub = userID
	}
	files, err := s.scan(ctx, sub)
	if err != nil {
		return CacheStats{}, err
	}
	st := CacheStats{}
	for _, f := range files {
		st.Files++
		st.Bytes += f.size
	}
	var names []string
	if userID != "" {
		names = []string{userID}
	} else if names, err = s.sys.ReadDirNames("."); err != nil {
		return st, err
	}
	for _, n := range names {
		if ctx.Err() != nil {
			return st, ctx.Err()
		}
		if err := s.sys.RemoveAll(n); err != nil && !errors.Is(err, fsjail.ErrNotFound) {
			return st, err
		}
	}
	st.RemovedFiles, st.FreedBytes = st.Files, st.Bytes
	return st, nil
}

// Prune removes least recently used thumbnails until the cache holds at
// most 90 % of budget (hysteresis); nothing happens below budget.
func (s *Service) Prune(ctx context.Context, budget int64) (CacheStats, error) {
	if budget <= 0 {
		budget = DefaultCacheBudget
	}
	files, err := s.scan(ctx, ".")
	if err != nil {
		return CacheStats{}, err
	}
	st := CacheStats{}
	for _, f := range files {
		st.Files++
		st.Bytes += f.size
	}
	if st.Bytes <= budget {
		return st, nil
	}
	sort.Slice(files, func(i, k int) bool { return files[i].used.Before(files[k].used) })
	target := budget / 10 * 9
	left := st.Bytes
	for _, f := range files {
		if left <= target {
			break
		}
		if ctx.Err() != nil {
			return st, ctx.Err()
		}
		if err := s.sys.Remove(f.rel); err != nil && !errors.Is(err, fsjail.ErrNotFound) && !errors.Is(err, os.ErrNotExist) {
			continue
		}
		left -= f.size
		st.RemovedFiles++
		st.FreedBytes += f.size
	}
	return st, nil
}
