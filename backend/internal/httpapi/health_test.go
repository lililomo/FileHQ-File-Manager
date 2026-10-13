package httpapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestHealthz(t *testing.T) {
	h := testServer(t, testConfig(t), nil)
	rec := do(h, "GET", "/healthz", "127.0.0.1:1", nil)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" || body["version"] != "test" {
		t.Errorf("body %v", body)
	}
}

func TestReadyzOK(t *testing.T) {
	h := testServer(t, testConfig(t), nil)
	rec := do(h, "GET", "/readyz", "127.0.0.1:1", nil)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestReadyzDegradedWhenDataDirNotWritable(t *testing.T) {
	cfg := testConfig(t)
	h := testServer(t, cfg, nil)
	tmp := filepath.Join(cfg.DataDir, "tmp")
	// Replace the tmp directory with a regular file so CreateTemp fails even as root.
	if err := os.RemoveAll(tmp); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := do(h, "GET", "/readyz", "127.0.0.1:1", nil)
	if rec.Code != 503 {
		t.Fatalf("expected 503, got %d body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Status string                 `json:"status"`
		Checks map[string]CheckResult `json:"checks"`
		Failed []string               `json:"failed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Status != "degraded" || body.Checks["data_dir_writable"].OK || len(body.Failed) != 1 {
		t.Errorf("unexpected body: %s", rec.Body.String())
	}
	if !body.Checks["storage_root_readable"].OK {
		t.Error("storage root should still be readable")
	}
}
