package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

// Step 16b: versions API — list alias, content, download, restore ETag,
// delete one / all, owner-only ids (IDOR), disabled store.
func TestVersionsAPI(t *testing.T) {
	env := newAuthEnv(t, "EDITOR_MAX_FILE_SIZE=64KB", "FILE_VERSION_LIMIT=5")
	ck, hdr := env.newUser(t, "vera", readyPassword)
	ck2, hdr2 := env.newUser(t, "mallory", readyPassword)
	ip := "203.0.113.90:1"
	writeHome(t, env, "vera", "docs/n.md", "# one\r\n")
	// two saves through the editor → two pre-images
	r := env.call(http.MethodGet, "/api/v1/files/content?path=/docs/n.md", ip, nil, nil, ck)
	if r.rec.Code != 200 {
		t.Fatalf("content: %d %v", r.rec.Code, r.body)
	}
	etag := r.rec.Header().Get("ETag")
	h := map[string]string{"X-CSRF-Token": hdr["X-CSRF-Token"], "If-Match": etag}
	r = env.call(http.MethodPut, "/api/v1/files/content", ip, map[string]any{"path": "/docs/n.md", "content": "# two\r\n", "eol": "keep"}, h, ck)
	if r.rec.Code != 200 {
		t.Fatalf("save 1: %d %v", r.rec.Code, r.body)
	}
	h["If-Match"] = r.rec.Header().Get("ETag")
	r = env.call(http.MethodPut, "/api/v1/files/content", ip, map[string]any{"path": "/docs/n.md", "content": "# three\r\n", "eol": "keep"}, h, ck)
	if r.rec.Code != 200 {
		t.Fatalf("save 2: %d %v", r.rec.Code, r.body)
	}
	r = env.call(http.MethodGet, "/api/v1/files/versions?path=/docs/n.md", ip, nil, nil, ck)
	items, _ := r.body["items"].([]any)
	if r.rec.Code != 200 || len(items) != 2 {
		t.Fatalf("files/versions alias: %d %v", r.rec.Code, r.body)
	}
	newest, _ := items[0].(map[string]any)
	oldest, _ := items[1].(map[string]any)
	v1ID, _ := oldest["id"].(string)
	v2ID, _ := newest["id"].(string)
	// content of the oldest version: "# one" with CRLF, read-only
	r = env.call(http.MethodGet, "/api/v1/versions/"+v1ID+"/content", ip, nil, nil, ck)
	if r.rec.Code != 200 || r.body["content"] != "# one\r\n" || r.body["eol"] != "crlf" || r.body["readOnly"] != true || r.body["path"] != "/docs/n.md" {
		t.Fatalf("version content: %d %v", r.rec.Code, r.body)
	}
	if r.rec.Header().Get("ETag") != `"v-`+v1ID+`"` {
		t.Fatalf("version etag %q", r.rec.Header().Get("ETag"))
	}
	// download: attachment named n.v1.md, bytes of the blob, Range honoured
	r = env.call(http.MethodGet, "/api/v1/versions/"+v1ID+"/download", ip, nil, nil, ck)
	if r.rec.Code != 200 || r.rec.Body.String() != "# one\r\n" {
		t.Fatalf("download: %d %q", r.rec.Code, r.rec.Body.String())
	}
	if cd := r.rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") || !strings.Contains(cd, "n.v1.md") {
		t.Fatalf("download disposition %q", cd)
	}
	r = env.call(http.MethodGet, "/api/v1/versions/"+v1ID+"/download", ip, nil, map[string]string{"Range": "bytes=0-2"}, ck)
	if r.rec.Code != 206 || r.rec.Body.String() != "# o" {
		t.Fatalf("range: %d %q", r.rec.Code, r.rec.Body.String())
	}
	// IDOR: mallory cannot read, restore or delete vera's versions
	for _, c := range []struct{ m, p string }{
		{http.MethodGet, "/api/v1/versions/" + v1ID + "/content"},
		{http.MethodGet, "/api/v1/versions/" + v1ID + "/download"},
		{http.MethodPost, "/api/v1/versions/" + v1ID + "/restore"},
		{http.MethodDelete, "/api/v1/versions/" + v1ID},
	} {
		rr := env.call(c.m, c.p, ip, nil, hdr2, ck2)
		if rr.rec.Code != 404 {
			t.Fatalf("%s %s as another user: %d %v", c.m, c.p, rr.rec.Code, rr.body)
		}
	}
	// restore v1: new ETag header, a `restore` pre-image, file content back
	r = env.call(http.MethodPost, "/api/v1/versions/"+v1ID+"/restore", ip, nil, hdr, ck)
	if r.rec.Code != 200 || r.rec.Header().Get("ETag") == "" || r.body["etag"] != r.rec.Header().Get("ETag") {
		t.Fatalf("restore: %d %v %q", r.rec.Code, r.body, r.rec.Header().Get("ETag"))
	}
	if ver, _ := r.body["version"].(map[string]any); ver == nil || ver["reason"] != "restore" {
		t.Fatalf("restore pre-image: %v", r.body["version"])
	}
	r = env.call(http.MethodGet, "/api/v1/files/content?path=/docs/n.md", ip, nil, nil, ck)
	if r.body["content"] != "# one\r\n" {
		t.Fatalf("content after restore: %v", r.body["content"])
	}
	// delete one, then all
	r = env.call(http.MethodDelete, "/api/v1/versions/"+v2ID, ip, nil, hdr, ck)
	if r.rec.Code != 204 {
		t.Fatalf("delete: %d %v", r.rec.Code, r.body)
	}
	r = env.call(http.MethodGet, "/api/v1/versions?path=/docs/n.md", ip, nil, nil, ck)
	if items, _ := r.body["items"].([]any); len(items) != 2 {
		t.Fatalf("after delete one: %v", r.body)
	}
	r = env.call(http.MethodDelete, "/api/v1/versions?path=/docs/n.md", ip, nil, hdr, ck)
	if r.rec.Code != 204 {
		t.Fatalf("delete all: %d %v", r.rec.Code, r.body)
	}
	r = env.call(http.MethodGet, "/api/v1/versions?path=/docs/n.md", ip, nil, nil, ck)
	if items, _ := r.body["items"].([]any); len(items) != 0 {
		t.Fatalf("after delete all: %v", r.body)
	}
	r = env.call(http.MethodGet, "/api/v1/me/quota", ip, nil, nil, ck)
	if v, _ := r.body["versions"].(float64); v != 0 {
		t.Fatalf("versions bytes after delete all: %v", r.body["versions"])
	}
	// an unknown share scope is NOT_FOUND (Step 18b); a bad path 400
	r = env.call(http.MethodGet, "/api/v1/versions?path=/docs/n.md&share=x", ip, nil, nil, ck)
	if r.rec.Code != 404 {
		t.Fatalf("scope: %d", r.rec.Code)
	}
	r = env.call(http.MethodGet, "/api/v1/versions?path=../x", ip, nil, nil, ck)
	if r.rec.Code != 400 {
		t.Fatalf("bad path: %d", r.rec.Code)
	}
}

// TestVersionsPermanentDeleteDropsBlobs: deleting a file permanently (or
// purging it from the trash) removes its versions and their bytes.
func TestVersionsPermanentDeleteDropsBlobs(t *testing.T) {
	env := newAuthEnv(t, "EDITOR_MAX_FILE_SIZE=64KB", "FILE_VERSION_LIMIT=5")
	ck, hdr := env.newUser(t, "vince", readyPassword)
	ip := "203.0.113.91:1"
	writeHome(t, env, "vince", "p/a.txt", "a1\n")
	r := env.call(http.MethodGet, "/api/v1/files/content?path=/p/a.txt", ip, nil, nil, ck)
	h := map[string]string{"X-CSRF-Token": hdr["X-CSRF-Token"], "If-Match": r.rec.Header().Get("ETag")}
	r = env.call(http.MethodPut, "/api/v1/files/content", ip, map[string]any{"path": "/p/a.txt", "content": "a2\n", "eol": "keep"}, h, ck)
	if r.rec.Code != 200 {
		t.Fatalf("save: %d %v", r.rec.Code, r.body)
	}
	r = env.call(http.MethodGet, "/api/v1/me/quota", ip, nil, nil, ck)
	if v, _ := r.body["versions"].(float64); v != 3 {
		t.Fatalf("versions bytes before delete: %v", r.body["versions"])
	}
	r = env.call(http.MethodPost, "/api/v1/files/delete-permanent", ip, map[string]any{"paths": []string{"/p"}}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("delete-permanent: %d %v", r.rec.Code, r.body)
	}
	r = env.call(http.MethodGet, "/api/v1/versions?path=/p/a.txt", ip, nil, nil, ck)
	if items, _ := r.body["items"].([]any); len(items) != 0 {
		t.Fatalf("versions survive permanent delete: %v", r.body)
	}
	r = env.call(http.MethodGet, "/api/v1/me/quota", ip, nil, nil, ck)
	if v, _ := r.body["versions"].(float64); v != 0 {
		t.Fatalf("versions bytes after permanent delete: %v", r.body["versions"])
	}
}
