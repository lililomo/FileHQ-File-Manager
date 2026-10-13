package listing

import (
	"strconv"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/fsjail"
)

func mkItems(n int) []*Item {
	out := make([]*Item, n)
	for i := range n {
		out[i] = &Item{Name: "f" + strconv.Itoa(i), Path: "/f" + strconv.Itoa(i)}
	}
	return out
}

func TestCacheTTLAndMtime(t *testing.T) {
	now := time.Unix(1000, 0)
	c := NewCache(1<<20, 15*time.Second, func() time.Time { return now })
	mt := time.Unix(900, 0)
	c.Put("u|s|/|k", "u", "s", "/", mkItems(3), mt)
	if got, ok := c.Get("u|s|/|k", mt); !ok || len(got) != 3 {
		t.Fatal("expected hit")
	}
	if _, ok := c.Get("u|s|/|k", mt.Add(time.Nanosecond)); ok {
		t.Fatal("mtime change must miss")
	}
	// The miss above evicted the entry; put again and expire by TTL.
	c.Put("u|s|/|k", "u", "s", "/", mkItems(3), mt)
	now = now.Add(16 * time.Second)
	if _, ok := c.Get("u|s|/|k", mt); ok {
		t.Fatal("expired entry served")
	}
	entries, _, hits, misses := c.Stats()
	if entries != 0 || hits != 1 || misses != 2 {
		t.Fatalf("stats entries=%d hits=%d misses=%d", entries, hits, misses)
	}
}

func TestCacheInvalidate(t *testing.T) {
	c := NewCache(1<<20, time.Minute, nil)
	mt := time.Unix(1, 0)
	c.Put("u|s|/a|name", "u", "s", "/a", mkItems(2), mt)
	c.Put("u|s|/a|size", "u", "s", "/a", mkItems(2), mt)
	c.Put("u|s|/b|name", "u", "s", "/b", mkItems(2), mt)
	c.Put("v|s|/a|name", "v", "s", "/a", mkItems(2), mt)
	c.Invalidate("u", "s", "/a")
	if _, ok := c.Get("u|s|/a|name", mt); ok {
		t.Fatal("/a name still cached")
	}
	if _, ok := c.Get("u|s|/a|size", mt); ok {
		t.Fatal("/a size still cached")
	}
	if _, ok := c.Get("u|s|/b|name", mt); !ok {
		t.Fatal("/b dropped")
	}
	if _, ok := c.Get("v|s|/a|name", mt); !ok {
		t.Fatal("other user dropped")
	}
	c.InvalidateUser("u")
	if _, ok := c.Get("u|s|/b|name", mt); ok {
		t.Fatal("user invalidation incomplete")
	}
	if _, ok := c.Get("v|s|/a|name", mt); !ok {
		t.Fatal("other user dropped by InvalidateUser")
	}
}

func TestCacheLRU(t *testing.T) {
	// Each 10-item listing ≈ 10*(360+~5) + 64 bytes; budget fits ~3 of them.
	c := NewCache(12_000, time.Minute, nil)
	mt := time.Unix(1, 0)
	for i := range 5 {
		c.Put("u|s|/d"+strconv.Itoa(i)+"|k", "u", "s", fsjail.VPath("/d"+strconv.Itoa(i)), mkItems(10), mt)
	}
	entries, bytes, _, _ := c.Stats()
	if entries >= 5 || bytes > 12_000 {
		t.Fatalf("eviction failed: entries=%d bytes=%d", entries, bytes)
	}
	if _, ok := c.Get("u|s|/d0|k", mt); ok {
		t.Fatal("oldest entry should have been evicted")
	}
	if _, ok := c.Get("u|s|/d4|k", mt); !ok {
		t.Fatal("newest entry missing")
	}
	// Oversized listings are not cached at all.
	c.Put("u|s|/huge|k", "u", "s", "/huge", mkItems(1000), mt)
	if _, ok := c.Get("u|s|/huge|k", mt); ok {
		t.Fatal("oversized listing cached")
	}
}
