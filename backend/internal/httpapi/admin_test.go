package httpapi

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/db"
)

// Step 19a: admin API through HTTP — RequireAdmin, user lifecycle with
// side effects, live settings, as_user scope + audit, branding logo.

func adminUsers(t *testing.T, env *authEnv, ck *http.Cookie, hdr map[string]string, query string) resp {
	t.Helper()
	return env.call(http.MethodGet, "/api/v1/admin/users"+query, "203.0.113.5:1", nil, hdr, ck)
}

func createUserAPI(t *testing.T, env *authEnv, ck *http.Cookie, hdr map[string]string, body map[string]any) resp {
	t.Helper()
	return env.call(http.MethodPost, "/api/v1/admin/users", "203.0.113.5:1", body, hdr, ck)
}

func userOf(t *testing.T, r resp) map[string]any {
	t.Helper()
	u, _ := r.body["user"].(map[string]any)
	if u == nil {
		t.Fatalf("no user in body: %s", r.rec.Body.String())
	}
	return u
}

func TestAdminRequireAdminAndUserLifecycle(t *testing.T) {
	env := newAuthEnv(t)
	ack, ahdr := readyAdmin(t, env)
	bck, bhdr := env.newUser(t, "dedi", "Rahasia sekali 2026!")
	ctx := context.Background()

	// non-admin → 403 FORBIDDEN on every admin route; anonymous → 401
	for _, rt := range []struct{ method, path string }{
		{"GET", "/api/v1/admin/users"}, {"GET", "/api/v1/admin/dashboard"}, {"GET", "/api/v1/admin/settings"},
		{"POST", "/api/v1/admin/users"}, {"PUT", "/api/v1/admin/settings"}, {"POST", "/api/v1/admin/branding/logo"},
	} {
		var body any
		if rt.method != "GET" {
			body = map[string]any{}
		}
		r := env.call(rt.method, rt.path, "203.0.113.9:1", body, bhdr, bck)
		if r.rec.Code != 403 || r.code(t) != "FORBIDDEN" {
			t.Fatalf("%s %s as user: %d %s", rt.method, rt.path, r.rec.Code, r.rec.Body.String())
		}
		r = env.call(rt.method, rt.path, "203.0.113.9:1", body, map[string]string{}, nil)
		if r.rec.Code != 401 {
			t.Fatalf("%s %s anonymous: %d", rt.method, rt.path, r.rec.Code)
		}
	}

	// create: validation per AC, then 201 with the password once
	for _, bad := range []string{"www", ".x", "Admin2"} {
		r := createUserAPI(t, env, ack, ahdr, map[string]any{"username": bad})
		if r.rec.Code != 400 || r.code(t) != "VALIDATION_FAILED" {
			t.Fatalf("create %q: %d %s", bad, r.rec.Code, r.rec.Body.String())
		}
	}
	r := createUserAPI(t, env, ack, ahdr, map[string]any{"username": "budi_01", "displayName": "Budi", "quotaBytes": 5 << 20})
	if r.rec.Code != 201 {
		t.Fatalf("create budi_01: %d %s", r.rec.Code, r.rec.Body.String())
	}
	tmp, _ := r.body["temporaryPassword"].(string)
	budi := userOf(t, r)
	if len(tmp) != 16 || budi["mustChangePassword"] != true || budi["homeProvisioned"] != false {
		t.Fatalf("create result: %s", r.rec.Body.String())
	}
	budiID := budi["id"].(string)
	// the list never carries the password; filters work
	r = adminUsers(t, env, ack, ahdr, "?q=budi&status=active")
	if r.rec.Code != 200 || strings.Contains(r.rec.Body.String(), tmp) || !strings.Contains(r.rec.Body.String(), `"username":"budi_01"`) || strings.Contains(r.rec.Body.String(), `"username":"dedi"`) {
		t.Fatalf("list: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r = adminUsers(t, env, ack, ahdr, "?role=root"); r.rec.Code != 400 {
		t.Fatalf("bad role filter: %d", r.rec.Code)
	}
	if r = env.call(http.MethodGet, "/api/v1/admin/users/"+budiID, "203.0.113.5:1", nil, ahdr, ack); r.rec.Code != 200 || userOf(t, r)["username"] != "budi_01" {
		t.Fatalf("get user: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// budi logs in with the temporary password → forced change, home exists
	lr, bck2, bcsrf := env.login(t, "budi_01", tmp, "203.0.113.9:1")
	if lr.rec.Code != 200 || lr.body["mustChangePassword"] != true {
		t.Fatalf("budi login: %d %s", lr.rec.Code, lr.rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(env.cfg.StorageRoot, "budi_01")); err != nil {
		t.Fatalf("home after login: %v", err)
	}
	bhdr2 := map[string]string{"X-CSRF-Token": bcsrf}
	if r = env.call(http.MethodGet, "/api/v1/files/list?path=/", "203.0.113.9:1", nil, bhdr2, bck2); r.rec.Code != 403 || r.code(t) != "PASSWORD_CHANGE_REQUIRED" {
		t.Fatalf("forced change gate: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/auth/change-password", "203.0.113.9:1", map[string]string{"currentPassword": tmp, "newPassword": "Budi-baru-2026-aman!"}, bhdr2, bck2)
	if r.rec.Code != 204 {
		t.Fatalf("budi change password: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// quota 5 MB: an upload precheck of 6 MB is refused by quota
	r = env.call(http.MethodPost, "/api/v1/upload/precheck", "203.0.113.9:1", map[string]any{"path": "/", "files": []map[string]any{{"name": "besar.bin", "size": 6 << 20}}}, bhdr2, bck2)
	if r.rec.Code != 200 || r.body["quotaOk"] != false || r.body["limit"] != float64(5<<20) {
		t.Fatalf("quota precheck: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// last admin: PATCH role=user / disable / DELETE → 409
	adminRow, _ := env.store.GetUserByUsername(ctx, "admin")
	for _, c := range []struct {
		method, path string
		body         any
	}{
		{"PATCH", "/api/v1/admin/users/" + adminRow.ID, map[string]any{"role": "user"}},
		{"POST", "/api/v1/admin/users/" + adminRow.ID + "/disable", map[string]any{}},
		{"DELETE", "/api/v1/admin/users/" + adminRow.ID, map[string]any{"confirmUsername": "admin", "mode": "archive"}},
	} {
		r := env.call(c.method, c.path, "203.0.113.5:1", c.body, ahdr, ack)
		if r.rec.Code != 409 || r.code(t) != "VALIDATION_FAILED" {
			t.Fatalf("last admin %s %s: %d %s", c.method, c.path, r.rec.Code, r.rec.Body.String())
		}
	}

	// disable budi → session dead, login refused, public share 410
	cr := env.call(http.MethodPost, "/api/v1/files/create", "203.0.113.9:1", map[string]any{"path": "/", "name": "catatan.txt", "content": "x"}, bhdr2, bck2)
	if cr.rec.Code != 201 {
		t.Fatalf("budi create file: %d %s", cr.rec.Code, cr.rec.Body.String())
	}
	_, token := createShare(t, env, bck2, bhdr2, map[string]any{"path": "/catatan.txt"})
	if r = env.call(http.MethodGet, pub(token, ""), "203.0.113.50:1", nil, nil, nil); r.rec.Code != 200 {
		t.Fatalf("public share before disable: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/admin/users/"+budiID+"/disable", "203.0.113.5:1", map[string]any{}, ahdr, ack)
	if r.rec.Code != 200 || userOf(t, r)["status"] != "disabled" {
		t.Fatalf("disable: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r = env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.9:1", nil, bhdr2, bck2); r.rec.Code != 401 && r.rec.Code != 403 {
		t.Fatalf("budi session after disable: %d", r.rec.Code)
	}
	if lr, _, _ = env.login(t, "budi_01", "Budi-baru-2026-aman!", "203.0.113.9:1"); lr.rec.Code != 403 || lr.code(t) != "ACCOUNT_DISABLED" {
		t.Fatalf("login while disabled: %d %s", lr.rec.Code, lr.rec.Body.String())
	}
	if r = env.call(http.MethodGet, pub(token, ""), "203.0.113.50:1", nil, nil, nil); r.rec.Code != 410 || r.code(t) != "SHARE_DISABLED" {
		t.Fatalf("public share while owner disabled: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// enable → login works again
	if r = env.call(http.MethodPost, "/api/v1/admin/users/"+budiID+"/enable", "203.0.113.5:1", map[string]any{}, ahdr, ack); r.rec.Code != 200 {
		t.Fatalf("enable: %d %s", r.rec.Code, r.rec.Body.String())
	}
	lr, bck3, bcsrf3 := env.login(t, "budi_01", "Budi-baru-2026-aman!", "203.0.113.9:1")
	if lr.rec.Code != 200 {
		t.Fatalf("login after enable: %d %s", lr.rec.Code, lr.rec.Body.String())
	}
	bhdr3 := map[string]string{"X-CSRF-Token": bcsrf3}
	// reset password → one-time password, must change, old session revoked
	r = env.call(http.MethodPost, "/api/v1/admin/users/"+budiID+"/reset-password", "203.0.113.5:1", map[string]any{}, ahdr, ack)
	npw, _ := r.body["temporaryPassword"].(string)
	if r.rec.Code != 200 || len(npw) != 16 || r.rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("reset: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r = env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.9:1", nil, bhdr3, bck3); r.rec.Code != 401 {
		t.Fatalf("session after reset: %d", r.rec.Code)
	}
	if strings.Contains(env.logs.String(), npw) || strings.Contains(env.logs.String(), tmp) {
		t.Fatal("temporary password reached the log")
	}
	// revoke-sessions → 204; PATCH quota + display name
	lr, bck4, _ := env.login(t, "budi_01", npw, "203.0.113.9:1")
	if lr.rec.Code != 200 {
		t.Fatalf("login with reset password: %d", lr.rec.Code)
	}
	if r = env.call(http.MethodPost, "/api/v1/admin/users/"+budiID+"/revoke-sessions", "203.0.113.5:1", map[string]any{}, ahdr, ack); r.rec.Code != 204 {
		t.Fatalf("revoke: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r = env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.9:1", nil, map[string]string{}, bck4); r.rec.Code != 401 {
		t.Fatalf("session after revoke-sessions: %d", r.rec.Code)
	}
	r = env.call(http.MethodPatch, "/api/v1/admin/users/"+budiID, "203.0.113.5:1", map[string]any{"quotaBytes": 1 << 20, "displayName": "Budi S."}, ahdr, ack)
	if r.rec.Code != 200 || userOf(t, r)["displayName"] != "Budi S." || userOf(t, r)["quota"].(map[string]any)["limit"] != float64(1<<20) {
		t.Fatalf("patch: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r = env.call(http.MethodPatch, "/api/v1/admin/users/"+budiID, "203.0.113.5:1", map[string]any{"quotaBytes": 1}, ahdr, ack); r.rec.Code != 400 {
		t.Fatalf("patch bad quota: %d", r.rec.Code)
	}

	// delete archive: 202 task owned by the admin → folder moved, share gone, user 404
	r = env.call(http.MethodDelete, "/api/v1/admin/users/"+budiID, "203.0.113.5:1", map[string]any{"confirmUsername": "nope", "mode": "archive"}, ahdr, ack)
	if r.rec.Code != 400 {
		t.Fatalf("delete confirm mismatch: %d", r.rec.Code)
	}
	r = env.call(http.MethodDelete, "/api/v1/admin/users/"+budiID, "203.0.113.5:1", map[string]any{"confirmUsername": "budi_01", "mode": "archive"}, ahdr, ack)
	if r.rec.Code != 202 {
		t.Fatalf("delete archive: %d %s", r.rec.Code, r.rec.Body.String())
	}
	task := r.body["task"].(map[string]any)
	if err := env.tasks.Wait(ctx, task["id"].(string)); err != nil {
		t.Fatal(err)
	}
	tr := env.call(http.MethodGet, "/api/v1/tasks/"+task["id"].(string), "203.0.113.5:1", nil, ahdr, ack)
	if tr.rec.Code != 200 || tr.body["task"].(map[string]any)["state"] != "done" || !strings.Contains(tr.rec.Body.String(), `"archivedTo":"deleted-users/budi_01-`) {
		t.Fatalf("delete task: %d %s", tr.rec.Code, tr.rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(env.cfg.StorageRoot, "budi_01")); !os.IsNotExist(err) {
		t.Fatalf("home still present after archive: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Join(env.cfg.DataDir, "deleted-users"))
	if len(entries) != 1 || !strings.HasPrefix(entries[0].Name(), "budi_01-") {
		t.Fatalf("archive folder: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(env.cfg.DataDir, "deleted-users", entries[0].Name(), "catatan.txt")); err != nil {
		t.Fatalf("archived file: %v", err)
	}
	if r = env.call(http.MethodGet, pub(token, ""), "203.0.113.50:1", nil, nil, nil); r.rec.Code != 404 {
		t.Fatalf("public share after archive: %d", r.rec.Code)
	}
	if r = env.call(http.MethodGet, "/api/v1/admin/users/"+budiID, "203.0.113.5:1", nil, ahdr, ack); r.rec.Code != 404 {
		t.Fatalf("deleted user: %d", r.rec.Code)
	}
	// purge dedi: everything gone, row kept soft-deleted
	dedi, _ := env.store.GetUserByUsername(ctx, "dedi")
	r = env.call(http.MethodDelete, "/api/v1/admin/users/"+dedi.ID, "203.0.113.5:1", map[string]any{"confirmUsername": "dedi", "mode": "purge"}, ahdr, ack)
	if r.rec.Code != 202 {
		t.Fatalf("delete purge: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if err := env.tasks.Wait(ctx, r.body["task"].(map[string]any)["id"].(string)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(env.cfg.StorageRoot, "dedi")); !os.IsNotExist(err) {
		t.Fatal("dedi home survived purge")
	}
	if row, err := env.store.GetUserByID(ctx, dedi.ID); err != nil || row.DeletedAt == nil {
		t.Fatalf("purge row: %v", err)
	}
	if r = env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.9:1", nil, bhdr, bck); r.rec.Code != 401 && r.rec.Code != 403 {
		t.Fatalf("dedi session after purge: %d", r.rec.Code)
	}
	// dashboard
	r = env.call(http.MethodGet, "/api/v1/admin/dashboard", "203.0.113.5:1", nil, ahdr, ack)
	if r.rec.Code != 200 || r.body["users"].(map[string]any)["active"] != float64(1) || r.body["storage"].(map[string]any)["disk"].(map[string]any)["total"] == float64(0) || len(r.body["deps"].([]any)) != 8 {
		t.Fatalf("dashboard: %d %s", r.rec.Code, r.rec.Body.String())
	}
}

func TestAdminSettingsLiveAndBranding(t *testing.T) {
	env := newAuthEnv(t)
	ack, ahdr := readyAdmin(t, env)
	bck, bhdr := env.newUser(t, "budi", "Rahasia sekali 2026!")

	// baseline: 20 GB default → a 30 GB precheck is too_large
	precheck := func() resp {
		return env.call(http.MethodPost, "/api/v1/upload/precheck", "203.0.113.5:1", map[string]any{"path": "/", "files": []map[string]any{{"name": "besar.bin", "size": 30 << 30}}}, ahdr, ack)
	}
	if r := precheck(); r.rec.Code != 200 || !strings.Contains(r.rec.Body.String(), `"status":"too_large"`) || !strings.Contains(r.rec.Body.String(), `"maxUploadSize":21474836480`) {
		t.Fatalf("precheck baseline: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// invalid → 400 with details.key
	r := env.call(http.MethodPut, "/api/v1/admin/settings", "203.0.113.5:1", map[string]any{"max_upload_size": "lots"}, ahdr, ack)
	if r.rec.Code != 400 || r.code(t) != "VALIDATION_FAILED" || details(r)["key"] != "max_upload_size" {
		t.Fatalf("invalid setting: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// 50 GB applies at once without restart: precheck accepts (quota then decides)
	r = env.call(http.MethodPut, "/api/v1/admin/settings", "203.0.113.5:1", map[string]any{"max_upload_size": "50GB", "share_require_password": true, "app_name": "Arsip KPP"}, ahdr, ack)
	if r.rec.Code != 200 || !strings.Contains(r.rec.Body.String(), `"key":"max_upload_size","group":"upload","type":"size","value":"50GB"`) {
		t.Fatalf("put settings: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := precheck(); r.rec.Code != 200 || strings.Contains(r.rec.Body.String(), `"too_large"`) || !strings.Contains(r.rec.Body.String(), `"maxUploadSize":53687091200`) {
		t.Fatalf("precheck after override: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// /auth/me config and /version carry the effective values
	r = env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.9:1", nil, bhdr, bck)
	cfg := r.body["config"].(map[string]any)
	if cfg["maxUploadSize"] != float64(50<<30) || cfg["shareRequirePassword"] != true || cfg["appName"] != "Arsip KPP" {
		t.Fatalf("me config: %v", cfg)
	}
	if r = env.call(http.MethodGet, "/api/v1/version", "203.0.113.9:1", nil, nil, nil); r.body["appName"] != "Arsip KPP" {
		t.Fatalf("version: %s", r.rec.Body.String())
	}
	// share.require_password is enforced for budi's new links
	if cr := env.call(http.MethodPost, "/api/v1/files/create", "203.0.113.9:1", map[string]any{"path": "/", "name": "a.txt", "content": "x"}, bhdr, bck); cr.rec.Code != 201 {
		t.Fatalf("create: %d %s", cr.rec.Code, cr.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/shares", "203.0.113.9:1", map[string]any{"path": "/a.txt"}, bhdr, bck)
	if r.rec.Code != 400 || !strings.Contains(r.rec.Body.String(), "password") {
		t.Fatalf("share without password while required: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// GET lists sources; reset returns to env/default and audits
	r = env.call(http.MethodGet, "/api/v1/admin/settings", "203.0.113.5:1", nil, ahdr, ack)
	if r.rec.Code != 200 || !strings.Contains(r.rec.Body.String(), `"source":"override"`) || !strings.Contains(r.rec.Body.String(), `"key":"password_min_length"`) {
		t.Fatalf("get settings: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/admin/settings/reset", "203.0.113.5:1", map[string]any{"keys": []string{"max_upload_size", "share_require_password"}}, ahdr, ack)
	if r.rec.Code != 200 || env.cfg.Runtime().MaxUploadSize != 20<<30 || env.cfg.Runtime().ShareRequirePassword {
		t.Fatalf("reset: %d %s", r.rec.Code, r.rec.Body.String())
	}
	rows, _ := env.store.ListAudit(context.Background(), db.AuditFilter{Action: db.Ptr(audit.ActionSettingsUpdate), Limit: 10})
	if len(rows) != 3 {
		t.Fatalf("settings.update audits: %d", len(rows))
	}

	// branding: non-PNG 415, PNG ok, served publicly, delete → 404
	mp := func(name string, data []byte) (*bytes.Buffer, string) {
		var buf bytes.Buffer
		w := multipart.NewWriter(&buf)
		fw, _ := w.CreateFormFile("logo", name)
		_, _ = fw.Write(data)
		_ = w.Close()
		return &buf, w.FormDataContentType()
	}
	upload := func(body *bytes.Buffer, ct string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/branding/logo", body)
		req.RemoteAddr = "203.0.113.5:1"
		req.Header.Set("Content-Type", ct)
		req.Header.Set("X-CSRF-Token", ahdr["X-CSRF-Token"])
		req.AddCookie(ack)
		rec := httptest.NewRecorder()
		env.handler.ServeHTTP(rec, req)
		return rec
	}
	if rec := upload(mp("x.gif", []byte("GIF89a"+strings.Repeat("x", 100)))); rec.Code != 415 {
		t.Fatalf("gif logo: %d %s", rec.Code, rec.Body.String())
	}
	png := append([]byte{0x89, 'P', 'N', 'G', 0x0D, 0x0A, 0x1A, 0x0A}, bytes.Repeat([]byte{1}, 100)...)
	if rec := upload(mp("logo.png", png)); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"logoUrl":"/api/v1/branding/logo"`) {
		t.Fatalf("png logo: %d %s", rec.Code, rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/branding/logo", "203.0.113.50:1", nil, nil, nil)
	if r.rec.Code != 200 || r.rec.Header().Get("Content-Type") != "image/png" || r.rec.Body.Len() != len(png) || r.rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("serve logo: %d %v", r.rec.Code, r.rec.Header())
	}
	if r = env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.9:1", nil, bhdr, bck); r.body["config"].(map[string]any)["logoUrl"] != "/api/v1/branding/logo" {
		t.Fatalf("me logoUrl: %s", r.rec.Body.String())
	}
	if r = env.call(http.MethodDelete, "/api/v1/admin/branding/logo", "203.0.113.5:1", nil, ahdr, ack); r.rec.Code != 204 {
		t.Fatalf("delete logo: %d", r.rec.Code)
	}
	if r = env.call(http.MethodGet, "/api/v1/branding/logo", "203.0.113.50:1", nil, nil, nil); r.rec.Code != 404 {
		t.Fatalf("logo after delete: %d", r.rec.Code)
	}
}

func TestAdminAsUserScope(t *testing.T) {
	// flag off (default): refused for everyone, even admins
	env := newAuthEnv(t)
	ack, ahdr := readyAdmin(t, env)
	env.newUser(t, "budi", "Rahasia sekali 2026!")
	r := env.call(http.MethodGet, "/api/v1/files/list?path=/&as_user=budi", "203.0.113.5:1", nil, ahdr, ack)
	if r.rec.Code != 403 || details(r)["reason"] != "admin_browse_disabled" {
		t.Fatalf("as_user with flag off: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// flag on: admin browses budi's root, every request audited; non-admin 403; unknown 404
	env = newAuthEnv(t, "ADMIN_CAN_BROWSE_USERS=true")
	ack, ahdr = readyAdmin(t, env)
	bck, bhdr := env.newUser(t, "budi", "Rahasia sekali 2026!")
	cck, chdr := env.newUser(t, "citra", "Rahasia sekali 2026!")
	if cr := env.call(http.MethodPost, "/api/v1/files/create", "203.0.113.9:1", map[string]any{"path": "/", "name": "milik-budi.txt", "content": "rahasia budi"}, bhdr, bck); cr.rec.Code != 201 {
		t.Fatalf("budi create: %d", cr.rec.Code)
	}
	r = env.call(http.MethodGet, "/api/v1/files/list?path=/&as_user=budi", "203.0.113.5:1", nil, ahdr, ack)
	if r.rec.Code != 200 || itemNames(t, r) != "milik-budi.txt" {
		t.Fatalf("admin list as budi: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/files/content?path=/milik-budi.txt&as_user=budi", "203.0.113.5:1", nil, ahdr, ack)
	if r.rec.Code != 200 || !strings.Contains(r.rec.Body.String(), "rahasia budi") {
		t.Fatalf("admin content as budi: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// a write lands in budi's root and is journaled as done by the admin
	r = env.call(http.MethodPost, "/api/v1/files/mkdir?as_user=budi", "203.0.113.5:1", map[string]any{"path": "/", "name": "dari-admin"}, ahdr, ack)
	if r.rec.Code != 201 {
		t.Fatalf("admin mkdir as budi: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(env.cfg.StorageRoot, "budi", "dari-admin")); err != nil {
		t.Fatal(err)
	}
	rows, err := env.store.ListAudit(context.Background(), db.AuditFilter{Action: db.Ptr(audit.ActionAdminBrowseUser), Limit: 10})
	if err != nil || len(rows) != 3 {
		t.Fatalf("admin.browse_user audit rows: %d %v", len(rows), err)
	}
	if !strings.Contains(*rows[2].DetailsJson, `"path":"/"`) || !strings.Contains(*rows[2].DetailsJson, `"method":"GET"`) || !strings.Contains(*rows[2].DetailsJson, `"username":"budi"`) || *rows[2].ActorUsername != "admin" {
		t.Fatalf("audit details: %s", *rows[2].DetailsJson)
	}
	// the admin's own listing is untouched
	if r = env.call(http.MethodGet, "/api/v1/files/list?path=/", "203.0.113.5:1", nil, ahdr, ack); r.rec.Code != 200 || strings.Contains(r.rec.Body.String(), "milik-budi") {
		t.Fatalf("admin own list: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// non-admin → 403 (no audit row), unknown → 404, both scopes → 400, trash refuses the scope
	r = env.call(http.MethodGet, "/api/v1/files/list?path=/&as_user=budi", "203.0.113.9:1", nil, chdr, cck)
	if r.rec.Code != 403 || details(r)["reason"] != "admin_required" {
		t.Fatalf("citra as budi: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r = env.call(http.MethodGet, "/api/v1/files/list?path=/&as_user=nobody9", "203.0.113.5:1", nil, ahdr, ack); r.rec.Code != 404 {
		t.Fatalf("unknown as_user: %d", r.rec.Code)
	}
	if r = env.call(http.MethodGet, "/api/v1/files/list?path=/&as_user=budi&share=x", "203.0.113.5:1", nil, ahdr, ack); r.rec.Code != 400 {
		t.Fatalf("both scopes: %d", r.rec.Code)
	}
	if r = env.call(http.MethodGet, "/api/v1/trash?as_user=budi", "203.0.113.5:1", nil, ahdr, ack); r.rec.Code != 400 {
		t.Fatalf("trash as_user: %d", r.rec.Code)
	}
	rows, _ = env.store.ListAudit(context.Background(), db.AuditFilter{Action: db.Ptr(audit.ActionAdminBrowseUser), Limit: 10})
	if len(rows) != 3 { // refusals (403/404/400) never reach the audit: only granted requests do
		t.Fatalf("audit rows after refusals: %d", len(rows))
	}
	_ = time.Second
}
