// Package thumbs is the server-side thumbnail pipeline (SPEC §8.4, ADR-09):
// a bounded worker pool renders WebP thumbnails with vipsthumbnail, ffmpeg
// and pdftoppm into DATA_DIR/thumbs, keyed by (path, size, mtime), and the
// HTTP layer serves them with ETag/304.
package thumbs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
)

// Size is a thumbnail edge length in pixels.
type Size int

// Supported sizes (SPEC §8.4).
const (
	Size128 Size = 128
	Size256 Size = 256
	Size512 Size = 512
)

// Sizes lists every supported size (a job renders all of them at once).
var Sizes = []Size{Size128, Size256, Size512}

// SizePreview is the large inline variant of GET /files/raw?variant=preview
// (Step 12b). It is rendered on demand for raster images only, never by the
// grid pipeline, so ParseSize does not accept it.
const SizePreview Size = 2048

// ParseSize validates the size= query parameter.
func ParseSize(s string) (Size, bool) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	for _, sz := range Sizes {
		if Size(n) == sz {
			return sz, true
		}
	}
	return 0, false
}

// Key is the cache key of a file version: sha256(path|size|mtime_ns).
func Key(path string, fileSize int64, mtimeNano int64) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", path, fileSize, mtimeNano)))
	return hex.EncodeToString(h[:])
}

// CacheDir is the per-user shard folder of a key inside DATA_DIR/thumbs.
func CacheDir(userID, key string) string { return userID + "/" + key[:2] }

// CacheRel is the cache file of (key, size), relative to DATA_DIR/thumbs.
func CacheRel(userID, key string, size Size) string {
	return CacheDir(userID, key) + "/" + key + "-" + strconv.Itoa(int(size)) + ".webp"
}

// ETag is the strong validator served with a thumbnail.
func ETag(key string, size Size) string {
	return `"` + key[:32] + "-" + strconv.Itoa(int(size)) + `"`
}
