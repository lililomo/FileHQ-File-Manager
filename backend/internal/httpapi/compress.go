package httpapi

import (
	"bufio"
	"compress/gzip"
	"io"
	"mime"
	"net"
	"net/http"
	"strings"
	"sync"
)

// MinCompressSize is the smallest body worth gzipping (PROMPT Step 21: API
// responses > 1 KB). Below it the gzip header and CPU cost more than they
// save.
const MinCompressSize = 1024

// compressibleTypes are the media types Compress may encode. Everything else
// (downloads, media, thumbnails, SSE, NDJSON streams) passes through
// untouched so http.ServeContent keeps Range and sendfile.
var compressibleTypes = map[string]bool{
	"application/json":       true,
	"text/html":              true,
	"text/css":               true,
	"application/javascript": true,
	"text/javascript":        true,
	"image/svg+xml":          true,
}

var gzipPool = sync.Pool{New: func() any {
	w, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression) // level 6, valid by construction
	return w
}}

// Compress gzips compressible responses of at least MinCompressSize bytes
// for clients that accept gzip. The decision is taken on the first
// MinCompressSize bytes: shorter bodies go out as they are. Responses that
// already carry Content-Encoding (precompressed assets), partial content and
// HEAD requests are never touched. Brotli is left to Nginx (SPEC §12.4).
func Compress(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", "Accept-Encoding")
		if r.Method == http.MethodHead || r.Header.Get("Range") != "" || !acceptsGzip(r.Header.Get("Accept-Encoding")) {
			next.ServeHTTP(w, r)
			return
		}
		cw := &compressWriter{ResponseWriter: w}
		defer func() {
			if p := recover(); p != nil {
				// nothing buffered reaches the client: Recoverer (outside)
				// answers on the real writer
				panic(p)
			}
			cw.finish()
		}()
		next.ServeHTTP(cw, r)
	})
}

// acceptsGzip parses Accept-Encoding enough to honour "gzip;q=0".
func acceptsGzip(h string) bool {
	for part := range strings.SplitSeq(h, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		if !strings.EqualFold(strings.TrimSpace(name), "gzip") {
			continue
		}
		q := strings.ReplaceAll(strings.TrimSpace(params), " ", "")
		return q != "q=0" && q != "q=0.0" && q != "q=0.00" && q != "q=0.000"
	}
	return false
}

type compressMode int

const (
	modeUndecided compressMode = iota
	modePlain
	modeGzip
)

type compressWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool // WriteHeader seen (forwarded later)
	mode        compressMode
	buf         []byte
	gz          *gzip.Writer
}

func (cw *compressWriter) WriteHeader(code int) {
	if cw.wroteHeader {
		return
	}
	cw.wroteHeader, cw.status = true, code
	if code < 200 || code == http.StatusNoContent || code == http.StatusNotModified || code == http.StatusPartialContent || !cw.eligible() {
		cw.plain()
	}
}

// eligible reports whether the headers allow compression at all.
func (cw *compressWriter) eligible() bool {
	h := cw.Header()
	if h.Get("Content-Encoding") != "" || h.Get("Content-Range") != "" {
		return false
	}
	mt, _, err := mime.ParseMediaType(h.Get("Content-Type"))
	return err == nil && compressibleTypes[mt]
}

func (cw *compressWriter) Write(p []byte) (int, error) {
	if !cw.wroteHeader {
		if cw.Header().Get("Content-Type") == "" {
			// net/http would sniff the type on the first write; do it here
			// so the eligibility check sees the same type
			cw.Header().Set("Content-Type", http.DetectContentType(p))
		}
		cw.WriteHeader(http.StatusOK)
	}
	switch cw.mode {
	case modePlain:
		return cw.ResponseWriter.Write(p)
	case modeGzip:
		return cw.gz.Write(p)
	}
	cw.buf = append(cw.buf, p...)
	if len(cw.buf) >= MinCompressSize {
		if err := cw.startGzip(); err != nil {
			return 0, err
		}
	}
	return len(p), nil
}

// plain commits to an unencoded response and forwards the status.
func (cw *compressWriter) plain() {
	cw.mode = modePlain
	cw.ResponseWriter.WriteHeader(cw.status)
}

func (cw *compressWriter) startGzip() error {
	cw.mode = modeGzip
	h := cw.Header()
	h.Del("Content-Length")
	h.Set("Content-Encoding", "gzip")
	if et := h.Get("ETag"); et != "" && !strings.HasPrefix(et, "W/") {
		h.Set("ETag", "W/"+et) // the encoded body is a different representation
	}
	cw.ResponseWriter.WriteHeader(cw.status)
	cw.gz = gzipPool.Get().(*gzip.Writer)
	cw.gz.Reset(cw.ResponseWriter)
	buf := cw.buf
	cw.buf = nil
	_, err := cw.gz.Write(buf)
	return err
}

// finish flushes whatever the handler left: a short body goes out plain.
func (cw *compressWriter) finish() {
	switch cw.mode {
	case modeUndecided:
		if !cw.wroteHeader {
			return // handler wrote nothing; net/http sends the default 200
		}
		cw.plain()
		if len(cw.buf) > 0 {
			_, _ = cw.ResponseWriter.Write(cw.buf)
		}
	case modeGzip:
		_ = cw.gz.Close()
		cw.gz.Reset(io.Discard)
		gzipPool.Put(cw.gz)
		cw.gz = nil
	}
}

// Flush commits the buffered prefix (gzip if it is already long enough) and
// flushes through to the client, so streaming handlers keep working.
func (cw *compressWriter) Flush() {
	if !cw.wroteHeader {
		// a flush before any WriteHeader commits 200 (as net/http itself
		// would); keep the bookkeeping in step so the handler's next Write
		// does not send the status a second time
		cw.WriteHeader(http.StatusOK)
	}
	if cw.mode == modeUndecided {
		if len(cw.buf) >= MinCompressSize {
			_ = cw.startGzip()
		} else {
			buf := cw.buf
			cw.buf = nil
			cw.plain()
			_, _ = cw.ResponseWriter.Write(buf)
		}
	}
	if cw.mode == modeGzip {
		_ = cw.gz.Flush()
	}
	_ = http.NewResponseController(cw.ResponseWriter).Flush()
}

// ReadFrom keeps io.Copy (http.ServeContent) on the underlying writer's fast
// path when the response is not being compressed.
func (cw *compressWriter) ReadFrom(r io.Reader) (int64, error) {
	if !cw.wroteHeader {
		cw.WriteHeader(http.StatusOK)
	}
	if cw.mode == modePlain {
		if rf, ok := cw.ResponseWriter.(io.ReaderFrom); ok {
			return rf.ReadFrom(r)
		}
		return io.Copy(cw.ResponseWriter, r)
	}
	return io.Copy(writerOnly{cw}, r)
}

// Hijack is forwarded for completeness (no route upgrades today).
func (cw *compressWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(cw.ResponseWriter).Hijack()
}

// Unwrap lets http.ResponseController reach deadlines on the real writer.
func (cw *compressWriter) Unwrap() http.ResponseWriter { return cw.ResponseWriter }

// writerOnly hides ReadFrom so io.Copy does not recurse into it.
type writerOnly struct{ io.Writer }
