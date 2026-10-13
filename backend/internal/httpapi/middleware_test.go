package httpapi

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/config"
)

func testConfig(t *testing.T) *config.Config {
	t.Helper()
	storage := filepath.Join(t.TempDir(), "storage")
	if err := os.MkdirAll(storage, 0o750); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(config.Options{
		Environ: []string{
			"APP_ENV=test",
			"APP_BASE_URL=https://file.example.test",
			"APP_SECRET=" + strings.Repeat("cd", 32),
			"STORAGE_ROOT=" + storage,
			"DATA_DIR=" + filepath.Join(storage, ".system"),
			"TRUSTED_PROXIES=127.0.0.1,10.0.0.0/8",
		},
		Cwd: "/opt/app", EUID: 1000, NumCPU: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func testServer(t *testing.T, cfg *config.Config, logBuf *bytes.Buffer) http.Handler {
	t.Helper()
	if logBuf == nil {
		logBuf = &bytes.Buffer{}
	}
	logger := config.NewLogger(logBuf, cfg.AppEnv, slog.LevelDebug)
	return New(Options{Cfg: cfg, Logger: logger, Version: "test", Assets: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte("<html>spa</html>"))
	})})
}

func do(h http.Handler, method, target, remote string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.RemoteAddr = remote
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) ErrorBody {
	t.Helper()
	var body ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON envelope: %v: %s", err, rec.Body.String())
	}
	return body
}

func TestRequestIDTrustedVsUntrusted(t *testing.T) {
	h := testServer(t, testConfig(t), nil)
	rec := do(h, "GET", "/healthz", "127.0.0.1:5000", map[string]string{"X-Request-ID": "proxy-abc.1"})
	if got := rec.Header().Get("X-Request-ID"); got != "proxy-abc.1" {
		t.Errorf("trusted proxy id not honored: %q", got)
	}
	rec = do(h, "GET", "/healthz", "203.0.113.9:5000", map[string]string{"X-Request-ID": "evil"})
	got := rec.Header().Get("X-Request-ID")
	if got == "evil" || len(got) != 32 {
		t.Errorf("untrusted id must be replaced by generated 32-hex: %q", got)
	}
	rec = do(h, "GET", "/healthz", "127.0.0.1:5000", map[string]string{"X-Request-ID": "bad id with spaces"})
	if got := rec.Header().Get("X-Request-ID"); got == "bad id with spaces" {
		t.Error("malformed id must be rejected even from trusted proxy")
	}
}

func TestRealIPOnlyFromTrustedProxy(t *testing.T) {
	cfg := testConfig(t)
	var seen string
	h := New(Options{Cfg: cfg, Logger: slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), Version: "t",
		Assets: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = ClientIP(r) })})
	do(h, "GET", "/x", "127.0.0.1:1", map[string]string{"X-Forwarded-For": "198.51.100.7, 10.1.1.1"})
	if seen != "198.51.100.7" {
		t.Errorf("expected right-most untrusted hop, got %q", seen)
	}
	do(h, "GET", "/x", "203.0.113.9:1", map[string]string{"X-Forwarded-For": "198.51.100.7"})
	if seen != "203.0.113.9" {
		t.Errorf("untrusted peer must not be able to spoof XFF, got %q", seen)
	}
}

func TestSecurityHeadersAndHSTS(t *testing.T) {
	h := testServer(t, testConfig(t), nil)
	rec := do(h, "GET", "/healthz", "203.0.113.9:1", nil)
	for k, v := range map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	} {
		if got := rec.Header().Get(k); got != v {
			t.Errorf("%s = %q want %q", k, got, v)
		}
	}
	if !strings.Contains(rec.Header().Get("Content-Security-Policy"), "default-src 'self'") {
		t.Error("CSP missing")
	}
	if rec.Header().Get("Permissions-Policy") == "" {
		t.Error("Permissions-Policy missing")
	}
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("HSTS must not be set for plain HTTP from untrusted peer")
	}
	rec = do(h, "GET", "/healthz", "127.0.0.1:1", map[string]string{"X-Forwarded-Proto": "https"})
	if rec.Header().Get("Strict-Transport-Security") == "" {
		t.Error("HSTS expected when trusted proxy says https")
	}
	rec = do(h, "GET", "/healthz", "203.0.113.9:1", map[string]string{"X-Forwarded-Proto": "https"})
	if rec.Header().Get("Strict-Transport-Security") != "" {
		t.Error("X-Forwarded-Proto from untrusted peer must be ignored")
	}
}

func TestErrorEnvelopes(t *testing.T) {
	h := testServer(t, testConfig(t), nil)
	rec := do(h, "GET", "/api/v1/does-not-exist", "127.0.0.1:1", nil)
	if rec.Code != 404 || decodeErr(t, rec).Error.Code != CodeNotFound {
		t.Errorf("404 envelope: %d %s", rec.Code, rec.Body.String())
	}
	if decodeErr(t, rec).Error.Details["requestId"] == "" {
		t.Error("requestId missing from details")
	}
	rec = do(h, "DELETE", "/api/v1/version", "127.0.0.1:1", nil)
	if rec.Code != 405 || decodeErr(t, rec).Error.Code != CodeMethodNotAllowed {
		t.Errorf("405 envelope: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(h, "GET", "/spa/route", "127.0.0.1:1", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "spa") {
		t.Errorf("non-API route should fall back to SPA: %d", rec.Code)
	}
}

func TestRecovererHidesPanic(t *testing.T) {
	cfg := testConfig(t)
	var logBuf bytes.Buffer
	logger := config.NewLogger(&logBuf, cfg.AppEnv, slog.LevelDebug)
	r := chi.NewRouter()
	r.Use(TrustedPeer(cfg), RequestID, Recoverer(logger))
	r.Get("/boom", func(http.ResponseWriter, *http.Request) { panic("secret-internal-detail") })
	rec := do(r, "GET", "/boom", "127.0.0.1:1", nil)
	if rec.Code != 500 {
		t.Fatalf("status %d", rec.Code)
	}
	body := decodeErr(t, rec)
	if body.Error.Code != CodeInternal || strings.Contains(rec.Body.String(), "secret-internal-detail") {
		t.Errorf("panic detail leaked or wrong code: %s", rec.Body.String())
	}
	if !strings.Contains(logBuf.String(), "secret-internal-detail") || !strings.Contains(logBuf.String(), "stack") {
		t.Error("panic must be logged with stack")
	}
}

func TestMaxBytes(t *testing.T) {
	cfg := testConfig(t)
	r := chi.NewRouter()
	r.Use(TrustedPeer(cfg), RequestID, MaxBytes(1<<20))
	r.Post("/echo", func(w http.ResponseWriter, r *http.Request) {
		var v map[string]any
		if !ReadJSON(w, r, &v) {
			return
		}
		WriteJSON(w, 200, v)
	})
	big := `{"a":"` + strings.Repeat("x", 2<<20) + `"}`
	req := httptest.NewRequest("POST", "/echo", strings.NewReader(big))
	req.RemoteAddr = "127.0.0.1:1"
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 413 || decodeErr(t, rec).Error.Code != CodePayloadTooLarge {
		t.Errorf("expected 413 PAYLOAD_TOO_LARGE, got %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest("POST", "/echo", strings.NewReader(`{"a":1}`))
	req.RemoteAddr = "127.0.0.1:1"
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Errorf("small body should pass: %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest("POST", "/echo", strings.NewReader(`{bad json`))
	req.RemoteAddr = "127.0.0.1:1"
	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != 400 || decodeErr(t, rec).Error.Code != CodeValidationFailed {
		t.Errorf("invalid json: %d %s", rec.Code, rec.Body.String())
	}
}

func TestAccessLogOmitsQuery(t *testing.T) {
	cfg := testConfig(t)
	var logBuf bytes.Buffer
	h := testServer(t, cfg, &logBuf)
	do(h, "GET", "/healthz?sig=SECRETSIG&exp=1", "127.0.0.1:1", nil)
	out := logBuf.String()
	if !strings.Contains(out, `"path":"/healthz"`) || strings.Contains(out, "SECRETSIG") {
		t.Errorf("access log must log path without query: %s", out)
	}
	for _, k := range []string{`"method"`, `"status"`, `"duration_ms"`, `"bytes"`, `"request_id"`} {
		if !strings.Contains(out, k) {
			t.Errorf("access log missing %s: %s", k, out)
		}
	}
}
