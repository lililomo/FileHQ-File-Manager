package httpapi

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/download"
	fmexec "kppbwi.app/filemanager/internal/exec"
)

// Review C probes (PROMPT Step R-C checklist b, c, d): inline policy matrix
// on /files/raw and /archives/entry-content, signed-URL gates of the archive
// browser, zip streaming refusals and the 7-Zip error path.

var pngBytes, _ = base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")

// activeContent is every type a browser could execute or that can carry a
// script; only the raster image may be served inline.
var activeContent = map[string]string{
	"page.html":     "<!doctype html><html><script>alert(1)</script></html>",
	"logo.svg":      `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`,
	"data.xml":      `<?xml version="1.0"?><!DOCTYPE x [<!ENTITY e SYSTEM "file:///etc/passwd">]><x>&e;</x>`,
	"app.xhtml":     `<?xml version="1.0"?><html xmlns="http://www.w3.org/1999/xhtml"><script>alert(1)</script></html>`,
	"code.js":       "alert(1)",
	"saved.mhtml":   "MIME-Version: 1.0\r\nContent-Type: multipart/related; boundary=\"b\"\r\n\r\n--b\r\nContent-Type: text/html\r\n\r\n<script>alert(1)</script>\r\n--b--\r\n",
	"saved.mht":     "From: <Saved by Blink>\r\nMIME-Version: 1.0\r\nContent-Type: multipart/related; type=\"text/html\"; boundary=\"----MultipartBoundary--x----\"\r\n\r\n------MultipartBoundary--x----\r\nContent-Type: text/html\r\n\r\n<script>alert(1)</script>\r\n------MultipartBoundary--x------\r\n",
	"doc.pdf":       "%PDF-1.7\n1 0 obj\n<< /Type /Catalog /OpenAction << /S /JavaScript /JS (app.alert(1)) >> >>\nendobj\ntrailer\n<< /Root 1 0 R >>\n%%EOF\n",
	"notes.txt.jpg": "<html><body>disguised</body></html>",
	"style.css":     "body{background:url(javascript:alert(1))}",
	"pixel.png":     string(pngBytes),
}

func checkContentPolicy(t *testing.T, where, name string, h http.Header) {
	t.Helper()
	ct := h.Get("Content-Type")
	cd := h.Get("Content-Disposition")
	if h.Get("Content-Security-Policy") != "sandbox; default-src 'none'" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("%s %s: hardening headers missing: %v", where, name, h)
	}
	switch name {
	case "pixel.png":
		if ct != "image/png" || !strings.HasPrefix(cd, "inline;") {
			t.Fatalf("%s %s: raster image should be inline image/png, got %q %q", where, name, ct, cd)
		}
	case "doc.pdf":
		if ct != "application/pdf" || !strings.HasPrefix(cd, "attachment;") {
			t.Fatalf("%s %s: pdf must be an attachment with its own type, got %q %q", where, name, ct, cd)
		}
	default:
		if ct != "application/octet-stream" || !strings.HasPrefix(cd, "attachment;") {
			t.Fatalf("%s %s: active content must be octet-stream attachment, got %q %q", where, name, ct, cd)
		}
	}
}

func TestReviewCInlinePolicyMatrix(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := env.newUser(t, "carol", readyPassword)
	home := filepath.Join(env.cfg.StorageRoot, "carol")
	ip := "203.0.113.77:1"
	for name, body := range activeContent {
		if err := os.WriteFile(filepath.Join(home, name), []byte(body), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	writeHome(t, env, "carol", "bundle.zip", zipBytes(t, activeContent))
	for name := range activeContent {
		for _, purpose := range []string{"media", "inline"} {
			r := env.call(http.MethodGet, signURL(t, env, ck, hdr, "/"+name, purpose), ip, nil, nil, nil)
			if r.rec.Code != 200 {
				t.Fatalf("raw %s %s: %d %s", purpose, name, r.rec.Code, r.rec.Body.String())
			}
			checkContentPolicy(t, "raw?purpose="+purpose, name, r.rec.Header())
		}
		// download purpose: always an attachment, never a dangerous type
		r := env.call(http.MethodGet, signURL(t, env, ck, hdr, "/"+name, "download"), ip, nil, nil, nil)
		if r.rec.Code != 200 || !strings.HasPrefix(r.rec.Header().Get("Content-Disposition"), "attachment;") {
			t.Fatalf("download %s: %d %q", name, r.rec.Code, r.rec.Header().Get("Content-Disposition"))
		}
		if ct := r.rec.Header().Get("Content-Type"); name != "pixel.png" && name != "doc.pdf" && ct != "application/octet-stream" {
			t.Fatalf("download %s: content type %q", name, ct)
		}
		// the same member streamed out of a zip
		sr := env.call(http.MethodPost, "/api/v1/archives/sign", ip, map[string]any{"path": "/bundle.zip", "inner": name}, hdr, ck)
		if sr.rec.Code != 200 {
			t.Fatalf("sign entry %s: %d %s", name, sr.rec.Code, sr.rec.Body.String())
		}
		u, _ := sr.body["url"].(string)
		r = env.call(http.MethodGet, u, ip, nil, nil, nil)
		if r.rec.Code != 200 {
			t.Fatalf("entry-content %s: %d %s", name, r.rec.Code, r.rec.Body.String())
		}
		checkContentPolicy(t, "entry-content", name, r.rec.Header())
	}
}

// TestReviewCArchiveEntryContentGates: the signed entry URL is bound to
// user, archive and member; the session id is bound to its owner; a disabled
// account is refused like every other signed route; the browser endpoints
// refuse scope parameters.
func TestReviewCArchiveEntryContentGates(t *testing.T) {
	env := newAuthEnv(t)
	ckA, hdrA := env.newUser(t, "alice", readyPassword)
	ckB, hdrB := env.newUser(t, "bob", readyPassword)
	ip := "203.0.113.78:1"
	writeHome(t, env, "alice", "a.zip", zipBytes(t, map[string]string{"s/secret.txt": "alice only"}))
	writeHome(t, env, "bob", "a.zip", zipBytes(t, map[string]string{"s/secret.txt": "bob's copy"}))
	sign := func(ck *http.Cookie, hdr map[string]string, body map[string]any) resp {
		return env.call(http.MethodPost, "/api/v1/archives/sign", ip, body, hdr, ck)
	}
	r := sign(ckA, hdrA, map[string]any{"path": "/a.zip", "inner": "s/secret.txt"})
	if r.rec.Code != 200 {
		t.Fatalf("sign: %d %s", r.rec.Code, r.rec.Body.String())
	}
	ua, _ := r.body["url"].(string)
	if strings.Contains(ua, env.cfg.StorageRoot) {
		t.Fatal("host path in signed url")
	}
	// bob signs the same virtual path: his own jail answers
	r = sign(ckB, hdrB, map[string]any{"path": "/a.zip", "inner": "s/secret.txt"})
	ub, _ := r.body["url"].(string)
	if rb := env.call(http.MethodGet, ub, ip, nil, nil, nil); rb.rec.Code != 200 || rb.rec.Body.String() != "bob's copy" {
		t.Fatalf("bob's entry: %d %q", rb.rec.Code, rb.rec.Body.String())
	}
	// swapping the scope of alice's URL to bob's id breaks the signature
	pa, _ := url.Parse(ua)
	pb, _ := url.Parse(ub)
	q := pa.Query()
	q.Set("scope", pb.Query().Get("scope"))
	pa.RawQuery = q.Encode()
	if rr := env.call(http.MethodGet, pa.String(), ip, nil, nil, nil); rr.rec.Code != 403 || rr.code(t) != "SIGNATURE_INVALID" {
		t.Fatalf("scope swap: %d %s", rr.rec.Code, rr.code(t))
	}
	// inner traversal is refused at sign time; purpose swap at serve time
	if rr := sign(ckA, hdrA, map[string]any{"path": "/a.zip", "inner": "../s/secret.txt"}); rr.rec.Code != 400 {
		t.Fatalf("traversal inner: %d", rr.rec.Code)
	}
	if rr := sign(ckA, hdrA, map[string]any{"path": "/a.zip", "inner": "s/secret.txt", "session": strings.Repeat("ab", 16)}); rr.rec.Code != 404 {
		t.Fatalf("unknown session at sign: %d", rr.rec.Code)
	}
	if rr := env.call(http.MethodGet, ua+"&sess="+strings.Repeat("cd", 16), ip, nil, nil, nil); rr.rec.Code != 404 {
		t.Fatalf("unknown session at serve: %d", rr.rec.Code)
	}
	if rr := env.call(http.MethodGet, strings.Replace(ua, "purpose=entry", "purpose=media", 1), ip, nil, nil, nil); rr.rec.Code != 403 {
		t.Fatalf("purpose swap: %d", rr.rec.Code)
	}
	if rr := env.call(http.MethodGet, strings.Replace(ua, "/archives/entry-content", "/files/raw", 1), ip, nil, nil, nil); rr.rec.Code != 403 {
		t.Fatalf("entry url on /files/raw: %d", rr.rec.Code)
	}
	for _, p := range []string{"/api/v1/archives/entries?path=/a.zip&share=x", "/api/v1/archives/entries?path=/a.zip&as_user=bob"} {
		if rr := env.call(http.MethodGet, p, ip, nil, nil, ckA); rr.rec.Code != 400 {
			t.Fatalf("scope param %s: %d", p, rr.rec.Code)
		}
	}
	// disabled account: the still-valid URL is refused
	alice, _ := env.store.GetUserByUsername(context.Background(), "alice")
	if err := env.store.UpdateUserStatus(context.Background(), sqlc.UpdateUserStatusParams{Status: "disabled", UpdatedAt: db.Now(), ID: alice.ID}); err != nil {
		t.Fatal(err)
	}
	if rr := env.call(http.MethodGet, ua, ip, nil, nil, nil); rr.rec.Code != 403 || rr.code(t) != "FORBIDDEN" {
		t.Fatalf("disabled account: %d %s", rr.rec.Code, rr.code(t))
	}
	if strings.Contains(env.logs.String(), env.cfg.StorageRoot) {
		t.Fatal("storage root leaked into logs")
	}
}

// TestReviewCZipRequestRefusals: the zip payload can neither name the root
// nor the reserved folder, and another user's token is unknown.
func TestReviewCZipRequestRefusals(t *testing.T) {
	env := newAuthEnv(t)
	ckA, hdrA := env.newUser(t, "alice", readyPassword)
	ckB, hdrB := env.newUser(t, "bob", readyPassword)
	ip := "203.0.113.79:1"
	seedFiles(t, filepath.Join(env.cfg.StorageRoot, "alice"), "docs/", "docs/a.txt")
	for _, paths := range [][]string{{"/"}, {"/.system"}, {"/.system/db/app.db"}, {"/docs/../../bob"}, {"/docs", "/"}} {
		r := env.call(http.MethodPost, "/api/v1/files/zip", ip, map[string]any{"paths": paths}, hdrA, ckA)
		// the reserved folder answers like a missing path (anti-enumeration), the rest 400
		if r.rec.Code != 400 && r.rec.Code != 404 {
			t.Fatalf("zip %v: want 400/404, got %d %s", paths, r.rec.Code, r.rec.Body.String())
		}
		if r.rec.Code == 404 && paths[0] != "/.system" && paths[0] != "/.system/db/app.db" {
			t.Fatalf("zip %v: want 400, got 404", paths)
		}
	}
	r := env.call(http.MethodPost, "/api/v1/files/zip", ip, map[string]any{"paths": []string{"/docs"}}, hdrA, ckA)
	if r.rec.Code != 200 {
		t.Fatalf("zip docs: %d %s", r.rec.Code, r.rec.Body.String())
	}
	ua, _ := r.body["url"].(string)
	// bob re-signs alice's token under his own scope: payload owner mismatch → 404
	pa, _ := url.Parse(ua)
	token := pa.Query().Get("token")
	rb := env.call(http.MethodPost, "/api/v1/files/sign", ip, map[string]any{"path": "/docs", "purpose": "zip"}, hdrB, ckB)
	if rb.rec.Code != 400 {
		t.Fatalf("files/sign purpose zip must be refused: %d", rb.rec.Code)
	}
	bob, _ := env.store.GetUserByUsername(context.Background(), "bob")
	forged := download.NewSigner(env.cfg.AppSecret, nil).URL(download.Params{Scope: download.UserScope(bob.ID), Purpose: download.PurposeZip, Extra: token, Exp: time.Now().Add(time.Minute).Unix()})
	if rr := env.call(http.MethodGet, forged, ip, nil, nil, nil); rr.rec.Code != 404 {
		t.Fatalf("bob with alice's zip token: %d %s", rr.rec.Code, rr.rec.Body.String())
	}
	if rr := env.call(http.MethodGet, ua, ip, nil, nil, nil); rr.rec.Code != 200 || rr.rec.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("alice zip: %d", rr.rec.Code)
	}
}

// TestReviewCSevenZipErrorHidesHostPath: a corrupt iso goes through `7z l`
// whose stderr names the host path; the API error must not.
func TestReviewCSevenZipErrorHidesHostPath(t *testing.T) {
	const bin = "/usr/lib/7zip/7z"
	if !fmexec.Executable(bin) {
		t.Skip("7z not installed")
	}
	env := newAuthEnv(t, "SEVENZIP_PATH="+bin)
	ck, hdr := env.newUser(t, "dave", readyPassword)
	ip := "203.0.113.80:1"
	fake := make([]byte, 0x8001+5+2048)
	copy(fake[0x8001:], "CD001")
	writeHome(t, env, "dave", "fake.iso", string(fake))
	r := env.call(http.MethodGet, "/api/v1/archives/entries?path=/fake.iso", ip, nil, nil, ck)
	if r.rec.Code == 200 {
		t.Fatal("fake iso listed")
	}
	if body := r.rec.Body.String(); strings.Contains(body, env.cfg.StorageRoot) {
		t.Fatalf("host path leaked in the API error: %s", body)
	}
	r = env.call(http.MethodPost, "/api/v1/archives/extract", ip, map[string]any{"path": "/fake.iso", "dest": "new"}, hdr, ck)
	if r.rec.Code == 202 {
		task := waitTask(t, env, ck, hdr, taskID(t, r), "failed")
		if e, _ := task["error"].(map[string]any); e != nil {
			if msg, _ := e["message"].(string); strings.Contains(msg, env.cfg.StorageRoot) {
				t.Fatalf("host path leaked in the task error: %s", msg)
			}
		}
	}
	if strings.Contains(env.logs.String(), env.cfg.StorageRoot) {
		t.Fatal("storage root leaked into logs")
	}
}
