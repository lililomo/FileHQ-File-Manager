package httpapi

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

func tusMeta(md map[string]string) string {
	parts := make([]string, 0, len(md))
	for k, v := range md {
		parts = append(parts, k+" "+base64.StdEncoding.EncodeToString([]byte(v)))
	}
	return strings.Join(parts, ",")
}

// tusCall is call() with a raw body (tus chunks are not JSON).
func (e *authEnv) tusCall(method, target string, body []byte, hdr map[string]string, cookie *http.Cookie) resp {
	req := httptest.NewRequest(method, target, bytes.NewReader(body))
	req.RemoteAddr = "203.0.113.9:1"
	req.Header.Set("Tus-Resumable", "1.0.0")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	out := resp{rec: rec}
	if rec.Body.Len() > 0 && strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		_ = json.Unmarshal(rec.Body.Bytes(), &out.body)
	}
	return out
}

func tusID(loc string) string { return loc[strings.LastIndex(loc, "/")+1:] }

// TestUploadAC covers the Step 12a acceptance criteria that live in the HTTP
// layer: session/CSRF gates, owner isolation (IDOR), the chunk limit,
// precheck, blocked extensions and quota.
func TestUploadAC(t *testing.T) {
	env := newAuthEnv(t, "UPLOAD_CHUNK_SIZE=1MB", "BLOCKED_EXTENSIONS=exe")
	ckA, hdrA := env.newUser(t, "ana", "Ana-Password-123!")
	ckB, hdrB := env.newUser(t, "budi", "Budi-Password-123!")
	tusHdr := func(h map[string]string, extra map[string]string) map[string]string {
		out := map[string]string{}
		for k, v := range h {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	create := func(ck *http.Cookie, hdr map[string]string, size int64, md map[string]string) resp {
		return env.tusCall(http.MethodPost, "/api/v1/upload/tus/", nil, tusHdr(hdr, map[string]string{
			"Upload-Length": strconv.FormatInt(size, 10), "Upload-Metadata": tusMeta(md)}), ck)
	}

	t.Run("no session → 401, no CSRF → 403 CSRF_INVALID", func(t *testing.T) {
		r := env.tusCall(http.MethodPost, "/api/v1/upload/tus/", nil, map[string]string{"Upload-Length": "10"}, nil)
		if r.rec.Code != 401 || r.code(t) != string(CodeUnauthenticated) {
			t.Fatalf("%d %s", r.rec.Code, r.rec.Body.String())
		}
		r = env.tusCall(http.MethodPost, "/api/v1/upload/tus/", nil, map[string]string{"Upload-Length": "10", "Upload-Metadata": tusMeta(map[string]string{"filename": "a.txt", "path": "/"})}, ckA)
		if r.rec.Code != 403 || r.code(t) != string(CodeCSRFInvalid) {
			t.Fatalf("%d %s", r.rec.Code, r.rec.Body.String())
		}
		// cross-site Fetch Metadata is refused even with a valid token
		r = env.tusCall(http.MethodPost, "/api/v1/upload/tus/", nil, tusHdr(hdrA, map[string]string{"Upload-Length": "10", "Sec-Fetch-Site": "cross-site"}), ckA)
		if r.rec.Code != 403 {
			t.Fatalf("cross-site: %d", r.rec.Code)
		}
	})

	t.Run("OPTIONS advertises the protocol", func(t *testing.T) {
		r := env.tusCall(http.MethodOptions, "/api/v1/upload/tus/", nil, nil, ckA)
		if r.rec.Code != 200 || r.rec.Header().Get("Tus-Version") == "" || !strings.Contains(r.rec.Header().Get("Tus-Extension"), "creation") {
			t.Fatalf("%d %v", r.rec.Code, r.rec.Header())
		}
		if r.rec.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Error("tusd CORS must stay disabled (same origin)")
		}
	})

	var locA string
	t.Run("create + IDOR: user B cannot see, patch or delete A's upload", func(t *testing.T) {
		r := create(ckA, hdrA, 2<<20, map[string]string{"filename": "rahasia.bin", "path": "/"})
		if r.rec.Code != 201 {
			t.Fatalf("%d %s", r.rec.Code, r.rec.Body.String())
		}
		locA = r.rec.Header().Get("Location")
		if !strings.HasPrefix(locA, "https://file.example.test/api/v1/upload/tus/") {
			t.Fatalf("Location = %q (must be absolute from APP_BASE_URL)", locA)
		}
		path := "/api/v1/upload/tus/" + tusID(locA)
		for _, c := range []struct {
			method string
			body   []byte
			hdr    map[string]string
		}{
			{http.MethodHead, nil, hdrB},
			{http.MethodPatch, []byte("x"), tusHdr(hdrB, map[string]string{"Content-Type": "application/offset+octet-stream", "Upload-Offset": "0"})},
			{http.MethodDelete, nil, hdrB},
		} {
			r := env.tusCall(c.method, path, c.body, c.hdr, ckB)
			if r.rec.Code != 404 {
				t.Errorf("%s as B: %d %s", c.method, r.rec.Code, r.rec.Body.String())
			}
		}
		if r := env.tusCall(http.MethodHead, path, nil, hdrA, ckA); r.rec.Code != 200 || r.rec.Header().Get("Upload-Offset") != "0" {
			t.Errorf("HEAD as A: %d %v", r.rec.Code, r.rec.Header())
		}
		// the reservation shows up in /me/quota of A, not B
		q := env.call(http.MethodGet, "/api/v1/me/quota", "203.0.113.9:1", nil, hdrA, ckA)
		if q.body["reserved"] != float64(2<<20) {
			t.Errorf("A reserved = %v", q.body["reserved"])
		}
		q = env.call(http.MethodGet, "/api/v1/me/quota", "203.0.113.9:1", nil, hdrB, ckB)
		if q.body["reserved"] != float64(0) {
			t.Errorf("B reserved = %v", q.body["reserved"])
		}
	})

	t.Run("PATCH larger than UPLOAD_CHUNK_SIZE → 413", func(t *testing.T) {
		path := "/api/v1/upload/tus/" + tusID(locA)
		big := bytes.Repeat([]byte("z"), 1<<20+1)
		r := env.tusCall(http.MethodPatch, path, big, tusHdr(hdrA, map[string]string{"Content-Type": "application/offset+octet-stream", "Upload-Offset": "0"}), ckA)
		if r.rec.Code != 413 || r.code(t) != string(CodePayloadTooLarge) {
			t.Fatalf("%d %s", r.rec.Code, r.rec.Body.String())
		}
		if d := details(r); d["reason"] != "chunk_too_large" {
			t.Errorf("details = %v", d)
		}
	})

	t.Run("full upload lands in the listing; precheck then reports the conflict", func(t *testing.T) {
		path := "/api/v1/upload/tus/" + tusID(locA)
		data := bytes.Repeat([]byte("q"), 2<<20)
		for off := 0; off < len(data); off += 1 << 20 {
			r := env.tusCall(http.MethodPatch, path, data[off:off+1<<20], tusHdr(hdrA, map[string]string{"Content-Type": "application/offset+octet-stream", "Upload-Offset": strconv.Itoa(off)}), ckA)
			if r.rec.Code != 204 {
				t.Fatalf("PATCH %d: %d %s", off, r.rec.Code, r.rec.Body.String())
			}
			if off+1<<20 == len(data) {
				if r.rec.Header().Get("Upload-Result") != "created" || r.rec.Header().Get("Upload-Path") != "/rahasia.bin" {
					t.Errorf("final headers = %v", r.rec.Header())
				}
			}
		}
		if names := listNames(t, env, ckA, hdrA, "/"); !strings.Contains(names, "rahasia.bin") {
			t.Fatalf("listing = %s", names)
		}
		q := env.call(http.MethodGet, "/api/v1/me/quota", "203.0.113.9:1", nil, hdrA, ckA)
		if q.body["used"] != float64(2<<20) || q.body["reserved"] != float64(0) {
			t.Errorf("quota after upload = %v", q.body)
		}
		p := env.call(http.MethodPost, "/api/v1/upload/precheck", "203.0.113.9:1", map[string]any{"path": "/", "files": []map[string]any{
			{"name": "rahasia.bin", "size": 5}, {"name": "virus.exe", "size": 5}, {"name": "baru.txt", "size": 5, "relativePath": "a/b"}}}, hdrA, ckA)
		if p.rec.Code != 200 {
			t.Fatalf("precheck: %d %s", p.rec.Code, p.rec.Body.String())
		}
		items, _ := p.body["items"].([]any)
		if len(items) != 3 {
			t.Fatalf("items = %v", p.body)
		}
		st := func(i int) string { m, _ := items[i].(map[string]any); s, _ := m["status"].(string); return s }
		if st(0) != "conflict" || st(1) != "blocked_ext" || st(2) != "ok" || p.body["quotaOk"] != true {
			t.Errorf("precheck = %s", p.rec.Body.String())
		}
	})

	t.Run("precheck validation", func(t *testing.T) {
		r := env.call(http.MethodPost, "/api/v1/upload/precheck", "203.0.113.9:1", map[string]any{"path": "/", "files": []any{}}, hdrA, ckA)
		if r.rec.Code != 400 || r.code(t) != string(CodeValidationFailed) {
			t.Errorf("empty files: %d %s", r.rec.Code, r.rec.Body.String())
		}
		r = env.call(http.MethodPost, "/api/v1/upload/precheck", "203.0.113.9:1", map[string]any{"path": "/../x", "files": []map[string]any{{"name": "a", "size": 1}}}, hdrA, ckA)
		if r.rec.Code != 400 {
			t.Errorf("bad path: %d %s", r.rec.Code, r.rec.Body.String())
		}
		r = env.call(http.MethodPost, "/api/v1/upload/precheck", "203.0.113.9:1", map[string]any{"path": "/tidak-ada", "files": []map[string]any{{"name": "a", "size": 1}}}, hdrA, ckA)
		if r.rec.Code != 404 {
			t.Errorf("missing dest: %d %s", r.rec.Code, r.rec.Body.String())
		}
		r = env.call(http.MethodPost, "/api/v1/upload/precheck", "203.0.113.9:1", map[string]any{"path": "/", "files": []map[string]any{{"name": "a", "size": 1}}}, nil, ckA)
		if r.rec.Code != 403 {
			t.Errorf("no csrf: %d", r.rec.Code)
		}
	})

	t.Run("blocked extension and quota are enforced at create", func(t *testing.T) {
		r := create(ckA, hdrA, 10, map[string]string{"filename": "x.exe", "path": "/"})
		if r.rec.Code != 415 || r.code(t) != string(CodeUnsupportedType) {
			t.Errorf("exe: %d %s", r.rec.Code, r.rec.Body.String())
		}
		u, err := env.store.GetUserByUsername(context.Background(), "budi")
		if err != nil {
			t.Fatal(err)
		}
		if err := env.store.UpdateUserQuota(context.Background(), sqlc.UpdateUserQuotaParams{ID: u.ID, QuotaBytes: 1 << 20, UpdatedAt: db.Now()}); err != nil {
			t.Fatal(err)
		}
		p := env.call(http.MethodPost, "/api/v1/upload/precheck", "203.0.113.9:1", map[string]any{"path": "/", "files": []map[string]any{{"name": "besar.bin", "size": 2 << 20}}}, hdrB, ckB)
		if p.rec.Code != 200 || p.body["quotaOk"] != false || p.body["quotaReason"] != "quota" || p.body["free"] != float64(1<<20) {
			t.Errorf("precheck quota = %d %s", p.rec.Code, p.rec.Body.String())
		}
		r = create(ckB, hdrB, 2<<20, map[string]string{"filename": "besar.bin", "path": "/"})
		if r.rec.Code != 413 || r.code(t) != string(CodeQuotaExceeded) {
			t.Errorf("quota create: %d %s", r.rec.Code, r.rec.Body.String())
		}
		if d := details(r); d["needed"] != float64(2<<20) || d["requestId"] == nil {
			t.Errorf("details = %v", d)
		}
	})
}
