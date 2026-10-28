package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kppbwi.app/filemanager/internal/download"
)

// Step 23e: separate user-content origin (SPEC ADR-06 (a), §8.9, §10.3).
// APP_BASE_URL of the test env is https://file.example.test; the content
// origin below is a different host on the same handler.
const (
	testContentBase = "http://content.example.test"
	testAppHost     = "https://file.example.test"
	testOtherHost   = "http://example.com"
)

func newContentEnv(t *testing.T, extra ...string) *authEnv {
	t.Helper()
	return newAuthEnv(t, append([]string{"CONTENT_BASE_URL=" + testContentBase}, extra...)...)
}

// hostCall issues a request against an absolute URL (Host comes from it).
func (e *authEnv) hostCall(method, absolute string, hdr map[string]string, cookie *http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, absolute, nil)
	req.RemoteAddr = "203.0.113.40:1"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec
}

// middleware404 recognises the content host's own "not found" (as opposed
// to a handler's 404 such as SHARE_NOT_FOUND or a missing file).
func middleware404(rec *httptest.ResponseRecorder) bool {
	code, _ := errCode(rec)
	return rec.Code == 404 && code == "NOT_FOUND" && strings.Contains(rec.Body.String(), `"message":"not found"`)
}

func TestContentOriginRoutingTable(t *testing.T) {
	env := newContentEnv(t)
	const token = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	allowed := []string{
		"/api/v1/files/raw?path=%2Fa.jpg&purpose=media", "/api/v1/files/download?path=%2Fa", "/api/v1/files/zip?token=x",
		"/api/v1/files/thumb?path=%2Fa&size=128", "/api/v1/archives/entry-content?path=%2Fa.zip&inner=b",
		"/api/v1/files/hls/0123456789abcdef/playlist.m3u8", "/api/v1/files/hls/0123456789abcdef/seg_00000.ts",
		PublicSharePrefix + token + "/raw?path=%2F", PublicSharePrefix + token + "/thumb?path=%2F&size=256",
		PublicSharePrefix + token + "/download?path=%2F", PublicSharePrefix + token + "/zip?id=x",
	}
	denied := []string{
		"/", "/files", "/login", "/s/x", "/healthz", "/readyz", "/api/v1/version", "/api/v1/auth/me", "/api/v1/auth/csrf",
		"/api/v1/files/list?path=%2F", "/api/v1/files/content?path=%2Fa", "/api/v1/files/subtitle?path=%2Fa",
		"/api/v1/files/hls/", "/api/v1/files/hls", "/api/v1/files/rawx", "/api/v1/files/raw/x",
		PublicSharePrefix + token, PublicSharePrefix + token + "/list", PublicSharePrefix + token + "/content?path=%2F",
		PublicSharePrefix + token + "/raw/x", PublicSharePrefix + "x/y/raw", "/api/v1/public/shares",
	}
	for _, p := range allowed {
		if !ContentRouteAllowed(strings.SplitN(p, "?", 2)[0]) {
			t.Errorf("ContentRouteAllowed(%s) = false", p)
		}
		for _, m := range []string{http.MethodGet, http.MethodHead} {
			rec := env.hostCall(m, testContentBase+p, nil, nil)
			if rec.Code == 302 || middleware404(rec) {
				t.Errorf("content host %s %s: %d %s (want the handler, not the host filter)", m, p, rec.Code, rec.Body.String())
			}
			if rec.Header().Get("Set-Cookie") != "" || rec.Header().Get("Access-Control-Allow-Origin") != testAppHost {
				t.Errorf("content host %s %s headers: %v", m, p, rec.Header())
			}
			for _, host := range []string{testAppHost, testOtherHost} {
				rec := env.hostCall(m, host+p, nil, nil)
				if rec.Code != 302 || rec.Header().Get("Location") != testContentBase+p {
					t.Errorf("%s %s %s: %d Location=%q (want 302 → %s)", host, m, p, rec.Code, rec.Header().Get("Location"), testContentBase+p)
				}
				if rec.Header().Get("Access-Control-Allow-Origin") != "" || rec.Body.Len() != 0 {
					t.Errorf("%s redirect leaks CORS or body: %v %q", host, rec.Header(), rec.Body.String())
				}
			}
		}
		// preflight
		rec := env.hostCall(http.MethodOptions, testContentBase+p, map[string]string{"Origin": testAppHost, "Access-Control-Request-Method": "GET", "Access-Control-Request-Headers": "range"}, nil)
		h := rec.Header()
		if rec.Code != 204 || rec.Body.Len() != 0 {
			t.Errorf("preflight %s: %d %q", p, rec.Code, rec.Body.String())
		}
		for k, want := range map[string]string{
			"Access-Control-Allow-Origin": testAppHost, "Access-Control-Allow-Methods": corsAllowMethods,
			"Access-Control-Allow-Headers": corsAllowHeaders, "Access-Control-Max-Age": corsMaxAge,
			"Access-Control-Expose-Headers": corsExposeHeaders, "Cross-Origin-Resource-Policy": "cross-origin",
			"Referrer-Policy": "no-referrer",
		} {
			if got := h.Get(k); got != want {
				t.Errorf("preflight %s %s = %q, want %q", p, k, got, want)
			}
		}
		if h.Get("Access-Control-Allow-Credentials") != "" || !strings.Contains(h.Get("Vary"), "Origin") {
			t.Errorf("preflight %s: credentials=%q vary=%q", p, h.Get("Access-Control-Allow-Credentials"), h.Get("Vary"))
		}
		// other methods on the content host are not routed
		for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
			if rec := env.hostCall(m, testContentBase+p, nil, nil); !middleware404(rec) {
				t.Errorf("content host %s %s: %d (want 404)", m, p, rec.Code)
			}
		}
	}
	for _, p := range denied {
		if ContentRouteAllowed(strings.SplitN(p, "?", 2)[0]) {
			t.Errorf("ContentRouteAllowed(%s) = true", p)
		}
		for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodPost} {
			rec := env.hostCall(m, testContentBase+p, nil, nil)
			if !middleware404(rec) {
				t.Errorf("content host %s %s: %d %.80q (want the host 404)", m, p, rec.Code, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); strings.Contains(ct, "text/html") {
				t.Errorf("content host %s %s served HTML", m, p)
			}
			if rec.Header().Get("Set-Cookie") != "" {
				t.Errorf("content host %s %s set a cookie", m, p)
			}
		}
		// the app host still serves them (never a redirect)
		rec := env.hostCall(http.MethodGet, testAppHost+p, nil, nil)
		if rec.Code == 302 {
			t.Errorf("app host GET %s redirected", p)
		}
	}
	// app-host POSTs on content paths are not redirected (POST /files/zip, /files/hls stay cookie routes)
	for _, p := range []string{"/api/v1/files/zip", "/api/v1/files/hls", PublicSharePrefix + token + "/zip"} {
		if rec := env.hostCall(http.MethodPost, testAppHost+p, nil, nil); rec.Code == 302 {
			t.Errorf("app host POST %s redirected", p)
		}
	}
	// host comparison: case-insensitive, default port dropped
	for _, host := range []string{"http://CONTENT.example.test", "http://content.example.test:80"} {
		if rec := env.hostCall(http.MethodGet, host+"/api/v1/auth/me", nil, nil); !middleware404(rec) {
			t.Errorf("%s not recognised as the content host: %d", host, rec.Code)
		}
	}
	if rec := env.hostCall(http.MethodGet, "http://content.example.test:8443/api/v1/auth/me", nil, nil); middleware404(rec) {
		t.Error("a different port must not be the content host")
	}
}

func TestContentOriginIgnoresCookies(t *testing.T) {
	env := newContentEnv(t)
	ck, hdr := readyAdmin(t, env)
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	if err := os.WriteFile(filepath.Join(home, "foto.jpg"), jpegBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	// a valid session on the content host is worthless
	if rec := env.hostCall(http.MethodGet, testContentBase+"/api/v1/files/list?path=%2F", hdr, ck); !middleware404(rec) || rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("session on content host: %d %v", rec.Code, rec.Header())
	}
	raw := signURL(t, env, ck, hdr, "/foto.jpg", "media")
	if !strings.HasPrefix(raw, testContentBase+RawPath+"?") {
		t.Fatalf("signed url not absolute: %s", raw)
	}
	bad := strings.Replace(raw, "sig=", "sig=x", 1)
	rec := env.hostCall(http.MethodGet, bad, hdr, ck)
	if rec.Code != 403 || rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("wrong signature + valid cookie: %d %v", rec.Code, rec.Header())
	}
	if code, _ := errCode(rec); code != "SIGNATURE_INVALID" {
		t.Fatalf("code %s", code)
	}
	// the genuine signed URL works without any cookie and carries CORS/CORP
	rec = env.hostCall(http.MethodGet, raw, map[string]string{"Origin": testAppHost}, nil)
	h := rec.Header()
	if rec.Code != 200 || h.Get("Content-Type") != "image/jpeg" || !strings.HasPrefix(h.Get("Content-Disposition"), "inline") {
		t.Fatalf("raw on content host: %d %v", rec.Code, h)
	}
	for k, want := range map[string]string{
		"Access-Control-Allow-Origin": testAppHost, "Access-Control-Expose-Headers": corsExposeHeaders,
		"Cross-Origin-Resource-Policy": "cross-origin", "Referrer-Policy": "no-referrer",
		"X-Content-Type-Options": "nosniff", "Content-Security-Policy": download.RawCSP, "Cache-Control": download.CacheControl,
	} {
		if got := h.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if h.Get("Set-Cookie") != "" || h.Get("Access-Control-Allow-Credentials") != "" || !strings.Contains(h.Get("Vary"), "Origin") {
		t.Errorf("headers: %v", h)
	}
	// Range through CORS (pdf.js / hls.js)
	rec = env.hostCall(http.MethodGet, raw, map[string]string{"Origin": testAppHost, "Range": "bytes=0-9"}, nil)
	if rec.Code != 206 || rec.Body.Len() != 10 || rec.Header().Get("Access-Control-Allow-Origin") != testAppHost {
		t.Errorf("range on content host: %d %d", rec.Code, rec.Body.Len())
	}
	// an error on the content host (expired/invalid) still carries CORS so the
	// client can read SIGNATURE_EXPIRED
	if rec := env.hostCall(http.MethodGet, bad, nil, nil); rec.Header().Get("Access-Control-Allow-Origin") != testAppHost {
		t.Error("error response without CORS header")
	}
}

func TestContentOriginSignedURLsAbsoluteAndHostIndependent(t *testing.T) {
	env := newContentEnv(t)
	ck, hdr := readyAdmin(t, env)
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	if err := os.WriteFile(filepath.Join(home, "foto.jpg"), jpegBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	for purpose, route := range map[string]string{"media": RawPath, "inline": RawPath, "download": DownloadPath, "thumb": ThumbPath} {
		u := signURL(t, env, ck, hdr, "/foto.jpg", purpose)
		if !strings.HasPrefix(u, testContentBase+route+"?") {
			t.Errorf("%s: %s", purpose, u)
		}
		// the HMAC does not cover the host: the same query verifies on any host
		rel := strings.TrimPrefix(u, testContentBase)
		// (thumbnails answer 501 here: the test env has no pipeline)
		if rec := env.hostCall(http.MethodGet, testContentBase+rel, nil, nil); rec.Code != 200 && rec.Code != 202 && (purpose != "thumb" || rec.Code != 501) {
			t.Errorf("%s on content host: %d %s", purpose, rec.Code, rec.Body.String())
		}
		if rec := env.hostCall(http.MethodGet, testAppHost+rel, nil, nil); rec.Code != 302 || rec.Header().Get("Location") != u {
			t.Errorf("%s on app host: %d %q", purpose, rec.Code, rec.Header().Get("Location"))
		}
	}
	// batch + items
	r := env.call(http.MethodPost, "/api/v1/files/sign", "203.0.113.5:1", map[string]any{"paths": []string{"/foto.jpg"}, "purpose": "thumb", "size": 128}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("batch: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if u := r.body["urls"].([]any)[0].(map[string]any)["url"].(string); !strings.HasPrefix(u, testContentBase+ThumbPath+"?") {
		t.Errorf("batch url: %s", u)
	}
	// zip
	r = env.call(http.MethodPost, "/api/v1/files/zip", "203.0.113.5:1", map[string]any{"paths": []string{"/foto.jpg"}}, hdr, ck)
	zu, _ := r.body["url"].(string)
	if r.rec.Code != 200 || !strings.HasPrefix(zu, testContentBase+ZipPath+"?") {
		t.Fatalf("zip: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if rec := env.hostCall(http.MethodGet, zu, nil, nil); rec.Code != 200 || rec.Header().Get("Content-Type") != "application/zip" || rec.Header().Get("Cross-Origin-Resource-Policy") != "cross-origin" {
		t.Errorf("zip on content host: %d %v", rec.Code, rec.Header())
	}
	// signer level: same key, same message with or without base
	secret := []byte(strings.Repeat("ab", 32))
	p := download.Params{Scope: "u:1", Path: "/a|b.jpg", Purpose: download.PurposeMedia, Exp: 1900000000}
	plain, based := download.NewSigner(secret, nil), download.NewSigner(secret, nil).WithBase(testContentBase+"/")
	if plain.Sign(p) != based.Sign(p) {
		t.Error("base changed the signature")
	}
	if based.URL(p) != testContentBase+plain.URL(p) || based.Base() != testContentBase || plain.Base() != "" {
		t.Errorf("URL: %s vs %s", based.URL(p), plain.URL(p))
	}
	if download.NewSigner(secret, nil).WithBase("").URL(p) != plain.URL(p) {
		t.Error("empty base must give the relative URL")
	}
	// config exposes the origin to the client
	me := env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.5:1", nil, hdr, ck)
	if me.body["config"].(map[string]any)["contentBaseUrl"] != testContentBase {
		t.Errorf("config.contentBaseUrl: %v", me.body["config"])
	}
	plainEnv := newAuthEnv(t)
	ck2, hdr2 := readyAdmin(t, plainEnv)
	me = plainEnv.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.5:1", nil, hdr2, ck2)
	if v, ok := me.body["config"].(map[string]any)["contentBaseUrl"]; !ok || v != "" {
		t.Errorf("empty mode config.contentBaseUrl: %v (%v)", v, ok)
	}
}

func TestContentOriginHLS(t *testing.T) {
	env := newContentEnv(t, "TEST_HLS_STUB=1")
	ck, hdr := readyAdmin(t, env)
	ip := "203.0.113.5:1"
	dupWrite(t, filepath.Join(env.cfg.StorageRoot, "admin"), "film.mkv", "fake video")
	if r := env.call(http.MethodPut, "/api/v1/admin/settings", ip, map[string]any{"transcode.enabled": true}, hdr, ck); r.rec.Code != 200 {
		t.Fatalf("enable: %d %s", r.rec.Code, r.rec.Body.String())
	}
	adminID := env.call(http.MethodGet, "/api/v1/auth/me", ip, nil, hdr, ck).body["user"].(map[string]any)["id"].(string)
	r := env.call(http.MethodPost, "/api/v1/files/hls", ip, map[string]any{"path": "/film.mkv"}, hdr, ck)
	if r.rec.Code != 202 {
		t.Fatalf("start: %d %s", r.rec.Code, r.rec.Body.String())
	}
	playlist, _ := r.body["playlistUrl"].(string)
	id := r.body["task"].(map[string]any)["id"].(string)
	if !strings.HasPrefix(playlist, testContentBase+HLSPrefix+id+"/playlist.m3u8?") {
		t.Fatalf("playlistUrl not on the content origin: %s", playlist)
	}
	env.waitHLSState(t, adminID, id, "done")
	rec := env.hostCall(http.MethodGet, playlist, map[string]string{"Origin": testAppHost}, nil)
	if rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != testAppHost || rec.Header().Get("Cross-Origin-Resource-Policy") != "cross-origin" {
		t.Fatalf("playlist on content host: %d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	var segment string
	for _, line := range strings.Split(rec.Body.String(), "\n") {
		if strings.HasPrefix(line, "seg_") {
			segment = line
			break
		}
	}
	if segment == "" || strings.Contains(segment, "://") {
		t.Fatalf("segment URI must be relative: %q", segment)
	}
	pu, _ := url.Parse(playlist)
	segURL := pu.Scheme + "://" + pu.Host + HLSPrefix + id + "/" + segment
	if rec := env.hostCall(http.MethodGet, segURL, map[string]string{"Origin": testAppHost, "Range": "bytes=0-99"}, nil); rec.Code != 206 || rec.Header().Get("Content-Type") != hlsSegmentType || rec.Header().Get("Access-Control-Allow-Origin") != testAppHost {
		t.Errorf("segment on content host: %d %v", rec.Code, rec.Header())
	}
	rel := strings.TrimPrefix(playlist, testContentBase)
	if rec := env.hostCall(http.MethodGet, testAppHost+rel, nil, nil); rec.Code != 302 || rec.Header().Get("Location") != playlist {
		t.Errorf("playlist on app host: %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestContentOriginCSP(t *testing.T) {
	if BuildCSP("") != FinalCSP || BuildCSP("  ") != FinalCSP {
		t.Fatal("empty origin must keep FinalCSP")
	}
	csp := BuildCSP("https://c.example/")
	for _, want := range []string{
		"img-src 'self' data: blob: https://c.example;", "media-src 'self' blob: https://c.example;",
		"connect-src 'self' https://c.example;", "frame-src https://c.example;", "object-src 'none'", "frame-ancestors 'none'",
		"script-src 'self' 'wasm-unsafe-eval';", "default-src 'self';",
	} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP lacks %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "frame-src 'none'") || strings.Count(csp, "frame-src") != 1 {
		t.Errorf("frame-src: %s", csp)
	}
	// served: app host HTML carries it, API answers keep APICSP, content host serves no HTML
	env := newContentEnv(t)
	// (httptest marks an https:// target as TLS → upgrade-insecure-requests)
	rec := env.hostCall(http.MethodGet, testAppHost+"/files", nil, nil)
	if got := rec.Header().Get("Content-Security-Policy"); got != BuildCSP(testContentBase)+"; upgrade-insecure-requests" {
		t.Errorf("HTTPS HTML CSP = %q", got)
	}
	rec = env.hostCall(http.MethodGet, testOtherHost+"/files", nil, nil)
	if got := rec.Header().Get("Content-Security-Policy"); got != BuildCSP(testContentBase) {
		t.Errorf("HTML CSP = %q", got)
	}
	if rec := env.hostCall(http.MethodGet, testAppHost+"/api/v1/version", nil, nil); rec.Header().Get("Content-Security-Policy") != APICSP {
		t.Errorf("API CSP = %q", rec.Header().Get("Content-Security-Policy"))
	}
	if rec := env.hostCall(http.MethodGet, testContentBase+"/files", nil, nil); !middleware404(rec) || rec.Header().Get("Content-Security-Policy") != APICSP {
		t.Errorf("content host /files: %d %q", rec.Code, rec.Header().Get("Content-Security-Policy"))
	}
	// empty mode: untouched (TestSecurityHeadersFinal covers the full set)
	plain := newAuthEnv(t)
	if got := plain.call(http.MethodGet, "/files", "203.0.113.5:1", nil, nil, nil).rec.Header().Get("Content-Security-Policy"); got != FinalCSP {
		t.Errorf("empty mode CSP = %q", got)
	}
}

func TestContentOriginHTMLPreview(t *testing.T) {
	env := newContentEnv(t)
	ck, hdr := readyAdmin(t, env)
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	const doc = "<!doctype html><html><body><script>parent.postMessage('x','*')</script><p>halo</p></body></html>"
	for name, body := range map[string]string{"page.html": doc, "teks.htm": "halo tanpa tag", "logo.svg": `<svg xmlns="http://www.w3.org/2000/svg"><script>1</script></svg>`, "page.txt": doc} {
		if err := os.WriteFile(filepath.Join(home, name), []byte(body), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	wantCSP := download.HTMLPreviewCSP + "; frame-ancestors " + testAppHost
	for _, name := range []string{"page.html", "teks.htm"} {
		u := signURL(t, env, ck, hdr, "/"+name, "inline")
		rec := env.hostCall(http.MethodGet, u, map[string]string{"Origin": testAppHost}, nil)
		h := rec.Header()
		if rec.Code != 200 || h.Get("Content-Type") != download.HTMLPreviewContentType || !strings.HasPrefix(h.Get("Content-Disposition"), "inline;") {
			t.Fatalf("%s preview: %d %v", name, rec.Code, h)
		}
		if h.Get("Content-Security-Policy") != wantCSP || h.Get("X-Frame-Options") != "" || h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s preview headers: csp=%q xfo=%q", name, h.Get("Content-Security-Policy"), h.Get("X-Frame-Options"))
		}
		if h.Get("Access-Control-Allow-Origin") != testAppHost || h.Get("Cross-Origin-Resource-Policy") != "cross-origin" || h.Get("Set-Cookie") != "" {
			t.Errorf("%s preview CORS: %v", name, h)
		}
		if !strings.Contains(rec.Body.String(), "halo") {
			t.Errorf("%s body: %s", name, rec.Body.String())
		}
		// same URL on the app host is a redirect, never inline HTML
		rel := strings.TrimPrefix(u, testContentBase)
		if rec := env.hostCall(http.MethodGet, testAppHost+rel, nil, ck); rec.Code != 302 {
			t.Errorf("%s on app host: %d", name, rec.Code)
		}
	}
	// purpose media, SVG, and a .txt that holds HTML keep the attachment policy
	for _, c := range []struct{ name, purpose string }{{"page.html", "media"}, {"logo.svg", "inline"}, {"page.txt", "inline"}} {
		u := signURL(t, env, ck, hdr, "/"+c.name, c.purpose)
		rec := env.hostCall(http.MethodGet, u, nil, nil)
		h := rec.Header()
		if rec.Code != 200 || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment;") || h.Get("Content-Security-Policy") != download.RawCSP {
			t.Errorf("%s %s: %d %v", c.name, c.purpose, rec.Code, h)
		}
		if c.name != "page.txt" && h.Get("Content-Type") != download.OctetStream {
			t.Errorf("%s %s content-type %q", c.name, c.purpose, h.Get("Content-Type"))
		}
	}
	// empty CONTENT_BASE_URL: HTML stays an octet-stream attachment on the app origin
	plain := newAuthEnv(t)
	ck2, hdr2 := readyAdmin(t, plain)
	if err := os.WriteFile(filepath.Join(plain.cfg.StorageRoot, "admin", "page.html"), []byte(doc), 0o640); err != nil {
		t.Fatal(err)
	}
	u := signURL(t, plain, ck2, hdr2, "/page.html", "inline")
	rec := plain.call(http.MethodGet, u, "203.0.113.5:1", nil, nil, nil).rec
	if rec.Code != 200 || rec.Header().Get("Content-Type") != download.OctetStream || !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment;") || rec.Header().Get("Content-Security-Policy") != download.RawCSP {
		t.Errorf("empty mode html: %d %v", rec.Code, rec.Header())
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" || rec.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("empty mode headers changed: %v", rec.Header())
	}
}

func TestContentOriginPublicShare(t *testing.T) {
	env := newContentEnv(t)
	ck, hdr, home := seedShareFixture(t, env)
	if err := os.WriteFile(filepath.Join(home, "laporan", "foto.jpg"), jpegBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "laporan", "page.html"), []byte("<p>halo</p>"), 0o640); err != nil {
		t.Fatal(err)
	}
	id, token := createShare(t, env, ck, hdr, map[string]any{"path": "/laporan", "password": "rahasia-1"})
	guest := "198.51.100.7:1"
	// meta advertises the content origin; locked → sign refused like any guest call
	meta := env.call(http.MethodGet, pub(token, ""), guest, nil, nil, nil)
	if meta.rec.Code != 200 || meta.body["contentBaseUrl"] != testContentBase {
		t.Fatalf("meta: %d %s", meta.rec.Code, meta.rec.Body.String())
	}
	if r := env.call(http.MethodPost, pub(token, "/sign"), guest, map[string]any{"path": "/foto.jpg", "purpose": "media"}, nil, nil); r.rec.Code != 401 {
		t.Fatalf("sign while locked: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r := env.call(http.MethodPost, pub(token, "/unlock"), guest, map[string]any{"password": "rahasia-1"}, nil, nil)
	if r.rec.Code != 204 {
		t.Fatalf("unlock: %d %s", r.rec.Code, r.rec.Body.String())
	}
	cookie := shareCookie(t, r.rec, token)
	sign := func(body map[string]any) resp {
		return env.call(http.MethodPost, pub(token, "/sign"), guest, body, nil, cookie)
	}
	r = sign(map[string]any{"path": "/foto.jpg", "purpose": "media"})
	raw, _ := r.body["url"].(string)
	if r.rec.Code != 200 || !strings.HasPrefix(raw, testContentBase+pub(token, "/raw?")) {
		t.Fatalf("sign: %d %s", r.rec.Code, r.rec.Body.String())
	}
	q := mustQuery(raw)
	if q.Get("scope") != download.ShareScope(id) || q.Get("purpose") != "media" || q.Get("sig") == "" || q.Get("path") != "/foto.jpg" {
		t.Fatalf("query: %v", q)
	}
	// the signed URL works on the content host without any cookie
	rec := env.hostCall(http.MethodGet, raw, map[string]string{"Origin": testAppHost}, nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/jpeg" || rec.Header().Get("Access-Control-Allow-Origin") != testAppHost || rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("public raw on content host: %d %v", rec.Code, rec.Header())
	}
	// tampered, expired-style, and cookie-only requests are refused there
	bad := strings.Replace(raw, "sig=", "sig=x", 1)
	if rec := env.hostCall(http.MethodGet, bad, nil, cookie); rec.Code != 403 {
		t.Errorf("tampered: %d", rec.Code)
	}
	if rec := env.hostCall(http.MethodGet, testContentBase+pub(token, "/raw?path=%2Ffoto.jpg"), nil, cookie); rec.Code != 403 {
		t.Errorf("cookie only on content host: %d %s", rec.Code, rec.Body.String())
	}
	// purpose is bound to the route: a media signature does not open /download
	dl := strings.Replace(raw, "/raw?", "/download?", 1)
	if rec := env.hostCall(http.MethodGet, dl, nil, nil); rec.Code != 403 {
		t.Errorf("media signature on download: %d", rec.Code)
	}
	// another share's signature does not verify here
	id2, token2 := createShare(t, env, ck, hdr, map[string]any{"path": "/laporan/2026"})
	cross := strings.Replace(raw, token, token2, 1)
	if rec := env.hostCall(http.MethodGet, cross, nil, nil); rec.Code != 403 {
		t.Errorf("cross-share signature (%s): %d", id2, rec.Code)
	}
	// app host: the cookie route redirects to the content origin
	if rec := env.hostCall(http.MethodGet, testAppHost+pub(token, "/raw?path=%2Ffoto.jpg"), nil, cookie); rec.Code != 302 || !strings.HasPrefix(rec.Header().Get("Location"), testContentBase+pub(token, "/raw?")) {
		t.Errorf("app host public raw: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// thumb, download, batch, HTML preview, zip
	r = sign(map[string]any{"path": "/foto.jpg", "purpose": "thumb", "size": 128})
	if tu, _ := r.body["url"].(string); r.rec.Code != 200 || !strings.HasPrefix(tu, testContentBase+pub(token, "/thumb?")) || mustQuery(tu).Get("size") != "128" {
		t.Errorf("thumb sign: %d %s", r.rec.Code, r.rec.Body.String())
	} else if rec := env.hostCall(http.MethodGet, tu, nil, nil); rec.Code != 200 && rec.Code != 202 && rec.Code != 501 { // 501: no thumbnail pipeline in tests
		t.Errorf("thumb on content host: %d %s", rec.Code, rec.Body.String())
	}
	r = sign(map[string]any{"path": "/foto.jpg", "purpose": "download"})
	du, _ := r.body["url"].(string)
	if r.rec.Code != 200 || !strings.HasPrefix(du, testContentBase+pub(token, "/download?")) {
		t.Fatalf("download sign: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if rec := env.hostCall(http.MethodGet, du, nil, nil); rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment;") {
		t.Errorf("download on content host: %d %v", rec.Code, rec.Header())
	}
	r = sign(map[string]any{"items": []map[string]any{{"path": "/foto.jpg", "purpose": "media"}, {"path": "/../x", "purpose": "media"}, {"path": "/page.html", "purpose": "inline"}}})
	if r.rec.Code != 200 || len(r.body["urls"].([]any)) != 2 || len(r.body["invalid"].([]any)) != 1 {
		t.Fatalf("batch sign: %d %s", r.rec.Code, r.rec.Body.String())
	}
	hu := r.body["urls"].([]any)[1].(map[string]any)["url"].(string)
	rec = env.hostCall(http.MethodGet, hu, nil, nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != download.HTMLPreviewContentType || rec.Header().Get("Content-Security-Policy") != download.HTMLPreviewCSP+"; frame-ancestors "+testAppHost {
		t.Errorf("public html preview: %d %v", rec.Code, rec.Header())
	}
	r = env.call(http.MethodPost, pub(token, "/zip"), guest, map[string]any{"paths": []string{"/"}}, nil, cookie)
	zu, _ := r.body["url"].(string)
	if r.rec.Code != 200 || !strings.HasPrefix(zu, testContentBase+pub(token, "/zip?")) || mustQuery(zu).Get("sig") == "" {
		t.Fatalf("public zip: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if rec := env.hostCall(http.MethodGet, zu, nil, nil); rec.Code != 200 || rec.Header().Get("Content-Type") != "application/zip" {
		t.Errorf("public zip on content host: %d %v", rec.Code, rec.Header())
	}
	// validation + preview-only
	for _, body := range []map[string]any{{}, {"path": "/foto.jpg", "purpose": "zip"}, {"path": "/foto.jpg", "purpose": "thumb", "size": 99}, {"path": "/x", "items": []any{}}} {
		if r := sign(body); r.rec.Code != 400 {
			t.Errorf("sign %v: %d", body, r.rec.Code)
		}
	}
	_, ptoken := createShare(t, env, ck, hdr, map[string]any{"path": "/laporan/ringkasan.txt", "allowDownload": false})
	if r := env.call(http.MethodPost, pub(ptoken, "/sign"), guest, map[string]any{"path": "/", "purpose": "download"}, nil, nil); r.rec.Code != 403 {
		t.Errorf("preview-only download sign: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := env.call(http.MethodPost, pub(ptoken, "/sign"), guest, map[string]any{"path": "/", "purpose": "media"}, nil, nil); r.rec.Code != 200 {
		t.Errorf("file link media sign: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// empty CONTENT_BASE_URL: the endpoint exists, URLs stay relative and the
	// cookie route is untouched (no contentBaseUrl in meta)
	plain := newAuthEnv(t)
	pck, phdr, phome := seedShareFixture(t, plain)
	if err := os.WriteFile(filepath.Join(phome, "laporan", "foto.jpg"), jpegBytes, 0o640); err != nil {
		t.Fatal(err)
	}
	_, ptok := createShare(t, plain, pck, phdr, map[string]any{"path": "/laporan"})
	if m := plain.call(http.MethodGet, pub(ptok, ""), guest, nil, nil, nil); m.body["contentBaseUrl"] != nil {
		t.Errorf("empty mode meta exposes contentBaseUrl: %v", m.body["contentBaseUrl"])
	}
	r = plain.call(http.MethodPost, pub(ptok, "/sign"), guest, map[string]any{"path": "/foto.jpg", "purpose": "media"}, nil, nil)
	if u, _ := r.body["url"].(string); r.rec.Code != 200 || !strings.HasPrefix(u, pub(ptok, "/raw?")) {
		t.Errorf("empty mode sign: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if rec := plain.call(http.MethodGet, pub(ptok, "/raw?path=%2Ffoto.jpg"), guest, nil, nil, nil).rec; rec.Code != 200 || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("empty mode public raw: %d %v", rec.Code, rec.Header())
	}
}
