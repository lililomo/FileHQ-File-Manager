package download

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"kppbwi.app/filemanager/internal/fsjail"
)

// Response headers shared by raw/download/zip (SPEC §8.9, §10.3).
const (
	// RawCSP isolates any user-derived bytes served from the app origin.
	RawCSP = "sandbox; default-src 'none'"
	// CacheControl for single files: resumable but always revalidated.
	CacheControl = "private, max-age=0, must-revalidate"
)

// ETag is the strong validator of a file version: hash(inode, size, mtime).
func ETag(info fsjail.Info) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("%d|%d|%d|%d", info.Dev, info.Ino, info.Size, info.ModTime.UnixNano())))
	return `"` + hex.EncodeToString(h[:16]) + `"`
}

// SetContentHeaders writes the common headers of every content response.
// crossOriginResourcePolicy is "same-origin" or "same-site" (CONTENT_BASE_URL).
func SetContentHeaders(h http.Header, crossOriginResourcePolicy string) {
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", RawCSP)
	h.Set("Referrer-Policy", "no-referrer")
	h.Set("Cross-Origin-Resource-Policy", crossOriginResourcePolicy)
	h.Set("X-Accel-Buffering", "no")
}

// ServeFile streams f with http.ServeContent (Range, If-Range, If-None-Match,
// HEAD). Content-Type is fixed beforehand so ServeContent never sniffs.
func ServeFile(w http.ResponseWriter, r *http.Request, f *os.File, info fsjail.Info, d Disposition, filename string) {
	h := w.Header()
	h.Set("Content-Type", d.ContentType)
	h.Set("Content-Disposition", ContentDisposition(d.Inline, filename))
	h.Set("Cache-Control", CacheControl)
	h.Set("Accept-Ranges", "bytes")
	h.Set("ETag", ETag(info))
	mod := info.ModTime
	if mod.IsZero() {
		mod = time.Now()
	}
	http.ServeContent(w, r, "", mod, f)
}

// SniffMIME reads the sniff window from the start of f and rewinds it.
func SniffMIME(f io.ReadSeeker, detect func(io.Reader) (string, error)) (string, error) {
	mt, err := detect(io.LimitReader(f, 3072))
	if _, serr := f.Seek(0, io.SeekStart); serr != nil {
		return "", serr
	}
	return mt, err
}
