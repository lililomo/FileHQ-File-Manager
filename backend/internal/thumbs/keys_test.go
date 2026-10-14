package thumbs

import (
	"strings"
	"testing"
)

func TestKeyAndPaths(t *testing.T) {
	k1 := Key("/a/b.jpg", 10, 1000)
	k2 := Key("/a/b.jpg", 10, 1001)
	k3 := Key("/a/b.jpg", 11, 1000)
	k4 := Key("/a/c.jpg", 10, 1000)
	if len(k1) != 64 || k1 == k2 || k1 == k3 || k1 == k4 {
		t.Fatalf("keys must differ per path/size/mtime: %s %s %s %s", k1, k2, k3, k4)
	}
	if k1 != Key("/a/b.jpg", 10, 1000) {
		t.Fatal("key must be deterministic")
	}
	rel := CacheRel("u1", k1, Size256)
	if !strings.HasPrefix(rel, "u1/"+k1[:2]+"/") || !strings.HasSuffix(rel, "-256.webp") || strings.Contains(rel, "..") {
		t.Fatalf("rel = %s", rel)
	}
	if ETag(k1, Size128) != `"`+k1[:32]+`-128"` {
		t.Fatalf("etag = %s", ETag(k1, Size128))
	}
}

func TestParseSize(t *testing.T) {
	for _, ok := range []string{"128", "256", "512"} {
		if _, valid := ParseSize(ok); !valid {
			t.Errorf("%s rejected", ok)
		}
	}
	for _, bad := range []string{"", "0", "64", "1024", "abc", "-128"} {
		if _, valid := ParseSize(bad); valid {
			t.Errorf("%q accepted", bad)
		}
	}
}
