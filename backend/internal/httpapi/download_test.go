package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/download"
)

// jpegBytes is a minimal JFIF header: enough for magic-byte detection.
var jpegBytes = append([]byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00}, bytes.Repeat([]byte{0x11}, 300)...)

func signURL(t *testing.T, env *authEnv, ck *http.Cookie, hdr map[string]string, path, purpose string) string {
	t.Helper()
	r := env.call(http.MethodPost, "/api/v1/files/sign", "203.0.113.5:1", map[string]any{"path": path, "purpose": purpose}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("sign %s %s: %d %s", purpose, path, r.rec.Code, r.rec.Body.String())
	}
	u, _ := r.body["url"].(string)
	if u == "" {
		t.Fatalf("no url: %s", r.rec.Body.String())
	}
	return u
}

func TestDownloadRangeHeadersAndPolicy(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	content := bytes.Repeat([]byte("0123456789"), 100) // 1000 bytes
	if err := os.WriteFile(filepath.Join(home, "Laporan Q3 – résumé.txt"), content, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "file.html"), []byte("<html><script>alert(1)</script></html>"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "foto.jpg"), jpegBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "logo.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>1</script></svg>`), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "notes.txt.jpg"), []byte("plain text pretending"), 0o640); err != nil {
		t.Fatal(err)
	}
	ip := "198.51.100.7:1"
	dl := signURL(t, env, ck, hdr, "/Laporan Q3 – résumé.txt", "download")
	if !strings.HasPrefix(dl, "/api/v1/files/download?") {
		t.Fatalf("download url %s", dl)
	}

	// AC: HEAD → Accept-Ranges, ETag, Content-Disposition attachment + filename*
	r := env.call(http.MethodHead, dl, ip, nil, nil, nil) // no cookie
	h := r.rec.Header()
	if r.rec.Code != 200 || h.Get("Accept-Ranges") != "bytes" || h.Get("ETag") == "" || h.Get("Last-Modified") == "" {
		t.Fatalf("HEAD: %d %v", r.rec.Code, h)
	}
	cd := h.Get("Content-Disposition")
	if !strings.HasPrefix(cd, `attachment; filename="Laporan Q3 _ r_sum_.txt"; filename*=UTF-8''Laporan%20Q3%20%E2%80%93%20r%C3%A9sum%C3%A9.txt`) {
		t.Fatalf("Content-Disposition %q", cd)
	}
	for k, want := range map[string]string{
		"Content-Security-Policy": download.RawCSP, "X-Content-Type-Options": "nosniff", "Cache-Control": download.CacheControl,
		"X-Accel-Buffering": "no", "Referrer-Policy": "no-referrer", "Cross-Origin-Resource-Policy": "same-origin", "Content-Type": "text/plain",
	} {
		if got := h.Get(k); !strings.HasPrefix(got, want) {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	etag := h.Get("ETag")

	// AC: Range 100-199 → 206 with exactly 100 bytes
	r = env.call(http.MethodGet, dl, ip, nil, map[string]string{"Range": "bytes=100-199"}, nil)
	if r.rec.Code != 206 || r.rec.Body.Len() != 100 || r.rec.Header().Get("Content-Range") != "bytes 100-199/1000" {
		t.Fatalf("range: %d len=%d %v", r.rec.Code, r.rec.Body.Len(), r.rec.Header())
	}
	if !bytes.Equal(r.rec.Body.Bytes(), content[100:200]) {
		t.Fatal("range body mismatch")
	}
	r = env.call(http.MethodGet, dl, ip, nil, map[string]string{"If-None-Match": etag}, nil)
	if r.rec.Code != 304 {
		t.Fatalf("If-None-Match: %d", r.rec.Code)
	}
	r = env.call(http.MethodGet, dl, ip, nil, map[string]string{"Range": "bytes=900-", "If-Range": etag}, nil)
	if r.rec.Code != 206 || r.rec.Body.Len() != 100 {
		t.Fatalf("If-Range: %d len=%d", r.rec.Code, r.rec.Body.Len())
	}
	// full GET is counted as an activity (Recent/Aktivitas)
	r = env.call(http.MethodGet, dl, ip, nil, nil, nil)
	if r.rec.Code != 200 || !bytes.Equal(r.rec.Body.Bytes(), content) {
		t.Fatalf("GET: %d", r.rec.Code)
	}
	r = env.call(http.MethodGet, "/api/v1/me/activity?action=download", ip, nil, hdr, ck)
	if r.rec.Code != 200 || !strings.Contains(r.rec.Body.String(), `"download"`) {
		t.Fatalf("activity: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// AC: file.html via raw → octet-stream attachment; foto.jpg → image/jpeg inline + CSP sandbox
	raw := signURL(t, env, ck, hdr, "/file.html", "media")
	if !strings.HasPrefix(raw, "/api/v1/files/raw?") {
		t.Fatalf("raw url %s", raw)
	}
	r = env.call(http.MethodGet, raw, ip, nil, nil, nil)
	h = r.rec.Header()
	if r.rec.Code != 200 || h.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment;") {
		t.Fatalf("html via raw: %d %v", r.rec.Code, h)
	}
	r = env.call(http.MethodGet, signURL(t, env, ck, hdr, "/foto.jpg", "media"), ip, nil, nil, nil)
	h = r.rec.Header()
	if r.rec.Code != 200 || h.Get("Content-Type") != "image/jpeg" || !strings.HasPrefix(h.Get("Content-Disposition"), "inline;") || h.Get("Content-Security-Policy") != download.RawCSP {
		t.Fatalf("jpg via raw: %d %v", r.rec.Code, h)
	}
	r = env.call(http.MethodGet, signURL(t, env, ck, hdr, "/logo.svg", "inline"), ip, nil, nil, nil)
	if r.rec.Header().Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(r.rec.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("svg via raw: %v", r.rec.Header())
	}
	// extension lies: bytes are text, name says jpg → not inline, not image/jpeg
	r = env.call(http.MethodGet, signURL(t, env, ck, hdr, "/notes.txt.jpg", "media"), ip, nil, nil, nil)
	if strings.HasPrefix(r.rec.Header().Get("Content-Disposition"), "inline;") || r.rec.Header().Get("Content-Type") == "image/jpeg" {
		t.Fatalf("sniffing by extension: %v", r.rec.Header())
	}
	// download purpose never inlines, even an image
	r = env.call(http.MethodGet, signURL(t, env, ck, hdr, "/foto.jpg", "download"), ip, nil, nil, nil)
	if !strings.HasPrefix(r.rec.Header().Get("Content-Disposition"), "attachment;") || r.rec.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("jpg download: %v", r.rec.Header())
	}
	// a download URL is refused on /raw and vice versa (purpose is signed)
	r = env.call(http.MethodGet, strings.Replace(dl, "/files/download?", "/files/raw?", 1), ip, nil, nil, nil)
	if r.rec.Code != 403 || r.code(t) != "SIGNATURE_INVALID" {
		t.Fatalf("download url on raw: %d %s", r.rec.Code, r.code(t))
	}
	r = env.call(http.MethodGet, strings.Replace(raw, "/files/raw?", "/files/download?", 1), ip, nil, nil, nil)
	if r.rec.Code != 403 || r.code(t) != "SIGNATURE_INVALID" {
		t.Fatalf("raw url on download: %d %s", r.rec.Code, r.code(t))
	}
	// folders can not be downloaded as a single file
	if err := os.Mkdir(filepath.Join(home, "dir"), 0o750); err != nil {
		t.Fatal(err)
	}
	r = env.call(http.MethodGet, signURL(t, env, ck, hdr, "/dir", "download"), ip, nil, nil, nil)
	if r.rec.Code != 400 {
		t.Fatalf("dir download: %d %s", r.rec.Code, r.rec.Body.String())
	}
}

func TestDownloadSignatureTamperExpiryAndAccount(t *testing.T) {
	env := newAuthEnv(t)
	ckA, hdrA := env.newUser(t, "alice", readyPassword)
	ckB, hdrB := env.newUser(t, "bob", readyPassword)
	seedFiles(t, filepath.Join(env.cfg.StorageRoot, "alice"), "secret.txt")
	seedFiles(t, filepath.Join(env.cfg.StorageRoot, "bob"), "mine.txt")
	ip := "198.51.100.7:1"
	dl := signURL(t, env, ckA, hdrA, "/secret.txt", "download")
	r := env.call(http.MethodGet, dl, ip, nil, nil, nil)
	if r.rec.Code != 200 || r.rec.Body.String() != "secret.txt" {
		t.Fatalf("alice download: %d", r.rec.Code)
	}

	// AC: one character of sig changed → 403 SIGNATURE_INVALID
	u, _ := url.Parse(dl)
	q := u.Query()
	sig := q.Get("sig")
	flip := "A"
	if sig[0] == 'A' {
		flip = "B"
	}
	q.Set("sig", flip+sig[1:])
	u.RawQuery = q.Encode()
	r = env.call(http.MethodGet, u.String(), ip, nil, nil, nil)
	if r.rec.Code != 403 || r.code(t) != "SIGNATURE_INVALID" {
		t.Fatalf("tampered sig: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// path swapped under a valid signature → invalid
	q = u.Query()
	q.Set("sig", sig)
	q.Set("path", "/other.txt")
	u.RawQuery = q.Encode()
	r = env.call(http.MethodGet, u.String(), ip, nil, nil, nil)
	if r.rec.Code != 403 || r.code(t) != "SIGNATURE_INVALID" {
		t.Fatalf("tampered path: %d", r.rec.Code)
	}
	// scope swapped to bob's id → invalid; bob signing alice's path hits his own jail → 404
	r = env.call(http.MethodGet, signURL(t, env, ckB, hdrB, "/secret.txt", "download"), ip, nil, nil, nil)
	if r.rec.Code != 404 {
		t.Fatalf("bob signs alice's path: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/files/sign", ip, map[string]any{"path": "/../alice/secret.txt", "purpose": "download"}, hdrB, ckB)
	if r.rec.Code != 400 || r.code(t) != "PATH_INVALID" {
		t.Fatalf("bob traversal at sign time: %d %s", r.rec.Code, r.code(t))
	}
	// missing sig / missing exp
	q = u.Query()
	q.Del("sig")
	u.RawQuery = q.Encode()
	if r = env.call(http.MethodGet, u.String(), ip, nil, nil, nil); r.rec.Code != 403 {
		t.Fatalf("no sig: %d", r.rec.Code)
	}

	// AC: expired → 403 SIGNATURE_EXPIRED (signed with the same key, exp in the past)
	alice, _ := env.store.GetUserByUsername(context.Background(), "alice")
	past := download.NewSigner(env.cfg.AppSecret, func() time.Time { return time.Now().Add(-10 * time.Minute) })
	expired := past.URL(download.Params{Scope: download.UserScope(alice.ID), Path: "/secret.txt", Purpose: download.PurposeDownload, Exp: past.Expiry(5 * time.Minute)})
	r = env.call(http.MethodGet, expired, ip, nil, nil, nil)
	if r.rec.Code != 403 || r.code(t) != "SIGNATURE_EXPIRED" {
		t.Fatalf("expired: %d %s", r.rec.Code, r.code(t))
	}

	// AC: alice disabled while her URL is still valid → 403
	if err := env.store.UpdateUserStatus(context.Background(), sqlc.UpdateUserStatusParams{Status: "disabled", UpdatedAt: db.Now(), ID: alice.ID}); err != nil {
		t.Fatal(err)
	}
	r = env.call(http.MethodGet, dl, ip, nil, nil, nil)
	if r.rec.Code != 403 || r.code(t) != "FORBIDDEN" {
		t.Fatalf("disabled user: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// logs never carry the host path
	if strings.Contains(env.logs.String(), env.cfg.StorageRoot) {
		t.Fatal("storage root leaked into logs")
	}
}

func TestDownloadConcurrencyLimit(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	seedFiles(t, filepath.Join(env.cfg.StorageRoot, "admin"), "a.txt", "b.jpg")
	ip := "198.51.100.7:1"
	admin, _ := env.store.GetUserByUsername(context.Background(), "admin")
	var rel []func()
	for i := 0; i < download.MaxConcurrentPerUser; i++ {
		f, ok := env.dl.Acquire(admin.ID)
		if !ok {
			t.Fatal("slot")
		}
		rel = append(rel, f)
	}
	dl := signURL(t, env, ck, hdr, "/a.txt", "download")
	r := env.call(http.MethodGet, dl, ip, nil, nil, nil)
	if r.rec.Code != 429 || r.code(t) != "RATE_LIMITED" || r.rec.Header().Get("Retry-After") != "5" || details(r)["reason"] != "concurrent_downloads" {
		t.Fatalf("4th download: %d %s %v", r.rec.Code, r.rec.Body.String(), r.rec.Header())
	}
	// inline media is not counted
	r = env.call(http.MethodGet, signURL(t, env, ck, hdr, "/b.jpg", "media"), ip, nil, nil, nil)
	if r.rec.Code != 200 {
		t.Fatalf("media while saturated: %d", r.rec.Code)
	}
	// zip streams share the limit
	r = env.call(http.MethodPost, "/api/v1/files/zip", ip, map[string]any{"paths": []string{"/a.txt"}}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("zip create: %d %s", r.rec.Code, r.rec.Body.String())
	}
	zipURL := r.body["url"].(string)
	if r = env.call(http.MethodGet, zipURL, ip, nil, nil, nil); r.rec.Code != 429 {
		t.Fatalf("zip while saturated: %d", r.rec.Code)
	}
	rel[0]()
	if r = env.call(http.MethodGet, dl, ip, nil, nil, nil); r.rec.Code != 200 {
		t.Fatalf("after release: %d", r.rec.Code)
	}
	if env.dl.Active(admin.ID) != download.MaxConcurrentPerUser-1 {
		t.Fatalf("slot leaked: %d", env.dl.Active(admin.ID))
	}
}

func TestZipStreaming(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	seedFiles(t, home, "x.txt", "y.txt", "proyek/", "proyek/a.txt", "proyek/sub/", "proyek/sub/b.txt")
	if err := os.Symlink("a.txt", filepath.Join(home, "proyek", "link.txt")); err != nil {
		t.Fatal(err)
	}
	ip := "198.51.100.7:1"

	// validation
	r := env.call(http.MethodPost, "/api/v1/files/zip", ip, map[string]any{"paths": []string{}}, hdr, ck)
	if r.rec.Code != 400 {
		t.Fatalf("empty paths: %d", r.rec.Code)
	}
	r = env.call(http.MethodPost, "/api/v1/files/zip", ip, map[string]any{"paths": []string{"/"}}, hdr, ck)
	if r.rec.Code != 400 {
		t.Fatalf("root: %d", r.rec.Code)
	}
	r = env.call(http.MethodPost, "/api/v1/files/zip", ip, map[string]any{"paths": []string{"/x.txt"}, "method": "lzma"}, hdr, ck)
	if r.rec.Code != 400 {
		t.Fatalf("method: %d", r.rec.Code)
	}
	r = env.call(http.MethodPost, "/api/v1/files/zip", ip, map[string]any{"paths": []string{"/x.txt"}}, nil, ck)
	if r.rec.Code != 403 {
		t.Fatalf("no csrf: %d", r.rec.Code)
	}

	r = env.call(http.MethodPost, "/api/v1/files/zip", ip, map[string]any{"paths": []string{"/x.txt", "/y.txt", "/proyek"}}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("zip create: %d %s", r.rec.Code, r.rec.Body.String())
	}
	zipURL := r.body["url"].(string)
	if !strings.HasPrefix(zipURL, "/api/v1/files/zip?") || !strings.Contains(zipURL, "purpose=zip") || !strings.Contains(zipURL, "token=") {
		t.Fatalf("zip url %s", zipURL)
	}
	if name := r.body["name"].(string); !zipNameRe.MatchString(name) {
		t.Fatalf("name %s", name)
	}
	r = env.call(http.MethodHead, zipURL, ip, nil, nil, nil)
	h := r.rec.Header()
	if r.rec.Code != 200 || h.Get("Content-Type") != "application/zip" || h.Get("Accept-Ranges") != "none" || h.Get("Content-Length") != "" ||
		h.Get("Content-Security-Policy") != download.RawCSP || !strings.HasPrefix(h.Get("Content-Disposition"), `attachment; filename="`+strings.ToLower(env.cfg.AppName)+`-`) {
		t.Fatalf("HEAD zip: %d %v", r.rec.Code, h)
	}
	r = env.call(http.MethodGet, zipURL, ip, nil, nil, nil)
	if r.rec.Code != 200 {
		t.Fatalf("GET zip: %d %s", r.rec.Code, r.rec.Body.String())
	}
	zr, err := zip.NewReader(bytes.NewReader(r.rec.Body.Bytes()), int64(r.rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, f := range zr.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		b := new(bytes.Buffer)
		_, _ = b.ReadFrom(rc)
		_ = rc.Close()
		got[f.Name] = b.String()
	}
	for name, want := range map[string]string{"x.txt": "x.txt", "y.txt": "y.txt", "proyek/a.txt": "proyek/a.txt", "proyek/sub/b.txt": "proyek/sub/b.txt"} {
		if got[name] != want {
			t.Fatalf("entry %q = %q (all: %v)", name, got[name], got)
		}
	}
	if _, ok := got["proyek/link.txt"]; ok {
		t.Fatal("symlink was archived")
	}
	if !strings.Contains(got[download.ErrorsEntry], "proyek/link.txt: symlink skipped") || strings.Contains(got[download.ErrorsEntry], env.cfg.StorageRoot) {
		t.Fatalf("_ERRORS.txt: %q", got[download.ErrorsEntry])
	}
	// only the small json payload lives in DATA_DIR/tmp
	entries, _ := os.ReadDir(filepath.Join(env.cfg.DataDir, "tmp"))
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "zip-") || !strings.HasSuffix(e.Name(), ".json") {
			t.Fatalf("unexpected temp file %s", e.Name())
		}
		fi, _ := e.Info()
		if fi.Size() > 4096 {
			t.Fatalf("payload too large: %d", fi.Size())
		}
	}

	// single folder → <folder>.zip with entries under the folder name; explicit name; deflate
	r = env.call(http.MethodPost, "/api/v1/files/zip", ip, map[string]any{"paths": []string{"/proyek"}, "method": "deflate"}, hdr, ck)
	if r.rec.Code != 200 || r.body["name"] != "proyek.zip" {
		t.Fatalf("folder zip: %d %v", r.rec.Code, r.body)
	}
	r = env.call(http.MethodGet, r.body["url"].(string), ip, nil, nil, nil)
	if !strings.HasPrefix(r.rec.Header().Get("Content-Disposition"), `attachment; filename="proyek.zip"`) {
		t.Fatalf("folder disposition %q", r.rec.Header().Get("Content-Disposition"))
	}
	zr, _ = zip.NewReader(bytes.NewReader(r.rec.Body.Bytes()), int64(r.rec.Body.Len()))
	if zr.File[0].Name != "proyek/" || zr.File[1].Method != zip.Deflate {
		t.Fatalf("folder zip entries: %s %d", zr.File[0].Name, zr.File[1].Method)
	}
	r = env.call(http.MethodPost, "/api/v1/files/zip", ip, map[string]any{"paths": []string{"/x.txt"}, "name": "pilihan"}, hdr, ck)
	if r.rec.Code != 200 || r.body["name"] != "pilihan.zip" {
		t.Fatalf("named zip: %d %v", r.rec.Code, r.body)
	}
	// the zip url is a bearer token bound to its payload: tampering the token → 403
	u, _ := url.Parse(zipURL)
	q := u.Query()
	q.Set("token", strings.Repeat("0", 32))
	u.RawQuery = q.Encode()
	if r = env.call(http.MethodGet, u.String(), ip, nil, nil, nil); r.rec.Code != 403 {
		t.Fatalf("tampered token: %d", r.rec.Code)
	}
	// bob can not reuse alice's… admin's payload id with his own signature
	ckB, hdrB := env.newUser(t, "bob", readyPassword)
	r = env.call(http.MethodPost, "/api/v1/files/zip", ip, map[string]any{"paths": []string{"/x.txt"}}, hdrB, ckB)
	bob, _ := env.store.GetUserByUsername(context.Background(), "bob")
	adminToken := q.Get("token")
	_ = adminToken
	sg := download.NewSigner(env.cfg.AppSecret, nil)
	forged := sg.URL(download.Params{Scope: download.UserScope(bob.ID), Purpose: download.PurposeZip, Extra: mustQuery(zipURL).Get("token"), Exp: sg.Expiry(time.Minute)})
	if r = env.call(http.MethodGet, forged, ip, nil, nil, nil); r.rec.Code != 404 {
		t.Fatalf("foreign payload: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// activity row
	r = env.call(http.MethodGet, "/api/v1/me/activity?action=download", ip, nil, hdr, ck)
	if !strings.Contains(r.rec.Body.String(), `"zip":true`) {
		t.Fatalf("zip activity: %s", r.rec.Body.String())
	}
}

func mustQuery(raw string) url.Values {
	u, _ := url.Parse(raw)
	return u.Query()
}

func TestSignBatchAndPurposes(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	ip := "203.0.113.5:1"
	for _, bad := range []map[string]any{
		{"path": "/a.txt", "purpose": "zip"},
		{"path": "/a.txt", "purpose": "whatever"},
		{"path": "/", "purpose": "download"},
		{"path": "/a.txt", "paths": []string{"/b.txt"}, "purpose": "download"},
		{"items": []map[string]any{{"path": "/a.txt", "purpose": "zip"}}},
		{"path": "/a.txt", "purpose": "download", "share": "abc"},
	} {
		r := env.call(http.MethodPost, "/api/v1/files/sign", ip, bad, hdr, ck)
		if r.rec.Code != 400 {
			t.Fatalf("%v: %d %s", bad, r.rec.Code, r.rec.Body.String())
		}
	}
	r := env.call(http.MethodPost, "/api/v1/files/sign", ip, map[string]any{"items": []map[string]any{
		{"path": "/a.txt", "purpose": "download"}, {"path": "/v.mp4", "purpose": "media"}, {"path": "/t.png", "purpose": "thumb"}, {"path": "/../x", "purpose": "download"},
	}}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("items: %d %s", r.rec.Code, r.rec.Body.String())
	}
	urls := r.body["urls"].([]any)
	if len(urls) != 3 || len(r.body["invalid"].([]any)) != 1 {
		t.Fatalf("items result: %s", r.rec.Body.String())
	}
	routes := map[string]string{"download": "/api/v1/files/download?", "media": "/api/v1/files/raw?", "thumb": "/api/v1/files/thumb?"}
	var expDownload, expMedia string
	for _, e := range urls {
		m := e.(map[string]any)
		if !strings.HasPrefix(m["url"].(string), routes[m["purpose"].(string)]) {
			t.Fatalf("route for %v", m)
		}
		switch m["purpose"] {
		case "download":
			expDownload = m["expiresAt"].(string)
		case "media":
			expMedia = m["expiresAt"].(string)
		}
	}
	// media TTL (2 h) is longer than download TTL (5 m); the batch expiresAt is the earliest
	if expDownload >= expMedia || r.body["expiresAt"].(string) != expDownload {
		t.Fatalf("ttls: download=%s media=%s batch=%v", expDownload, expMedia, r.body["expiresAt"])
	}
	// paths + purpose download (same purpose for all)
	r = env.call(http.MethodPost, "/api/v1/files/sign", ip, map[string]any{"paths": []string{"/a.txt", "/b.txt"}, "purpose": "download"}, hdr, ck)
	if r.rec.Code != 200 || len(r.body["urls"].([]any)) != 2 {
		t.Fatalf("paths batch: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// unauthenticated sign is refused
	r = env.call(http.MethodPost, "/api/v1/files/sign", ip, map[string]any{"path": "/a.txt", "purpose": "download"}, nil, nil)
	if r.rec.Code != 401 {
		t.Fatalf("no session: %d", r.rec.Code)
	}
}

func TestRawVariantsPreviewAndThumb(t *testing.T) {
	env := newAuthEnv(t)
	svc := withThumbs(t, env, testFFmpeg)
	_ = svc
	ck, hdr := readyAdmin(t, env)
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	if err := os.WriteFile(filepath.Join(home, "big.png"), testPNG(t, 3000, 1500), 0o640); err != nil {
		t.Fatal(err)
	}
	seedFiles(t, home, "notes.txt")
	ip := "198.51.100.7:1"
	raw := signURL(t, env, ck, hdr, "/big.png", "media")
	r := env.call(http.MethodGet, raw+"&variant=preview", ip, nil, nil, nil)
	if r.rec.Code != 200 || r.rec.Header().Get("Content-Type") != "image/webp" || r.rec.Header().Get("Content-Security-Policy") != RawCSP {
		t.Fatalf("preview: %d %v %s", r.rec.Code, r.rec.Header(), r.rec.Body.String())
	}
	w, h := webpDimensions(t, r.rec.Body.Bytes())
	if w != 2048 || h != 1024 {
		t.Fatalf("preview dimensions %dx%d", w, h)
	}
	// second call is served from the cache with an ETag
	etag := r.rec.Header().Get("ETag")
	if r = env.call(http.MethodGet, raw+"&variant=preview", ip, nil, map[string]string{"If-None-Match": etag}, nil); r.rec.Code != 304 {
		t.Fatalf("preview etag: %d", r.rec.Code)
	}
	// thumb variant = 512 px pipeline (202 while rendering)
	r = waitThumb(t, env, raw+"&variant=thumb")
	if r.rec.Code != 200 || r.rec.Header().Get("Content-Type") != "image/webp" {
		t.Fatalf("thumb variant: %d", r.rec.Code)
	}
	if w, _ := webpDimensions(t, r.rec.Body.Bytes()); w != 512 {
		t.Fatalf("thumb width %d", w)
	}
	r = env.call(http.MethodGet, raw+"&variant=huge", ip, nil, nil, nil)
	if r.rec.Code != 400 {
		t.Fatalf("bad variant: %d", r.rec.Code)
	}
	r = env.call(http.MethodGet, signURL(t, env, ck, hdr, "/notes.txt", "media")+"&variant=preview", ip, nil, nil, nil)
	if r.rec.Code != 404 {
		t.Fatalf("text preview: %d", r.rec.Code)
	}
	// the thumbnail route still refuses the preview size
	r = env.call(http.MethodPost, "/api/v1/files/sign", ip, map[string]any{"path": "/big.png", "purpose": "thumb", "size": 2048}, hdr, ck)
	if r.rec.Code != 400 {
		t.Fatalf("thumb 2048: %d", r.rec.Code)
	}
}

// webpDimensions reads the VP8/VP8L/VP8X header of a RIFF WebP file.
func webpDimensions(t *testing.T, b []byte) (int, int) {
	t.Helper()
	if len(b) < 30 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		t.Fatalf("not a webp (%d bytes)", len(b))
	}
	switch string(b[12:16]) {
	case "VP8X":
		return int(b[24]) | int(b[25])<<8 | int(b[26])<<16 + 1, int(b[27]) | int(b[28])<<8 | int(b[29])<<16 + 1
	case "VP8L":
		bits := uint32(b[21]) | uint32(b[22])<<8 | uint32(b[23])<<16 | uint32(b[24])<<24
		return int(bits&0x3FFF) + 1, int((bits>>14)&0x3FFF) + 1
	case "VP8 ":
		return int(b[26]) | int(b[27]&0x3F)<<8, int(b[28]) | int(b[29]&0x3F)<<8
	}
	t.Fatalf("unknown webp chunk %q", b[12:16])
	return 0, 0
}

var _ = httptest.NewRecorder

// zipNameRe is "<app>-<yyyyMMdd-HHmm>.zip".
var zipNameRe = regexp.MustCompile(`^[a-z0-9-]+-\d{8}-\d{4}\.zip$`)
