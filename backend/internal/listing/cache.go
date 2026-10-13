package listing

import (
	"container/list"
	"sync"
	"time"

	"kppbwi.app/filemanager/internal/fsjail"
)

// Cache defaults (SPEC §8.3 step 2).
const (
	DefaultCacheTTL   = 15 * time.Second
	DefaultCacheBytes = 64 << 20
)

// Cache is an LRU of sorted listings keyed by (user, scope, dir, sort,
// locale). An entry is valid while it is younger than the TTL, the folder's
// mtime is unchanged and nobody invalidated the folder.
type Cache struct {
	mu       sync.Mutex
	ttl      time.Duration
	maxBytes int64
	used     int64
	ll       *list.List
	byKey    map[string]*list.Element
	byDir    map[string]map[string]struct{} // user|scope|dir → keys
	now      func() time.Time
	hits     int64
	misses   int64
}

type cacheEntry struct {
	key      string
	dirKey   string
	items    []*Item
	dirMtime time.Time
	builtAt  time.Time
	bytes    int64
}

// NewCache creates a cache; maxBytes ≤ 0 and ttl ≤ 0 use the defaults.
func NewCache(maxBytes int64, ttl time.Duration, now func() time.Time) *Cache {
	if maxBytes <= 0 {
		maxBytes = DefaultCacheBytes
	}
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	if now == nil {
		now = time.Now
	}
	return &Cache{ttl: ttl, maxBytes: maxBytes, ll: list.New(), byKey: map[string]*list.Element{},
		byDir: map[string]map[string]struct{}{}, now: now}
}

func dirKey(user, scope string, dir fsjail.VPath) string {
	return user + "|" + scope + "|" + string(dir)
}

// Get returns the cached sorted items when still valid.
func (c *Cache) Get(key string, dirMtime time.Time) ([]*Item, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.byKey[key]
	if !ok {
		c.misses++
		return nil, false
	}
	e := el.Value.(*cacheEntry)
	if !e.dirMtime.Equal(dirMtime) || c.now().Sub(e.builtAt) > c.ttl {
		c.removeLocked(el)
		c.misses++
		return nil, false
	}
	c.ll.MoveToFront(el)
	c.hits++
	return e.items, true
}

// Put stores a sorted listing, evicting least recently used entries past
// the byte budget. Listings larger than the whole budget are not cached.
func (c *Cache) Put(key, user, scope string, dir fsjail.VPath, items []*Item, dirMtime time.Time) {
	size := int64(64)
	for _, it := range items {
		size += itemBytes(it)
	}
	if size > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.byKey[key]; ok {
		c.removeLocked(el)
	}
	e := &cacheEntry{key: key, dirKey: dirKey(user, scope, dir), items: items, dirMtime: dirMtime, builtAt: c.now(), bytes: size}
	el := c.ll.PushFront(e)
	c.byKey[key] = el
	if c.byDir[e.dirKey] == nil {
		c.byDir[e.dirKey] = map[string]struct{}{}
	}
	c.byDir[e.dirKey][key] = struct{}{}
	c.used += size
	for c.used > c.maxBytes && c.ll.Len() > 0 {
		c.removeLocked(c.ll.Back())
	}
}

// Invalidate drops every cached ordering of one folder (called by all
// mutations: mkdir, create, rename, move, delete, upload, extract…).
func (c *Cache) Invalidate(user, scope string, dir fsjail.VPath) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.byDir[dirKey(user, scope, dir)] {
		if el, ok := c.byKey[key]; ok {
			c.removeLocked(el)
		}
	}
}

// InvalidateUser drops everything cached for a user (quota recalculation,
// deletion, external changes reported by the integrity check).
func (c *Cache) InvalidateUser(user string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for el := c.ll.Front(); el != nil; {
		next := el.Next()
		if e := el.Value.(*cacheEntry); len(e.key) > len(user) && e.key[:len(user)+1] == user+"|" {
			c.removeLocked(el)
		}
		el = next
	}
}

func (c *Cache) removeLocked(el *list.Element) {
	e := el.Value.(*cacheEntry)
	c.ll.Remove(el)
	delete(c.byKey, e.key)
	if m := c.byDir[e.dirKey]; m != nil {
		delete(m, e.key)
		if len(m) == 0 {
			delete(c.byDir, e.dirKey)
		}
	}
	c.used -= e.bytes
}

// Stats reports entries, bytes, hits and misses (admin dashboard, tests).
func (c *Cache) Stats() (entries int, bytes, hits, misses int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ll.Len(), c.used, c.hits, c.misses
}

// itemBytes estimates the memory of one Item (struct + strings + sort key).
func itemBytes(it *Item) int64 {
	n := int64(360) + int64(len(it.Name)) + int64(len(it.Path)) + int64(len(it.TypeLabel)) + int64(len(it.folded))
	for _, t := range it.nameKey {
		n += 24 + int64(len(t.digits)) + int64(len(t.key))
	}
	return n
}
