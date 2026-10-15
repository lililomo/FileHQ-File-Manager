package archive

import (
	"context"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/text/collate"

	"kppbwi.app/filemanager/internal/listing"
)

// Archive browser index (SPEC §8.11 "Archive browser", PROMPT Step 14b):
// the member list of one archive, read once without extracting anything,
// shaped into a folder tree and cached per (user, path, size, mtime,
// encoding) for IndexTTL. Stream containers (tar.*, single files) are read
// through once; indexed containers (zip, 7z, rar) only need their headers.

// IndexTTL is how long a built index is served from memory.
const IndexTTL = 10 * time.Minute

// IndexCacheMax bounds the number of archives kept in memory at once.
const IndexCacheMax = 16

// IndexEntry is one member of the index (names are decoded, normalised and
// never contain a trailing slash).
type IndexEntry struct {
	Name           string    `json:"path"`
	Base           string    `json:"name"`
	IsDir          bool      `json:"isDir"`
	IsSymlink      bool      `json:"isSymlink,omitempty"`
	Size           int64     `json:"size"`
	CompressedSize int64     `json:"compressedSize"`
	ModTime        time.Time `json:"mtime"`
	Encrypted      bool      `json:"encrypted"`
	Method         string    `json:"method,omitempty"`
	// Implicit directories only exist because a member lives below them.
	Implicit bool `json:"implicit,omitempty"`
	// Children counts the direct members of a directory.
	Children int `json:"itemCount"`
}

// Index is the tree of one archive.
type Index struct {
	Format    Format
	Encrypted bool
	// HeaderEncrypted: the member list itself was only readable with the
	// password (7z -mhe, rar -hp). The index cache key carries no password,
	// so a cached index with this flag is refused to password-less requests
	// (review C).
	HeaderEncrypted bool
	// Encoding is the name encoding that was applied (detected for auto).
	Encoding string
	Entries  []IndexEntry
	// Unsafe counts members whose names could not be mapped to a path
	// (absolute, "..", NUL); they are not listed.
	Unsafe int
	byName map[string]int
	dirs   map[string][]int
	built  time.Time
}

// Total is the number of listed members.
func (ix *Index) Total() int { return len(ix.Entries) }

// Lookup finds a member by its inner path ("" is the root).
func (ix *Index) Lookup(inner string) (IndexEntry, bool) {
	i, ok := ix.byName[inner]
	if !ok {
		return IndexEntry{}, false
	}
	return ix.Entries[i], true
}

// IsDir reports whether inner is the root or a directory member.
func (ix *Index) IsDir(inner string) bool {
	if inner == "" {
		return true
	}
	e, ok := ix.Lookup(inner)
	return ok && e.IsDir
}

// Children returns the sorted direct members of a directory (folders first,
// natural order). ok=false when inner is not a directory.
func (ix *Index) Children(inner string) ([]IndexEntry, bool) {
	if !ix.IsDir(inner) {
		return nil, false
	}
	idx := ix.dirs[inner]
	out := make([]IndexEntry, len(idx))
	for i, n := range idx {
		out[i] = ix.Entries[n]
	}
	return out, true
}

// Members returns every member at or below inner (inner itself included
// when it is a file), in archive order: the selection an "extract these"
// request expands to.
func (ix *Index) Members(inner string) []IndexEntry {
	if inner == "" {
		return ix.Entries
	}
	var out []IndexEntry
	for _, e := range ix.Entries {
		if e.Name == inner || strings.HasPrefix(e.Name, inner+"/") {
			out = append(out, e)
		}
	}
	return out
}

// NormalizeInner maps a client path inside the archive to the index form:
// forward slashes, no leading/trailing slash, no "." or ".." segments.
func NormalizeInner(raw string) (string, bool) {
	s := strings.ReplaceAll(raw, "\\", "/")
	s = strings.Trim(s, "/")
	if s == "" {
		return "", true
	}
	if strings.ContainsRune(s, 0) {
		return "", false
	}
	parts := strings.Split(s, "/")
	out := parts[:0]
	for _, p := range parts {
		switch p {
		case "", ".":
			continue
		case "..":
			return "", false
		}
		out = append(out, p)
	}
	return strings.Join(out, "/"), true
}

// BuildIndex reads the member list of src. limit bounds the number of
// members (ARCHIVE_MAX_ENTRIES → ARCHIVE_LIMIT).
func BuildIndex(ctx context.Context, src Source, limit int) (*Index, error) {
	entries, ok, err := src.Index(ctx)
	if err != nil {
		return nil, err
	}
	if !ok {
		entries = entries[:0]
		err = src.Walk(ctx, func(e *Entry) error {
			c := *e
			c.Open = nil
			entries = append(entries, c)
			if limit > 0 && len(entries) > limit {
				return limitErr("archive has too many entries", map[string]any{"reason": "entries", "limit": limit})
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	if limit > 0 && len(entries) > limit {
		return nil, limitErr("archive has too many entries", map[string]any{"reason": "entries", "limit": limit, "entries": len(entries)})
	}
	ix := &Index{Format: src.Format(), Encrypted: src.Encrypted(), built: time.Now()}
	if d, ok := src.(interface{ Detected() string }); ok {
		ix.Encoding = d.Detected()
	}
	if h, ok := src.(interface{ HeaderEncrypted() bool }); ok && ix.Encrypted {
		ix.HeaderEncrypted = h.HeaderEncrypted()
	}
	ix.addAll(entries)
	return ix, nil
}

// addAll shapes raw entries into the tree: explicit directory members keep
// their metadata, missing parents are synthesised.
func (ix *Index) addAll(entries []Entry) {
	ix.byName = make(map[string]int, len(entries))
	ix.dirs = map[string][]int{}
	ensureDir := func(name string) {
		for p := name; p != ""; p = parentOf(p) {
			if _, ok := ix.byName[p]; ok {
				return
			}
			ix.Entries = append(ix.Entries, IndexEntry{Name: p, Base: path.Base(p), IsDir: true, Implicit: true, CompressedSize: -1})
			ix.byName[p] = len(ix.Entries) - 1
		}
	}
	for i := range entries {
		e := &entries[i]
		name, ok := NormalizeInner(e.Name)
		if !ok || name == "" {
			ix.Unsafe++
			continue
		}
		ie := IndexEntry{Name: name, Base: path.Base(name), IsDir: e.IsDir, IsSymlink: e.IsSymlink, Size: e.Size, CompressedSize: e.CompressedSize,
			ModTime: e.ModTime, Encrypted: e.Encrypted, Method: e.Method}
		if ie.IsDir {
			ie.Size = 0
		}
		if idx, dup := ix.byName[name]; dup {
			// an explicit directory arriving after its children, or a repeated
			// name: the explicit/last metadata wins, the slot stays
			if ix.Entries[idx].Implicit || !ie.IsDir {
				ie.Implicit = false
				ix.Entries[idx] = ie
			}
			continue
		}
		ensureDir(parentOf(name))
		ix.Entries = append(ix.Entries, ie)
		ix.byName[name] = len(ix.Entries) - 1
	}
	for i, e := range ix.Entries {
		p := parentOf(e.Name)
		ix.dirs[p] = append(ix.dirs[p], i)
	}
	coll := listing.NewCollator("")
	for p, idx := range ix.dirs {
		ix.sortChildren(idx, coll)
		if p != "" {
			if di, ok := ix.byName[p]; ok {
				ix.Entries[di].Children = len(idx)
			}
		}
	}
}

func (ix *Index) sortChildren(idx []int, coll *collate.Collator) {
	sort.SliceStable(idx, func(a, b int) bool {
		ea, eb := ix.Entries[idx[a]], ix.Entries[idx[b]]
		if ea.IsDir != eb.IsDir {
			return ea.IsDir
		}
		return listing.NaturalLess(ea.Base, eb.Base, coll)
	})
}

func parentOf(name string) string {
	i := strings.LastIndexByte(name, '/')
	if i < 0 {
		return ""
	}
	return name[:i]
}

// Page slices a child list by an opaque offset cursor ("o<n>").
func Page(items []IndexEntry, cursor string, limit int) (page []IndexEntry, next string, err error) {
	off := 0
	if cursor != "" {
		n, perr := strconv.Atoi(strings.TrimPrefix(cursor, "o"))
		if perr != nil || n < 0 || !strings.HasPrefix(cursor, "o") {
			return nil, "", validationErr("invalid cursor", map[string]any{"fields": map[string]any{"cursor": "invalid"}})
		}
		off = n
	}
	if off > len(items) {
		off = len(items)
	}
	end := min(off+limit, len(items))
	page = items[off:end]
	if end < len(items) {
		next = "o" + strconv.Itoa(end)
	}
	return page, next, nil
}

// indexCache keeps built indexes per user and archive version.
type indexCache struct {
	mu  sync.Mutex
	m   map[string]*Index
	ttl time.Duration
	max int
	now func() time.Time
}

func newIndexCache(now func() time.Time) *indexCache {
	return &indexCache{m: map[string]*Index{}, ttl: IndexTTL, max: IndexCacheMax, now: now}
}

func indexKey(userID, archive string, size int64, mtimeNano int64, encoding string) string {
	return userID + "\x00" + archive + "\x00" + strconv.FormatInt(size, 10) + "\x00" + strconv.FormatInt(mtimeNano, 10) + "\x00" + encoding
}

func (c *indexCache) get(key string) (*Index, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ix, ok := c.m[key]
	if !ok {
		return nil, false
	}
	if c.now().Sub(ix.built) > c.ttl {
		delete(c.m, key)
		return nil, false
	}
	return ix, true
}

func (c *indexCache) put(key string, ix *Index) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ix.built = c.now()
	c.m[key] = ix
	for len(c.m) > c.max {
		var (
			oldest  string
			oldestT time.Time
		)
		for k, v := range c.m {
			if oldest == "" || v.built.Before(oldestT) {
				oldest, oldestT = k, v.built
			}
		}
		delete(c.m, oldest)
	}
}

// invalidate drops every index of one archive path (any version) of a user.
func (c *indexCache) invalidate(userID, archive string) {
	prefix := userID + "\x00" + archive + "\x00"
	c.mu.Lock()
	defer c.mu.Unlock()
	for k := range c.m {
		if strings.HasPrefix(k, prefix) {
			delete(c.m, k)
		}
	}
}
