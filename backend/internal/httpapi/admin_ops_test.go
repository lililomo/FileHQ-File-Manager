package httpapi

import (
	"context"
	"encoding/csv"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/tasks"
)

// Step 19b: admin audit/tasks/shares/maintenance over HTTP and the audit
// coverage of SPEC §8.16 (one row per listed action).

func (e *authEnv) auditActions(t *testing.T, action string) int {
	t.Helper()
	rows, err := e.store.ListAudit(context.Background(), db.AuditFilter{Action: &action, Limit: 1000})
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

func (e *authEnv) waitTask(t *testing.T, id string) tasks.Task {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := e.tasks.Wait(ctx, id); err != nil {
		t.Fatal(err)
	}
	tk, err := e.tasks.GetAny(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return tk
}

func TestAdminAuditCoverage(t *testing.T) {
	env := newAuthEnv(t, "ADMIN_CAN_BROWSE_USERS=true")
	const pw = "Rahasia sekali 2026!"
	ack, ahdr, home := seedShareFixture(t, env) // auth.login, auth.password_changed (readyAdmin)
	ip := "203.0.113.5:1"
	call := func(method, path string, body any, want int) resp {
		t.Helper()
		r := env.call(method, path, ip, body, ahdr, ack)
		if r.rec.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, r.rec.Code, r.rec.Body.String())
		}
		return r
	}
	// auth.*
	if r, _, _ := env.login(t, "admin", "salah-sekali-123", "198.51.100.1:1"); r.rec.Code != 401 {
		t.Fatal("wrong password accepted")
	}
	// users: create → home provisioned on first login → update/reset/disable/enable/revoke/delete
	r := call(http.MethodPost, "/api/v1/admin/users", map[string]any{"username": "eko", "displayName": "Eko"}, 201)
	ekoID := userOf(t, r)["id"].(string)
	tmp := r.body["temporaryPassword"].(string)
	lr, eck, ecsrf := env.login(t, "eko", tmp, "198.51.100.2:1")
	if lr.rec.Code != 200 {
		t.Fatal("eko login")
	}
	if r := env.call(http.MethodPost, "/api/v1/auth/change-password", "198.51.100.2:1", map[string]string{"currentPassword": tmp, "newPassword": pw}, map[string]string{"X-CSRF-Token": ecsrf}, eck); r.rec.Code != 204 {
		t.Fatalf("eko change password: %d", r.rec.Code)
	}
	if r := env.call(http.MethodPost, "/api/v1/auth/logout", "198.51.100.2:1", nil, map[string]string{"X-CSRF-Token": ecsrf}, eck); r.rec.Code != 204 {
		t.Fatalf("logout: %d", r.rec.Code)
	}
	for i := range 5 { // lockout after 5 failures
		env.login(t, "eko", "bukan-password-"+string(rune('a'+i)), "198.51.100.3:1")
	}
	call(http.MethodPatch, "/api/v1/admin/users/"+ekoID, map[string]any{"displayName": "Eko S"}, 200)
	call(http.MethodPost, "/api/v1/admin/users/"+ekoID+"/reset-password", nil, 200)
	call(http.MethodPost, "/api/v1/admin/users/"+ekoID+"/revoke-sessions", nil, 204)
	call(http.MethodPost, "/api/v1/admin/users/"+ekoID+"/disable", nil, 200)
	call(http.MethodPost, "/api/v1/admin/users/"+ekoID+"/enable", nil, 200)
	// admin browses eko's folder (ADMIN_CAN_BROWSE_USERS)
	call(http.MethodGet, "/api/v1/files/list?path=/&as_user=eko", nil, 200)
	// settings
	call(http.MethodPut, "/api/v1/admin/settings", map[string]any{"activity_retention_days": 30}, 200)
	call(http.MethodPost, "/api/v1/admin/settings/reset", map[string]any{"keys": []string{"activity_retention_days"}}, 200)
	// shares: create/update, public access, admin revoke, delete
	id, token := createShare(t, env, ack, ahdr, map[string]any{"path": "/laporan", "expiresInDays": 1})
	call(http.MethodPatch, "/api/v1/shares/"+id, map[string]any{"note": "tim"}, 200)
	if r := env.call(http.MethodGet, pub(token, "/download?path=/ringkasan.txt"), "198.51.100.4:1", nil, nil, nil); r.rec.Code != 200 {
		t.Fatalf("public download: %d %s", r.rec.Code, r.rec.Body.String())
	}
	call(http.MethodPost, "/api/v1/admin/shares/"+id+"/revoke", nil, 204)
	call(http.MethodDelete, "/api/v1/shares/"+id, nil, 204)
	// files: delete permanently, empty the trash
	if err := os.WriteFile(filepath.Join(home, "hapus.txt"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	call(http.MethodPost, "/api/v1/files/delete-permanent", map[string]any{"paths": []string{"/hapus.txt"}}, 200)
	call(http.MethodPost, "/api/v1/trash/empty", map[string]any{"confirm": "HAPUS"}, 200)
	// admin cancels a user's task
	env.tasks.Register("test_block", tasks.Spec{Unit: tasks.UnitItems, Run: func(ctx context.Context, _ *tasks.Runtime) (any, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}})
	blk, err := env.tasks.Enqueue(context.Background(), tasks.EnqueueRequest{UserID: ekoID, Type: "test_block", Title: "blok"})
	if err != nil {
		t.Fatal(err)
	}
	call(http.MethodPost, "/api/v1/admin/tasks/"+blk.ID+"/cancel", nil, 200)
	if tk := env.waitTask(t, blk.ID); tk.State != tasks.Cancelled {
		t.Fatalf("cancelled task state %s", tk.State)
	}
	// maintenance: recalc after an out-of-band write → quota.drift; backup
	if err := os.WriteFile(filepath.Join(home, "diam-diam.bin"), make([]byte, 64<<10), 0o640); err != nil {
		t.Fatal(err)
	}
	rc := call(http.MethodPost, "/api/v1/admin/maintenance/recalc-quota", map[string]any{}, 202)
	if tk := env.waitTask(t, rc.body["task"].(map[string]any)["id"].(string)); tk.State != tasks.Done {
		t.Fatalf("recalc %s %+v", tk.State, tk.Error)
	}
	// second recalc on a clean counter: the out-of-band write above was the drift
	if err := os.WriteFile(filepath.Join(home, "lagi.bin"), make([]byte, 64<<10), 0o640); err != nil {
		t.Fatal(err)
	}
	rc = call(http.MethodPost, "/api/v1/admin/maintenance/recalc-quota", nil, 202)
	env.waitTask(t, rc.body["task"].(map[string]any)["id"].(string))
	// delete eko (purge) last
	dr := call(http.MethodDelete, "/api/v1/admin/users/"+ekoID, map[string]any{"confirmUsername": "eko", "mode": "purge"}, 202)
	env.waitTask(t, dr.body["task"].(map[string]any)["id"].(string))

	for _, action := range []string{
		"system.seed_admin", "auth.login", "auth.login_failed", "auth.account_locked", "auth.logout", "auth.password_changed",
		"user.create", "user.update", "user.reset_password", "user.disable", "user.enable", "user.delete", "user.home_provisioned",
		"user.revoke_sessions", "settings.update", "settings.reset", "share.create", "share.update", "share.delete",
		"share.public_access", "share.admin_revoked", "file.delete_permanent", "trash.empty", "admin.browse_user",
		"admin.task_cancel", "maintenance.recalc_quota", "quota.drift",
	} {
		if env.auditActions(t, action) == 0 {
			t.Errorf("no audit row for %s", action)
		}
	}
	// nothing in the audit log carries a password or the share token
	page := call(http.MethodGet, "/api/v1/admin/audit?limit=500", nil, 200)
	raw := page.rec.Body.String()
	for _, secret := range []string{tmp, pw, token, initialAdminPassword} {
		if strings.Contains(raw, secret) {
			t.Fatalf("secret leaked into the audit log")
		}
	}
}

func TestAdminOpsHTTP(t *testing.T) {
	env := newAuthEnv(t)
	ack, ahdr, _ := seedShareFixture(t, env)
	bck, bhdr := env.newUser(t, "dedi", "Rahasia sekali 2026!")
	ip := "203.0.113.5:1"
	// non-admin: 403 on every Step 19b route
	for _, rt := range []struct{ method, path string }{
		{"GET", "/api/v1/admin/audit"}, {"GET", "/api/v1/admin/audit/export.csv"}, {"GET", "/api/v1/admin/tasks"},
		{"POST", "/api/v1/admin/tasks/x/cancel"}, {"GET", "/api/v1/admin/shares"}, {"POST", "/api/v1/admin/shares/x/revoke"},
		{"GET", "/api/v1/admin/maintenance"}, {"POST", "/api/v1/admin/maintenance/backup-db"}, {"GET", "/api/v1/admin/maintenance/backups"},
		{"GET", "/api/v1/admin/maintenance/backups/app-20261022-000000.db"},
	} {
		if r := env.call(rt.method, rt.path, "203.0.113.9:1", nil, bhdr, bck); r.rec.Code != 403 {
			t.Fatalf("%s %s as user: %d", rt.method, rt.path, r.rec.Code)
		}
	}
	// audit list + filters + cursor
	r := env.call(http.MethodGet, "/api/v1/admin/audit?action=auth.login&limit=1", ip, nil, ahdr, ack)
	if r.rec.Code != 200 || len(r.body["items"].([]any)) != 1 || r.body["nextCursor"] == nil {
		t.Fatalf("audit list: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/admin/audit?action=auth.login&cursor="+r.body["nextCursor"].(string), ip, nil, ahdr, ack)
	if r.rec.Code != 200 {
		t.Fatalf("audit page 2: %d", r.rec.Code)
	}
	if r = env.call(http.MethodGet, "/api/v1/admin/audit?from=kemarin", ip, nil, ahdr, ack); r.rec.Code != 400 {
		t.Fatalf("bad from: %d", r.rec.Code)
	}
	// export: headers + CSV
	r = env.call(http.MethodGet, "/api/v1/admin/audit/export.csv?action=auth.", ip, nil, ahdr, ack)
	if r.rec.Code != 200 || r.rec.Header().Get("Content-Type") != "text/csv; charset=utf-8" ||
		!strings.HasPrefix(r.rec.Header().Get("Content-Disposition"), `attachment; filename="audit-`) {
		t.Fatalf("export headers: %d %v", r.rec.Code, r.rec.Header())
	}
	recs, err := csv.NewReader(strings.NewReader(strings.TrimPrefix(r.rec.Body.String(), "\ufeff"))).ReadAll()
	if err != nil || len(recs) < 3 || recs[0][4] != "action" || !strings.HasPrefix(recs[1][4], "auth.") {
		t.Fatalf("export body: %v %v", err, recs)
	}
	if r = env.call(http.MethodGet, "/api/v1/admin/audit/export.csv?to=x", ip, nil, ahdr, ack); r.rec.Code != 400 {
		t.Fatalf("export bad filter: %d", r.rec.Code)
	}
	// tasks: user filter, unknown id
	if r = env.call(http.MethodGet, "/api/v1/admin/tasks?user=dedi&state=running", ip, nil, ahdr, ack); r.rec.Code != 200 {
		t.Fatalf("tasks: %d", r.rec.Code)
	}
	if r = env.call(http.MethodPost, "/api/v1/admin/tasks/nope/cancel", ip, nil, ahdr, ack); r.rec.Code != 404 {
		t.Fatalf("cancel unknown: %d", r.rec.Code)
	}
	// shares: metadata only (no url/token/slug), owner filter, revoke → owner notified, 403 on enable
	id, token := createShare(t, env, ack, ahdr, map[string]any{"path": "/laporan", "expiresInDays": 1, "password": "kata-sandi-share-1"})
	r = env.call(http.MethodGet, "/api/v1/admin/shares?mode=public&owner=admin", ip, nil, ahdr, ack)
	items := r.body["items"].([]any)
	if r.rec.Code != 200 || len(items) != 1 || strings.Contains(r.rec.Body.String(), token) || strings.Contains(r.rec.Body.String(), `"url"`) {
		t.Fatalf("admin shares: %s", r.rec.Body.String())
	}
	sh := items[0].(map[string]any)
	if sh["ownerUsername"] != "admin" || sh["name"] != "laporan" || sh["hasPassword"] != true || sh["status"] != "active" {
		t.Fatalf("share view %v", sh)
	}
	if r = env.call(http.MethodGet, "/api/v1/admin/shares?owner=dedi", ip, nil, ahdr, ack); len(r.body["items"].([]any)) != 0 {
		t.Fatal("owner filter")
	}
	if r = env.call(http.MethodGet, "/api/v1/admin/shares?status=weird", ip, nil, ahdr, ack); r.rec.Code != 400 {
		t.Fatalf("bad status: %d", r.rec.Code)
	}
	if r = env.call(http.MethodPost, "/api/v1/admin/shares/"+id+"/revoke", ip, nil, ahdr, ack); r.rec.Code != 204 {
		t.Fatalf("revoke: %d", r.rec.Code)
	}
	if r = env.call(http.MethodPost, "/api/v1/admin/shares/nope/revoke", ip, nil, ahdr, ack); r.rec.Code != 404 {
		t.Fatalf("revoke unknown: %d", r.rec.Code)
	}
	if r = env.call(http.MethodGet, pub(token, ""), "198.51.100.4:1", nil, nil, nil); r.rec.Code != 410 {
		t.Fatalf("revoked link: %d", r.rec.Code)
	}
	if r = env.call(http.MethodPost, "/api/v1/shares/"+id+"/enable", ip, nil, ahdr, ack); r.rec.Code == 200 {
		t.Fatalf("owner re-enabled an admin-revoked share")
	}
	notes, _ := env.store.ListNotifications(context.Background(), userIDOf(t, env, "admin"), false, "", "", 20)
	found := false
	for _, n := range notes {
		found = found || n.Type == "share_revoked_by_admin"
	}
	if !found {
		t.Fatal("owner not notified")
	}
	// maintenance: status + run backup → file listed and downloadable
	r = env.call(http.MethodPost, "/api/v1/admin/maintenance/backup-db", ip, map[string]any{"label": "uji"}, ahdr, ack)
	if r.rec.Code != 202 {
		t.Fatalf("run backup: %d %s", r.rec.Code, r.rec.Body.String())
	}
	env.waitTask(t, r.body["task"].(map[string]any)["id"].(string))
	r = env.call(http.MethodGet, "/api/v1/admin/maintenance", ip, nil, ahdr, ack)
	if r.rec.Code != 200 || len(r.body["jobs"].([]any)) != 7 || len(r.body["backups"].([]any)) != 1 {
		t.Fatalf("maintenance status: %s", r.rec.Body.String())
	}
	name := r.body["backups"].([]any)[0].(map[string]any)["name"].(string)
	r = env.call(http.MethodGet, "/api/v1/admin/maintenance/backups/"+name, ip, nil, ahdr, ack)
	if r.rec.Code != 200 || !strings.HasPrefix(r.rec.Body.String(), "SQLite format 3") {
		t.Fatalf("backup download: %d", r.rec.Code)
	}
	if r = env.call(http.MethodGet, "/api/v1/admin/maintenance/backups/..%2Fdb%2Fapp.db", ip, nil, ahdr, ack); r.rec.Code != 404 {
		t.Fatalf("traversal: %d", r.rec.Code)
	}
	if r = env.call(http.MethodPost, "/api/v1/admin/maintenance/purge-trash", ip, map[string]any{"force": true}, ahdr, ack); r.rec.Code != 400 {
		t.Fatalf("force without HAPUS: %d", r.rec.Code)
	}
	if r = env.call(http.MethodPost, "/api/v1/admin/maintenance/nope", ip, nil, ahdr, ack); r.rec.Code != 404 {
		t.Fatalf("unknown job: %d", r.rec.Code)
	}
	// integrity check finds a folder without an account
	if err := os.MkdirAll(filepath.Join(env.cfg.StorageRoot, "yatim"), 0o750); err != nil {
		t.Fatal(err)
	}
	r = env.call(http.MethodPost, "/api/v1/admin/maintenance/integrity-check", ip, nil, ahdr, ack)
	tk := env.waitTask(t, r.body["task"].(map[string]any)["id"].(string))
	if tk.State != tasks.Done || !strings.Contains(string(tk.Result), `"orphanFolders":[{"name":"yatim","isDir":true}]`) {
		t.Fatalf("integrity: %s %s", tk.State, tk.Result)
	}
}

func userIDOf(t *testing.T, env *authEnv, username string) string {
	t.Helper()
	u, err := env.store.GetUserByUsername(context.Background(), username)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}
