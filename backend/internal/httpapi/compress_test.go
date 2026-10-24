package httpapi

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func compressRecord(t *testing.T, h http.HandlerFunc, method, accept string, hdr map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/x", nil)
	if accept != "" {
		req.Header.Set("Accept-Encoding", accept)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	Compress(h).ServeHTTP(rec, req)
	return rec
}

func gunzip(t *testing.T, b []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func jsonOf(n int) http.HandlerFunc {
	body := `{"x":"` + strings.Repeat("a", n) + `"}`
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("ETag", `"abc"`)
		_, _ = io.WriteString(w, body)
	}
}

func TestCompressLargeJSON(t *testing.T) {
	rec := compressRecord(t, jsonOf(5000), http.MethodGet, "br, gzip", nil)
	if rec.Header().Get("Content-Encoding") != "gzip" || rec.Header().Get("Content-Length") != "" {
		t.Fatalf("headers %v", rec.Header())
	}
	if !strings.Contains(rec.Header().Get("Vary"), "Accept-Encoding") || rec.Header().Get("ETag") != `W/"abc"` {
		t.Fatalf("vary/etag %v", rec.Header())
	}
	if got := gunzip(t, rec.Body.Bytes()); len(got) != 5008 {
		t.Fatalf("decoded %d bytes", len(got))
	}
	if rec.Body.Len() > 200 {
		t.Fatalf("not compressed: %d bytes", rec.Body.Len())
	}
}

func TestCompressSkips(t *testing.T) {
	big := strings.Repeat("z", 4000)
	cases := map[string]struct {
		h      http.HandlerFunc
		method string
		accept string
		hdr    map[string]string
	}{
		"small json":       {jsonOf(100), http.MethodGet, "gzip", nil},
		"no gzip accepted": {jsonOf(5000), http.MethodGet, "br", nil},
		"gzip q=0":         {jsonOf(5000), http.MethodGet, "gzip;q=0, identity", nil},
		"head":             {jsonOf(5000), http.MethodHead, "gzip", nil},
		"range request":    {jsonOf(5000), http.MethodGet, "gzip", map[string]string{"Range": "bytes=0-10"}},
		"binary": {func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = io.Copy(w, strings.NewReader(big)) // ReadFrom path
		}, http.MethodGet, "gzip", nil},
		"already encoded": {func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/javascript")
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = io.WriteString(w, big)
		}, http.MethodGet, "gzip", nil},
		"partial content": {func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/css")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.WriteString(w, big)
		}, http.MethodGet, "gzip", nil},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			rec := compressRecord(t, c.h, c.method, c.accept, c.hdr)
			if ce := rec.Header().Get("Content-Encoding"); ce == "gzip" && name != "already encoded" {
				t.Fatalf("compressed unexpectedly")
			}
			if c.method != http.MethodHead && name != "already encoded" && strings.Contains(rec.Body.String(), "\x1f\x8b") {
				t.Fatalf("gzip magic in plain body")
			}
		})
	}
}

func TestCompressStatusAndEmptyBodies(t *testing.T) {
	rec := compressRecord(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
	}, http.MethodGet, "gzip", nil)
	if rec.Code != http.StatusCreated || rec.Body.Len() != 0 || rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("201 empty: %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	rec = compressRecord(t, func(w http.ResponseWriter, _ *http.Request) {
		WriteJSON(w, http.StatusBadRequest, map[string]string{"error": strings.Repeat("e", 3000)})
	}, http.MethodGet, "gzip", nil)
	if rec.Code != http.StatusBadRequest || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("400 large: %d %v", rec.Code, rec.Header())
	}
	// sniffed type: a large HTML body without Content-Type is compressed like net/http would type it
	rec = compressRecord(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "<!doctype html><html>"+strings.Repeat("<p>x</p>", 500))
	}, http.MethodGet, "gzip", nil)
	if !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") || rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("sniffed html: %v", rec.Header())
	}
}

func TestCompressFlushStreams(t *testing.T) {
	rec := compressRecord(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[1`)
		_ = http.NewResponseController(w).Flush() // short prefix → committed plain
		_, _ = io.WriteString(w, strings.Repeat(",1", 2000)+"]")
	}, http.MethodGet, "gzip", nil)
	if rec.Header().Get("Content-Encoding") != "" || !rec.Flushed || !strings.HasPrefix(rec.Body.String(), "[1,1") {
		t.Fatalf("flush short: %v flushed=%v", rec.Header(), rec.Flushed)
	}
	rec = compressRecord(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "["+strings.Repeat("1,", 1000))
		_ = http.NewResponseController(w).Flush()
		_, _ = io.WriteString(w, "1]")
	}, http.MethodGet, "gzip", nil)
	if rec.Header().Get("Content-Encoding") != "gzip" || !strings.HasSuffix(gunzip(t, rec.Body.Bytes()), "1,1]") {
		t.Fatalf("flush long: %v", rec.Header())
	}
}

// headerCounter fails the test if the status line is sent twice (net/http
// logs "superfluous response.WriteHeader call" and keeps the first one).
type headerCounter struct {
	*httptest.ResponseRecorder
	writes int
}

func (h *headerCounter) WriteHeader(code int) {
	h.writes++
	h.ResponseRecorder.WriteHeader(code)
}

// Flush commits an implicit 200 like net/http does.
func (h *headerCounter) Flush() {
	if h.writes == 0 {
		h.writes++
	}
	h.ResponseRecorder.Flush()
}

func TestCompressFlushBeforeHeaderCommitsOnce(t *testing.T) {
	// an SSE-style handler that flushes before writing anything (status
	// implied 200) must not trigger a second WriteHeader on its first write
	req := httptest.NewRequest(http.MethodGet, "/events", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	rec := &headerCounter{ResponseRecorder: httptest.NewRecorder()}
	Compress(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		_ = http.NewResponseController(w).Flush()
		_, _ = io.WriteString(w, "data: "+strings.Repeat("x", 2000)+"\n\n")
		_ = http.NewResponseController(w).Flush()
	})).ServeHTTP(rec, req)
	if rec.writes != 1 || rec.Code != http.StatusOK || !rec.Flushed {
		t.Fatalf("WriteHeader calls=%d code=%d flushed=%v", rec.writes, rec.Code, rec.Flushed)
	}
	if rec.Header().Get("Content-Encoding") != "" || !strings.HasPrefix(rec.Body.String(), "data: xxx") {
		t.Fatalf("event stream must stay plain: %v", rec.Header())
	}
}

func TestCompressPanicReachesRecoverer(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("panic swallowed")
		}
	}()
	compressRecord(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{")
		panic("boom")
	}, http.MethodGet, "gzip", nil)
}

func TestAcceptsGzip(t *testing.T) {
	for h, want := range map[string]bool{
		"gzip": true, "GZIP": true, "deflate, gzip;q=0.5": true, "gzip;q=0": false,
		"gzip; q=0.000": false, "br": false, "": false, "x-gzip": false,
	} {
		if got := acceptsGzip(h); got != want {
			t.Errorf("acceptsGzip(%q)=%v", h, got)
		}
	}
}
