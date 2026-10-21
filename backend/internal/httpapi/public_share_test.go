package httpapi

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

// createShare makes a public link through the API and returns id + token/slug.
func createShare(t *testing.T, env *authEnv, ck *http.Cookie, hdr map[string]string, body map[string]any) (id, token string) {
	t.Helper()
	r := env.call(http.MethodPost, "/api/v1/shares", "203.0.113.5:1", body, hdr, ck)
	if r.rec.Code != 201 {
		t.Fatalf("create share %v: %d %s", body, r.rec.Code, r.rec.Body.String())
	}
	sh := shareOf(t, r)
	return sh["id"].(string), strings.TrimPrefix(r.body["url"].(string), env.cfg.AppBaseURL+"/s/")
}

func pub(token, suffix string) string { return PublicSharePrefix + token + suffix }

// shareCookie extracts the API-path unlock cookie from an unlock response.
func shareCookie(t *testing.T, rec *httptest.ResponseRecorder, token string) *http.Cookie {
	t.Helper()
	var api *http.Cookie
	paths := map[string]bool{}
	for _, c := range rec.Result().Cookies() {
		if strings.HasPrefix(c.Name, "fm_share_") {
			paths[c.Path] = true
			if !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.MaxAge <= 0 || c.MaxAge > 3600 {
				t.Fatalf("cookie flags %+v", c)
			}
			if c.Path == strings.TrimSuffix(PublicSharePrefix, "/")+"/"+token {
				api = c
			}
		}
	}
	if api == nil || !paths["/s/"+token] {
		t.Fatalf("unlock cookies: %v", paths)
	}
	return api
}

func TestPublicShareFolderFlow(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr, _ := seedShareFixture(t, env)
	id, token := createShare(t, env, ck, hdr, map[string]any{"path": "/laporan", "password": "rahasia-1", "expiresInDays": 1, "maxDownloads": 3, "note": "untuk tim"})
	_, other := createShare(t, env, ck, hdr, map[string]any{"path": "/laporan/2026", "password": "rahasia-2"})
	guest := "198.51.100.7:1"

	// locked meta reveals only the password state; listing needs the password
	r := env.call(http.MethodGet, pub(token, ""), guest, nil, nil, nil)
	if r.rec.Code != 200 || r.body["passwordRequired"] != true || r.body["unlocked"] != false || r.body["name"] != nil || r.body["note"] != nil {
		t.Fatalf("locked meta: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if rp := r.rec.Header().Get("Referrer-Policy"); rp != "no-referrer" {
		t.Fatalf("Referrer-Policy %q", rp)
	}
	r = env.call(http.MethodGet, pub(token, "/list"), guest, nil, nil, nil)
	if r.rec.Code != 401 || r.code(t) != "SHARE_PASSWORD_REQUIRED" {
		t.Fatalf("list locked: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// wrong password ×3, then the 11th attempt from one IP is rate limited
	for i := 0; i < 3; i++ {
		r = env.call(http.MethodPost, pub(token, "/unlock"), guest, map[string]string{"password": "salah"}, nil, nil)
		if r.rec.Code != 401 || r.code(t) != "SHARE_PASSWORD_WRONG" {
			t.Fatalf("wrong %d: %d %s", i, r.rec.Code, r.rec.Body.String())
		}
	}
	for i := 0; i < 7; i++ {
		env.call(http.MethodPost, pub(token, "/unlock"), guest, map[string]string{"password": "salah"}, nil, nil)
	}
	r = env.call(http.MethodPost, pub(token, "/unlock"), guest, map[string]string{"password": "rahasia-1"}, nil, nil)
	if r.rec.Code != 429 || r.rec.Header().Get("Retry-After") == "" {
		t.Fatalf("11th attempt: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// another IP: correct password → 204 with the two cookies
	guest2 := "198.51.100.8:1"
	r = env.call(http.MethodPost, pub(token, "/unlock"), guest2, map[string]string{"password": "rahasia-1"}, nil, nil)
	if r.rec.Code != 204 {
		t.Fatalf("unlock: %d %s", r.rec.Code, r.rec.Body.String())
	}
	sc := shareCookie(t, r.rec, token)
	// the cookie is bound to this link: the other link stays locked
	r = env.call(http.MethodGet, pub(other, "/list"), guest2, nil, nil, &http.Cookie{Name: "fm_share_" + id, Value: sc.Value})
	if r.rec.Code != 401 {
		t.Fatalf("cookie reused on another link: %d", r.rec.Code)
	}
	// meta now shows the item and counts one view per visitor
	r = env.call(http.MethodGet, pub(token, ""), guest2, nil, nil, sc)
	if r.rec.Code != 200 || r.body["name"] != "laporan" || r.body["isDir"] != true || r.body["owner"] != "Admin" && r.body["owner"] != "admin" || r.body["note"] != "untuk tim" {
		t.Fatalf("meta: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// listing: relative paths, no parent above the share, dotfiles hidden
	r = env.call(http.MethodGet, pub(token, "/list?path=/"), guest2, nil, nil, sc)
	if r.rec.Code != 200 {
		t.Fatalf("list: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r.body["parentPath"] != nil || r.body["path"] != "/" {
		t.Fatalf("root listing leaks a parent: %s", r.rec.Body.String())
	}
	names := itemNames(t, r)
	if names != "2026,ringkasan.txt" {
		t.Fatalf("names %q", names)
	}
	for _, it := range r.body["items"].([]any) {
		m := it.(map[string]any)
		if strings.Contains(m["path"].(string), "laporan") || m["favorite"] != false || m["note"] != nil {
			t.Fatalf("item leaks owner data: %v", m)
		}
	}
	r = env.call(http.MethodGet, pub(token, "/list?path=/2026"), guest2, nil, nil, sc)
	if r.rec.Code != 200 || r.body["parentPath"] != "/" || itemNames(t, r) != "q1.txt" {
		t.Fatalf("sub listing: %d %s", r.rec.Code, r.rec.Body.String())
	}
	for _, bad := range []string{"/list?path=../", "/list?path=/../rahasia.txt", "/list?path=/..", "/stat?path=/../rahasia.txt", "/raw?path=../rahasia.txt", "/download?path=/2026/../../rahasia.txt"} {
		r = env.call(http.MethodGet, pub(token, bad), guest2, nil, nil, sc)
		if r.rec.Code != 400 {
			t.Fatalf("escape %s: %d %s", bad, r.rec.Code, r.rec.Body.String())
		}
	}
	r = env.call(http.MethodGet, pub(token, "/stat?path=/rahasia.txt"), guest2, nil, nil, sc)
	if r.rec.Code != 404 {
		t.Fatalf("file outside the share by name: %d", r.rec.Code)
	}
	// raw (preview) is not counted; download counts; zip counts once; limit 3
	r = env.call(http.MethodGet, pub(token, "/raw?path=/ringkasan.txt"), guest2, nil, nil, sc)
	if r.rec.Code != 200 || r.rec.Body.String() != "isi ringkasan" {
		t.Fatalf("raw: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, pub(token, "/download?path=/ringkasan.txt"), guest2, nil, nil, sc)
	if r.rec.Code != 200 || !strings.HasPrefix(r.rec.Header().Get("Content-Disposition"), "attachment") || r.rec.Body.String() != "isi ringkasan" {
		t.Fatalf("download: %d %q", r.rec.Code, r.rec.Header().Get("Content-Disposition"))
	}
	r = env.call(http.MethodPost, pub(token, "/zip"), guest2, map[string]any{"paths": []string{"/2026", "/ringkasan.txt"}}, nil, sc)
	if r.rec.Code != 200 {
		t.Fatalf("zip create: %d %s", r.rec.Code, r.rec.Body.String())
	}
	zipURL := r.body["url"].(string)
	if !strings.HasPrefix(zipURL, pub(token, "/zip?id=")) {
		t.Fatalf("zip url %q", zipURL)
	}
	r = env.call(http.MethodGet, zipURL, guest2, nil, nil, sc)
	if r.rec.Code != 200 || r.rec.Header().Get("Content-Type") != "application/zip" || r.rec.Body.Len() < 100 {
		t.Fatalf("zip get: %d %s", r.rec.Code, r.rec.Header().Get("Content-Type"))
	}
	r = env.call(http.MethodGet, pub(token, "/download?path=/2026/q1.txt"), guest2, nil, nil, sc)
	if r.rec.Code != 200 {
		t.Fatalf("download 3: %d", r.rec.Code)
	}
	r = env.call(http.MethodGet, pub(token, "/download?path=/2026/q1.txt"), guest2, nil, nil, sc)
	if r.rec.Code != 410 || r.code(t) != "SHARE_LIMIT_REACHED" {
		t.Fatalf("limit: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, pub(token, ""), guest2, nil, nil, sc)
	if r.body["downloadLimitReached"] != true || r.body["downloadCount"] != float64(3) {
		t.Fatalf("meta after limit: %s", r.rec.Body.String())
	}
	// owner sees counts, logs and the badge
	r = env.call(http.MethodGet, "/api/v1/shares/"+id, "203.0.113.5:1", nil, nil, ck)
	sh := shareOf(t, r)
	// two meta calls without the fm_pv cookie = two visitors = two views
	if sh["downloadCount"] != float64(3) || sh["viewCount"] != float64(2) || sh["lastAccessAt"] == nil {
		t.Fatalf("owner view: %s", r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/shares/"+id+"/access-logs", "203.0.113.5:1", nil, nil, ck)
	logs := r.body["items"].([]any)
	actions := map[string]int{}
	for _, l := range logs {
		m := l.(map[string]any)
		actions[m["action"].(string)]++
		if ip := m["ipTrunc"].(string); ip != "198.51.100.0" {
			t.Fatalf("ip not truncated: %s", ip)
		}
	}
	if actions["unlock_fail"] != 10 || actions["unlock_ok"] != 1 || actions["download"] != 2 || actions["zip"] != 1 || actions["denied"] < 1 || actions["view"] != 2 {
		t.Fatalf("log actions %v", actions)
	}
	// public audit rows name the owner, never the token
	audits, _ := env.store.ListAudit(context.Background(), db.AuditFilter{Action: strPtr("share.public_access"), Limit: 50})
	if len(audits) != 4 {
		t.Fatalf("public_access audit rows %d", len(audits))
	}
	for _, a := range audits {
		details := ""
		if a.DetailsJson != nil {
			details = *a.DetailsJson
		}
		if strings.Contains(details, token) || !strings.Contains(details, `"owner":"admin"`) {
			t.Fatalf("audit details %s", details)
		}
	}
	// expiry: 25 h later the link is gone for guests, "expired" for the owner
	env.clock.Advance(25 * time.Hour)
	r = env.call(http.MethodGet, pub(token, ""), guest2, nil, nil, sc)
	if r.rec.Code != 410 || r.code(t) != "SHARE_EXPIRED" {
		t.Fatalf("expired: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// the owner's session idled out with the clock: log in again
	_, ck2, _ := env.login(t, "admin", readyPassword, "203.0.113.5:1")
	r = env.call(http.MethodGet, "/api/v1/shares/"+id, "203.0.113.5:1", nil, nil, ck2)
	if shareOf(t, r)["status"] != "expired" {
		t.Fatalf("status: %s", r.rec.Body.String())
	}
	// unknown tokens: NOT_FOUND, then rate limited per IP
	for i := 0; i < 30; i++ {
		env.call(http.MethodGet, pub(strings.Repeat("Q", 43), ""), "198.51.100.9:1", nil, nil, nil)
	}
	r = env.call(http.MethodGet, pub(strings.Repeat("Q", 43), ""), "198.51.100.9:1", nil, nil, nil)
	if r.rec.Code != 429 {
		t.Fatalf("enumeration: %d", r.rec.Code)
	}
}

func TestPublicShareLifecycleAndPreviewOnly(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr, home := seedShareFixture(t, env)
	id, token := createShare(t, env, ck, hdr, map[string]any{"path": "/laporan", "allowDownload": false, "slug": "tim-laporan", "hideOwner": true})
	if token != "tim-laporan" {
		t.Fatalf("slug link %q", token)
	}
	guest := "198.51.100.20:1"
	r := env.call(http.MethodGet, pub(token, ""), guest, nil, nil, nil)
	if r.rec.Code != 200 || r.body["allowDownload"] != false || r.body["owner"] != nil || r.body["unlocked"] != true {
		t.Fatalf("meta: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// preview only: raw works, download/zip refuse
	r = env.call(http.MethodGet, pub(token, "/raw?path=/ringkasan.txt"), guest, nil, nil, nil)
	if r.rec.Code != 200 {
		t.Fatalf("raw: %d", r.rec.Code)
	}
	r = env.call(http.MethodGet, pub(token, "/download?path=/ringkasan.txt"), guest, nil, nil, nil)
	if r.rec.Code != 403 || r.code(t) != "FORBIDDEN" {
		t.Fatalf("download preview-only: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, pub(token, "/zip"), guest, map[string]any{"paths": []string{"/"}}, nil, nil)
	if r.rec.Code != 403 {
		t.Fatalf("zip preview-only: %d", r.rec.Code)
	}
	// content + media-info + stat work for guests
	r = env.call(http.MethodGet, pub(token, "/content?path=/ringkasan.txt"), guest, nil, nil, nil)
	if r.rec.Code != 200 || r.body["content"] != "isi ringkasan" || r.body["readOnly"] != true {
		t.Fatalf("content: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, pub(token, "/media-info?path=/ringkasan.txt"), guest, nil, nil, nil)
	if r.rec.Code != 200 || r.body["gps"] != nil {
		t.Fatalf("media-info: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// disable → 410, enable → back
	r = env.call(http.MethodPost, "/api/v1/shares/"+id+"/disable", "203.0.113.5:1", nil, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("disable: %d", r.rec.Code)
	}
	r = env.call(http.MethodGet, pub(token, ""), guest, nil, nil, nil)
	if r.rec.Code != 410 || r.code(t) != "SHARE_DISABLED" {
		t.Fatalf("disabled: %d %s", r.rec.Code, r.rec.Body.String())
	}
	env.call(http.MethodPost, "/api/v1/shares/"+id+"/enable", "203.0.113.5:1", nil, hdr, ck)
	// rename the folder through the API: the link keeps working
	r = env.call(http.MethodPost, "/api/v1/files/rename", "203.0.113.5:1", map[string]any{"path": "/laporan", "newName": "arsip"}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("rename: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, pub(token, "/list"), guest, nil, nil, nil)
	if r.rec.Code != 200 || itemNames(t, r) != "2026,ringkasan.txt" {
		t.Fatalf("after rename: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/shares/"+id, "203.0.113.5:1", nil, nil, ck)
	if shareOf(t, r)["path"] != "/arsip" {
		t.Fatalf("path not rewritten: %s", r.rec.Body.String())
	}
	// delete to the Recycle Bin → item_trashed; restore offers the id; enable
	r = env.call(http.MethodPost, "/api/v1/files/delete", "203.0.113.5:1", map[string]any{"paths": []string{"/arsip"}}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("delete: %d %s", r.rec.Code, r.rec.Body.String())
	}
	trashID := r.body["results"].([]any)[0].(map[string]any)["trashId"].(string)
	r = env.call(http.MethodGet, pub(token, ""), guest, nil, nil, nil)
	if r.rec.Code != 410 || r.code(t) != "SHARE_DISABLED" {
		t.Fatalf("trashed: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if e, _ := r.body["error"].(map[string]any); e["details"].(map[string]any)["reason"] != "item_trashed" {
		t.Fatalf("reason: %s", r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/trash/restore", "203.0.113.5:1", map[string]any{"ids": []string{trashID}}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("restore: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if re, _ := r.body["reactivateShares"].([]any); len(re) != 1 || re[0] != id {
		t.Fatalf("reactivateShares: %s", r.rec.Body.String())
	}
	r = env.call(http.MethodGet, pub(token, ""), guest, nil, nil, nil)
	if r.rec.Code != 410 {
		t.Fatal("restore must not enable automatically")
	}
	r = env.call(http.MethodPost, "/api/v1/shares/"+id+"/enable", "203.0.113.5:1", nil, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("enable after restore: %d", r.rec.Code)
	}
	r = env.call(http.MethodGet, pub(token, "/list"), guest, nil, nil, nil)
	if r.rec.Code != 200 {
		t.Fatalf("after restore: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// item removed outside the app → broken on access, cleared when it is back
	if err := os.Rename(filepath.Join(home, "arsip"), filepath.Join(home, "arsip-x")); err != nil {
		t.Fatal(err)
	}
	r = env.call(http.MethodGet, pub(token, "/list"), guest, nil, nil, nil)
	if r.rec.Code != 410 {
		t.Fatalf("broken: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/shares/"+id, "203.0.113.5:1", nil, nil, ck)
	if shareOf(t, r)["status"] != "broken" {
		t.Fatalf("broken status: %s", r.rec.Body.String())
	}
	if err := os.Rename(filepath.Join(home, "arsip-x"), filepath.Join(home, "arsip")); err != nil {
		t.Fatal(err)
	}
	r = env.call(http.MethodGet, pub(token, "/list"), guest, nil, nil, nil)
	if r.rec.Code != 200 {
		t.Fatalf("repaired: %d", r.rec.Code)
	}
	// owner disabled → link gone; permanent delete → row gone
	if err := env.store.UpdateUserStatus(context.Background(), sqlc.UpdateUserStatusParams{Status: "disabled", UpdatedAt: db.Now(), ID: func() string {
		u, _ := env.store.GetUserByUsername(context.Background(), "admin")
		return u.ID
	}()}); err != nil {
		t.Fatal(err)
	}
	r = env.call(http.MethodGet, pub(token, ""), guest, nil, nil, nil)
	if r.rec.Code != 410 || r.code(t) != "SHARE_DISABLED" {
		t.Fatalf("owner disabled: %d %s", r.rec.Code, r.rec.Body.String())
	}
}

func TestPublicShareFileShare(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr, _ := seedShareFixture(t, env)
	_, token := createShare(t, env, ck, hdr, map[string]any{"path": "/laporan/ringkasan.txt"})
	guest := "198.51.100.30:1"
	r := env.call(http.MethodGet, pub(token, ""), guest, nil, nil, nil)
	if r.rec.Code != 200 || r.body["name"] != "ringkasan.txt" || r.body["isDir"] != false || r.body["size"] != float64(13) {
		t.Fatalf("meta: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, pub(token, "/stat"), guest, nil, nil, nil)
	if r.rec.Code != 200 || r.body["path"] != "/" || r.body["name"] != "ringkasan.txt" {
		t.Fatalf("stat: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, pub(token, "/list"), guest, nil, nil, nil)
	if r.rec.Code != 400 {
		t.Fatalf("list on file share: %d", r.rec.Code)
	}
	r = env.call(http.MethodGet, pub(token, "/download?path=/"), guest, nil, nil, nil)
	if r.rec.Code != 200 || r.rec.Body.String() != "isi ringkasan" {
		t.Fatalf("download: %d", r.rec.Code)
	}
	r = env.call(http.MethodGet, pub(token, "/raw?path=/2026/q1.txt"), guest, nil, nil, nil)
	if r.rec.Code != 404 {
		t.Fatalf("sibling through a file share: %d", r.rec.Code)
	}
	r = env.call(http.MethodGet, pub(token, "/content"), guest, nil, nil, nil)
	if r.rec.Code != 200 || r.body["content"] != "isi ringkasan" || r.body["path"] != "/" {
		t.Fatalf("content: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// the SPA page carries no-referrer
	r = env.call(http.MethodGet, "/s/"+token, guest, nil, nil, nil)
	if r.rec.Code != 200 || r.rec.Header().Get("Referrer-Policy") != "no-referrer" || !strings.Contains(r.rec.Header().Get("X-Robots-Tag"), "noindex") {
		t.Fatalf("page headers: %d %v", r.rec.Code, r.rec.Header())
	}
}

func TestPublicShareFileRequestUpload(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr, home := seedShareFixture(t, env)
	id, token := createShare(t, env, ck, hdr, map[string]any{"path": "/laporan", "allowUpload": true, "hideListing": true})
	_, plain := createShare(t, env, ck, hdr, map[string]any{"path": "/laporan/2026"})
	guest := "198.51.100.40:1"
	adminID := func() string {
		u, _ := env.store.GetUserByUsername(context.Background(), "admin")
		return u.ID
	}()
	before, _ := env.quota.Usage(context.Background(), adminID)

	// drop-box: listing hidden, precheck reports a conflict for an existing name
	r := env.call(http.MethodGet, pub(token, "/list"), guest, nil, nil, nil)
	if r.rec.Code != 403 {
		t.Fatalf("hide_listing: %d", r.rec.Code)
	}
	r = env.call(http.MethodPost, pub(token, "/upload/precheck"), guest, map[string]any{"files": []map[string]any{{"name": "ringkasan.txt", "size": 5}, {"name": "baru.txt", "size": 5}}}, nil, nil)
	if r.rec.Code != 200 {
		t.Fatalf("precheck: %d %s", r.rec.Code, r.rec.Body.String())
	}
	items := r.body["items"].([]any)
	if items[0].(map[string]any)["status"] != "conflict" || items[1].(map[string]any)["status"] != "ok" {
		t.Fatalf("precheck verdicts: %s", r.rec.Body.String())
	}
	// a link without allowUpload refuses
	r = env.call(http.MethodPost, pub(plain, "/upload/precheck"), guest, map[string]any{"files": []map[string]any{{"name": "x.txt", "size": 1}}}, nil, nil)
	if r.rec.Code != 403 {
		t.Fatalf("upload disabled: %d %s", r.rec.Code, r.rec.Body.String())
	}

	upload := func(name, content, policy string) (string, resp) {
		req := httptest.NewRequest(http.MethodPost, pub(token, "/upload/tus/"), nil)
		req.RemoteAddr = guest
		req.Header.Set("Tus-Resumable", "1.0.0")
		req.Header.Set("Upload-Length", itoa(len(content)))
		req.Header.Set("Upload-Metadata", tusMeta(map[string]string{"filename": name, "path": "/rahasia-dir", "onConflict": policy}))
		rec := httptest.NewRecorder()
		env.handler.ServeHTTP(rec, req)
		if rec.Code != 201 {
			t.Fatalf("tus create: %d %s", rec.Code, rec.Body.String())
		}
		loc := rec.Header().Get("Location")
		if !strings.Contains(loc, pub(token, "/upload/tus/")) || strings.Contains(loc, "/api/v1/upload/tus/") {
			t.Fatalf("Location must point at the public endpoint: %q", loc)
		}
		path := loc[strings.Index(loc, "/api/v1/"):]
		preq := httptest.NewRequest(http.MethodPatch, path, bytes.NewReader([]byte(content)))
		preq.RemoteAddr = guest
		preq.Header.Set("Tus-Resumable", "1.0.0")
		preq.Header.Set("Upload-Offset", "0")
		preq.Header.Set("Content-Type", "application/offset+octet-stream")
		prec := httptest.NewRecorder()
		env.handler.ServeHTTP(prec, preq)
		return path, resp{rec: prec}
	}
	// a new name lands in the shared folder (the client's path is ignored)
	_, pr := upload("baru.txt", "hello", "overwrite")
	if pr.rec.Code != 204 || pr.rec.Header().Get("Upload-Result") != "created" {
		t.Fatalf("patch: %d %s %v", pr.rec.Code, pr.rec.Body.String(), pr.rec.Header())
	}
	if b, err := os.ReadFile(filepath.Join(home, "laporan", "baru.txt")); err != nil || string(b) != "hello" {
		t.Fatalf("file not in the shared folder: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "rahasia-dir")); err == nil {
		t.Fatal("client destination was honoured")
	}
	// an existing name is never overwritten, even with policy overwrite
	_, pr = upload("ringkasan.txt", "HACK", "overwrite")
	if pr.rec.Code != 204 || pr.rec.Header().Get("Upload-Result") != "renamed" {
		t.Fatalf("overwrite attempt: %d %v", pr.rec.Code, pr.rec.Header())
	}
	if b, _ := os.ReadFile(filepath.Join(home, "laporan", "ringkasan.txt")); string(b) != "isi ringkasan" {
		t.Fatal("original overwritten")
	}
	if _, err := os.Stat(filepath.Join(home, "laporan", "ringkasan (1).txt")); err != nil {
		t.Fatalf("renamed copy missing: %v", err)
	}
	// quota of the owner, access log, notification
	after, _ := env.quota.Usage(context.Background(), adminID)
	if after.Used-before.Used != 9 {
		t.Fatalf("owner quota delta %d", after.Used-before.Used)
	}
	r = env.call(http.MethodGet, "/api/v1/shares/"+id+"/access-logs", "203.0.113.5:1", nil, nil, ck)
	ups := 0
	for _, l := range r.body["items"].([]any) {
		if l.(map[string]any)["action"] == "upload" {
			ups++
		}
	}
	if ups != 2 {
		t.Fatalf("upload log rows %d: %s", ups, r.rec.Body.String())
	}
	notes, err := env.store.ListNotifications(context.Background(), adminID, false, "", "", 10)
	if err != nil || len(notes) != 2 || notes[0].Type != "share_upload" {
		t.Fatalf("notifications: %v %d", err, len(notes))
	}
	// an upload id of this share is invisible through another link; HEAD works on its own
	path, _ := upload("ketiga.txt", "abc", "rename")
	uid := path[strings.LastIndex(path, "/")+1:]
	hreq := httptest.NewRequest(http.MethodHead, pub(plain, "/upload/tus/"+uid), nil)
	hreq.Header.Set("Tus-Resumable", "1.0.0")
	hrec := httptest.NewRecorder()
	env.handler.ServeHTTP(hrec, hreq)
	if hrec.Code != 404 && hrec.Code != 403 {
		t.Fatalf("foreign HEAD: %d", hrec.Code)
	}
	// the private tus endpoint never accepts the public session
	preq := httptest.NewRequest(http.MethodHead, "/api/v1/upload/tus/"+uid, nil)
	preq.Header.Set("Tus-Resumable", "1.0.0")
	prec := httptest.NewRecorder()
	env.handler.ServeHTTP(prec, preq)
	if prec.Code != 401 {
		t.Fatalf("private HEAD without session: %d", prec.Code)
	}
}
