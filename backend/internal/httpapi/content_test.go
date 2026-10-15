package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestContentOpenSave covers PROMPT Step 16a AC on the API: open (etag,
// EOL, encoding), save with If-Match (new etag, one pre-image version,
// no-op on identical content, CRLF bytes kept), conflict → 409 unless
// force, unencodable character → 400 with a position, tail mode for a file
// beyond EDITOR_MAX_FILE_SIZE, binary → 415, create, IDOR (other user's
// path is their own jail).
func TestContentOpenSave(t *testing.T) {
	env := newAuthEnv(t, "EDITOR_MAX_FILE_SIZE=64KB", "FILE_VERSION_LIMIT=5")
	ck, hdr := env.newUser(t, "eve", readyPassword)
	ip := "203.0.113.90:1"
	writeHome(t, env, "eve", "notes/a.txt", "one\r\ntwo\r\n")

	r := env.call(http.MethodGet, "/api/v1/files/content?path=/notes/a.txt", ip, nil, nil, ck)
	if r.rec.Code != 200 {
		t.Fatalf("open: %d %s", r.rec.Code, r.rec.Body.String())
	}
	etag, _ := r.body["etag"].(string)
	if r.body["eol"] != "crlf" || r.body["encoding"] != "utf-8" || r.body["content"] != "one\r\ntwo\r\n" || etag == "" || r.body["lines"] != float64(2) || r.body["readOnly"] != false {
		t.Fatalf("open body: %v", r.body)
	}
	if r.rec.Header().Get("ETag") != etag {
		t.Fatal("ETag header missing")
	}

	// save with the right etag, keep EOL: bytes stay CRLF
	h := map[string]string{"X-CSRF-Token": hdr["X-CSRF-Token"], "If-Match": etag}
	r = env.call(http.MethodPut, "/api/v1/files/content", ip, map[string]any{"path": "/notes/a.txt", "content": "one\r\ntwo\r\nthree\r\n", "eol": "keep"}, h, ck)
	if r.rec.Code != 200 || r.body["noop"] != false {
		t.Fatalf("save: %d %s", r.rec.Code, r.rec.Body.String())
	}
	etag2, _ := r.body["etag"].(string)
	ver, _ := r.body["version"].(map[string]any)
	if etag2 == "" || etag2 == etag || ver == nil || ver["no"] != float64(1) {
		t.Fatalf("save result: %v", r.body)
	}
	got, _ := os.ReadFile(filepath.Join(env.cfg.StorageRoot, "eve", "notes", "a.txt"))
	if string(got) != "one\r\ntwo\r\nthree\r\n" {
		t.Fatalf("bytes after save: %q", got)
	}
	// identical content → noop, no new version
	h["If-Match"] = etag2
	r = env.call(http.MethodPut, "/api/v1/files/content", ip, map[string]any{"path": "/notes/a.txt", "content": "one\r\ntwo\r\nthree\r\n"}, h, ck)
	if r.rec.Code != 200 || r.body["noop"] != true || r.body["version"] != nil {
		t.Fatalf("noop save: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// stale etag → 409 with server etag; force → saved with reason force_save
	h["If-Match"] = etag
	r = env.call(http.MethodPut, "/api/v1/files/content", ip, map[string]any{"path": "/notes/a.txt", "content": "stale"}, h, ck)
	if r.rec.Code != 409 || r.code(t) != "CONFLICT" {
		t.Fatalf("stale: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if d, _ := r.body["error"].(map[string]any)["details"].(map[string]any); d["serverEtag"] != etag2 || d["serverMtime"] == nil {
		t.Fatalf("conflict details: %v", r.body)
	}
	r = env.call(http.MethodPut, "/api/v1/files/content", ip, map[string]any{"path": "/notes/a.txt", "content": "forced\n", "force": true, "eol": "lf"}, h, ck)
	if r.rec.Code != 200 || r.body["version"] == nil {
		t.Fatalf("force: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// versions: exactly two pre-images (save, force_save)
	r = env.call(http.MethodGet, "/api/v1/versions?path=/notes/a.txt", ip, nil, nil, ck)
	items, _ := r.body["items"].([]any)
	if len(items) != 2 {
		t.Fatalf("versions: %v", r.body)
	}
	reasons := map[string]bool{}
	for _, it := range items {
		m, _ := it.(map[string]any)
		reasons[m["reason"].(string)] = true
	}
	if !reasons["save"] || !reasons["force_save"] {
		t.Fatalf("version reasons: %v", reasons)
	}
	// EOL conversion to CRLF on save
	r = env.call(http.MethodGet, "/api/v1/files/content?path=/notes/a.txt", ip, nil, nil, ck)
	h["If-Match"], _ = r.body["etag"].(string)
	r = env.call(http.MethodPut, "/api/v1/files/content", ip, map[string]any{"path": "/notes/a.txt", "content": "a\nb\n", "eol": "crlf"}, h, ck)
	if r.rec.Code != 200 {
		t.Fatalf("crlf save: %d %s", r.rec.Code, r.rec.Body.String())
	}
	got, _ = os.ReadFile(filepath.Join(env.cfg.StorageRoot, "eve", "notes", "a.txt"))
	if string(got) != "a\r\nb\r\n" {
		t.Fatalf("crlf bytes: %q", got)
	}

	// unencodable character for windows-1252 → 400 with position
	r = env.call(http.MethodPut, "/api/v1/files/content", ip, map[string]any{"path": "/notes/latin.txt", "content": "ok\nsnow ☃\n", "encoding": "windows-1252", "create": true}, map[string]string{"X-CSRF-Token": hdr["X-CSRF-Token"]}, ck)
	if r.rec.Code != 400 || r.code(t) != "VALIDATION_FAILED" {
		t.Fatalf("unencodable: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if d, _ := r.body["error"].(map[string]any)["details"].(map[string]any); d["line"] != float64(2) || d["col"] != float64(6) {
		t.Fatalf("unencodable position: %v", r.body)
	}
	// create a new file (no If-Match needed), then missing file without create → 404
	r = env.call(http.MethodPut, "/api/v1/files/content", ip, map[string]any{"path": "/notes/new.md", "content": "# new\n", "create": true}, map[string]string{"X-CSRF-Token": hdr["X-CSRF-Token"]}, ck)
	if r.rec.Code != 200 || r.body["created"] != true {
		t.Fatalf("create: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPut, "/api/v1/files/content", ip, map[string]any{"path": "/notes/missing.md", "content": "x"}, map[string]string{"X-CSRF-Token": hdr["X-CSRF-Token"]}, ck)
	if r.rec.Code != 404 {
		t.Fatalf("missing without create: %d", r.rec.Code)
	}
	// Shift_JIS round trip through the API: open detects, save with the same encoding keeps bytes
	sjis := []byte("\x93\xfa\x96\x7b\x8c\xea\x82\xcc\x83\x65\x83\x4c\x83\x58\x83\x67\x82\xc5\x82\xb7\x81\x42\x82\xb1\x82\xea\x82\xcd\x83\x56\x83\x74\x83\x67JIS\x82\xc5\x95\xdb\x91\xb6\x82\xb3\x82\xea\x82\xbd\x83\x74\x83\x40\x83\x43\x83\x8b\x82\xc5\x82\xb7\x81\x42\n")
	writeHome(t, env, "eve", "notes/jp.txt", string(sjis))
	r = env.call(http.MethodGet, "/api/v1/files/content?path=/notes/jp.txt", ip, nil, nil, ck)
	if r.rec.Code != 200 || r.body["encoding"] != "shift_jis" || r.body["lossy"] != false {
		t.Fatalf("sjis open: %d %v", r.rec.Code, r.body)
	}
	h2 := map[string]string{"X-CSRF-Token": hdr["X-CSRF-Token"], "If-Match": r.body["etag"].(string)}
	r = env.call(http.MethodPut, "/api/v1/files/content", ip, map[string]any{"path": "/notes/jp.txt", "content": r.body["content"], "encoding": "shift_jis"}, h2, ck)
	if r.rec.Code != 200 || r.body["noop"] != true {
		t.Fatalf("sjis identical save must be a no-op: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// UTF-16LE with BOM
	writeHome(t, env, "eve", "notes/u16.txt", "\xff\xfeh\x00i\x00\n\x00")
	r = env.call(http.MethodGet, "/api/v1/files/content?path=/notes/u16.txt", ip, nil, nil, ck)
	if r.rec.Code != 200 || r.body["encoding"] != "utf-16le" || r.body["bom"] != true || r.body["content"] != "hi\n" {
		t.Fatalf("utf-16: %d %v", r.rec.Code, r.body)
	}

	// binary → 415
	writeHome(t, env, "eve", "notes/bin.dat", "ab\x00cd")
	r = env.call(http.MethodGet, "/api/v1/files/content?path=/notes/bin.dat", ip, nil, nil, ck)
	if r.rec.Code != 415 || r.code(t) != "BINARY_FILE" {
		t.Fatalf("binary: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// large → 413 without tail; tail=100 → read-only last lines; bigger tail loads more
	var big strings.Builder
	for i := 0; i < 5000; i++ {
		big.WriteString("log line number ")
		big.WriteString(strings.Repeat("0", 4-len(itoaT(i))) + itoaT(i) + "\n")
	}
	writeHome(t, env, "eve", "notes/big.log", big.String())
	r = env.call(http.MethodGet, "/api/v1/files/content?path=/notes/big.log", ip, nil, nil, ck)
	if r.rec.Code != 413 || r.code(t) != "FILE_TOO_LARGE" {
		t.Fatalf("large: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/files/content?path=/notes/big.log&tail=100", ip, nil, nil, ck)
	if r.rec.Code != 200 || r.body["truncated"] != true || r.body["readOnly"] != true || r.body["tailLines"] != float64(100) {
		t.Fatalf("tail: %d %v", r.rec.Code, r.body)
	}
	if c, _ := r.body["content"].(string); !strings.HasPrefix(c, "log line number 4900\n") {
		t.Fatalf("tail content starts %q", c[:40])
	}
	r = env.call(http.MethodGet, "/api/v1/files/content?path=/notes/big.log&tail=2000", ip, nil, nil, ck)
	if r.rec.Code != 200 || r.body["tailLines"] != float64(2000) {
		t.Fatalf("tail more: %d %v", r.rec.Code, r.body["tailLines"])
	}
	// saving a tail-mode (too large) file is refused by size on the way in
	r = env.call(http.MethodPut, "/api/v1/files/content", ip, map[string]any{"path": "/notes/big.log", "content": big.String(), "force": true}, map[string]string{"X-CSRF-Token": hdr["X-CSRF-Token"]}, ck)
	if r.rec.Code != 413 {
		t.Fatalf("save beyond limit: %d", r.rec.Code)
	}

	// folder → 400; traversal → 400; another user sees their own jail (404)
	r = env.call(http.MethodGet, "/api/v1/files/content?path=/notes", ip, nil, nil, ck)
	if r.rec.Code != 400 {
		t.Fatalf("dir: %d", r.rec.Code)
	}
	r = env.call(http.MethodGet, "/api/v1/files/content?path=/../etc/passwd", ip, nil, nil, ck)
	if r.rec.Code != 400 {
		t.Fatalf("traversal: %d", r.rec.Code)
	}
	ck2, _ := env.newUser(t, "frank", readyPassword)
	r = env.call(http.MethodGet, "/api/v1/files/content?path=/notes/a.txt", ip, nil, nil, ck2)
	if r.rec.Code != 404 {
		t.Fatalf("other user: %d", r.rec.Code)
	}
	// CSRF required on PUT
	r = env.call(http.MethodPut, "/api/v1/files/content", ip, map[string]any{"path": "/notes/a.txt", "content": "x", "force": true}, nil, ck)
	if r.rec.Code != 403 {
		t.Fatalf("no csrf: %d", r.rec.Code)
	}
}

func itoaT(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestEditorSession: the workspace document round-trips, is capped at 20
// tabs, validates paths and is private per user.
func TestEditorSession(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := env.newUser(t, "gina", readyPassword)
	ip := "203.0.113.91:1"
	r := env.call(http.MethodGet, "/api/v1/me/editor-session", ip, nil, nil, ck)
	if r.rec.Code != 200 {
		t.Fatalf("empty: %d", r.rec.Code)
	}
	if tabs, _ := r.body["tabs"].([]any); len(tabs) != 0 {
		t.Fatalf("empty tabs: %v", r.body)
	}
	doc := map[string]any{"tabs": []map[string]any{{"path": "/a.txt", "scrollTop": 120, "cursor": map[string]int{"line": 3, "col": 7}}, {"path": "/b/c.md"}}, "activePath": "/b/c.md"}
	r = env.call(http.MethodPut, "/api/v1/me/editor-session", ip, doc, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("put: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/me/editor-session", ip, nil, nil, ck)
	tabs, _ := r.body["tabs"].([]any)
	if len(tabs) != 2 || r.body["activePath"] != "/b/c.md" {
		t.Fatalf("round trip: %v", r.body)
	}
	first, _ := tabs[0].(map[string]any)
	if first["scrollTop"] != float64(120) || first["cursor"].(map[string]any)["line"] != float64(3) {
		t.Fatalf("tab 0: %v", first)
	}
	many := make([]map[string]any, 21)
	for i := range many {
		many[i] = map[string]any{"path": "/f" + itoaT(i)}
	}
	r = env.call(http.MethodPut, "/api/v1/me/editor-session", ip, map[string]any{"tabs": many}, hdr, ck)
	if r.rec.Code != 400 {
		t.Fatalf("21 tabs: %d", r.rec.Code)
	}
	r = env.call(http.MethodPut, "/api/v1/me/editor-session", ip, map[string]any{"tabs": []map[string]any{{"path": "../x"}}}, hdr, ck)
	if r.rec.Code != 400 {
		t.Fatalf("bad path: %d", r.rec.Code)
	}
	ck2, _ := env.newUser(t, "hank", readyPassword)
	r = env.call(http.MethodGet, "/api/v1/me/editor-session", ip, nil, nil, ck2)
	if tabs, _ := r.body["tabs"].([]any); len(tabs) != 0 {
		t.Fatalf("other user sees tabs: %v", r.body)
	}
}
