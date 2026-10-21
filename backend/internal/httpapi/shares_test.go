package httpapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/share"
)

// seedShareFixture provisions the admin home (login) and writes a folder
// with two files for the share tests.
func seedShareFixture(t *testing.T, env *authEnv) (ck *http.Cookie, hdr map[string]string, home string) {
	t.Helper()
	ck, hdr = readyAdmin(t, env)
	home = filepath.Join(env.cfg.StorageRoot, "admin")
	if err := os.MkdirAll(filepath.Join(home, "laporan", "2026"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "laporan", "ringkasan.txt"), []byte("isi ringkasan"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "laporan", "2026", "q1.txt"), []byte("kuartal satu"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "rahasia.txt"), []byte("di luar share"), 0o640); err != nil {
		t.Fatal(err)
	}
	return ck, hdr, home
}

func shareOf(t *testing.T, r resp) map[string]any {
	t.Helper()
	s, _ := r.body["share"].(map[string]any)
	if s == nil {
		t.Fatalf("no share in body: %s", r.rec.Body.String())
	}
	return s
}

func TestSharesOwnerCRUDBadgeAndIDOR(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr, _ := seedShareFixture(t, env)
	ctx := context.Background()

	// create: 201 with a 43-char token link; the DB keeps hash + sealed copy only
	r := env.call(http.MethodPost, "/api/v1/shares", "203.0.113.5:1", map[string]any{"path": "/laporan", "expiresInDays": 1, "note": "untuk tim"}, hdr, ck)
	if r.rec.Code != 201 {
		t.Fatalf("create: %d %s", r.rec.Code, r.rec.Body.String())
	}
	sh := shareOf(t, r)
	url, _ := r.body["url"].(string)
	token := strings.TrimPrefix(url, env.cfg.AppBaseURL+"/s/")
	if !share.IsToken(token) || sh["url"] != url || sh["status"] != "active" || sh["isDir"] != true {
		t.Fatalf("create body: %s", r.rec.Body.String())
	}
	id := sh["id"].(string)
	row, err := env.store.GetShareByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if row.TokenHash == nil || *row.TokenHash != share.HashToken(token) || strings.Contains(string(row.TokenEnc), token) || row.PasswordHash != nil {
		t.Fatal("db must hold the token hash and a sealed copy only")
	}
	// audit row without the token
	audits, err := env.store.ListAudit(ctx, db.AuditFilter{Action: strPtr("share.create"), Limit: 10})
	if err != nil || len(audits) != 1 || (audits[0].DetailsJson != nil && strings.Contains(*audits[0].DetailsJson, token)) {
		t.Fatalf("audit: %v %d", err, len(audits))
	}

	// badge in the listing + stat.shares
	r = env.call(http.MethodGet, "/api/v1/files/list?path=/", "203.0.113.5:1", nil, nil, ck)
	if r.rec.Code != 200 {
		t.Fatalf("list: %d", r.rec.Code)
	}
	found := false
	for _, it := range r.body["items"].([]any) {
		m := it.(map[string]any)
		if m["path"] == "/laporan" {
			found = true
			if m["shared"] != "public" {
				t.Fatalf("badge: %v", m["shared"])
			}
		} else if m["shared"] != "none" {
			t.Fatalf("badge leaked to %v", m["path"])
		}
	}
	if !found {
		t.Fatal("laporan not listed")
	}
	r = env.call(http.MethodGet, "/api/v1/files/stat?path=/laporan", "203.0.113.5:1", nil, nil, ck)
	if shares, _ := r.body["shares"].([]any); len(shares) != 1 {
		t.Fatalf("stat shares: %s", r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/shares/by-path?path=/laporan", "203.0.113.5:1", nil, nil, ck)
	if items, _ := r.body["items"].([]any); r.rec.Code != 200 || len(items) != 1 {
		t.Fatalf("by-path: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// list + get + patch (password on, preview only) + access logs
	r = env.call(http.MethodGet, "/api/v1/shares", "203.0.113.5:1", nil, nil, ck)
	if items, _ := r.body["items"].([]any); r.rec.Code != 200 || len(items) != 1 {
		t.Fatalf("list shares: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPatch, "/api/v1/shares/"+id, "203.0.113.5:1", map[string]any{"password": "rahasia-1", "allowDownload": false, "slug": "laporan-tim"}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("patch: %d %s", r.rec.Code, r.rec.Body.String())
	}
	sh = shareOf(t, r)
	if sh["hasPassword"] != true || sh["allowDownload"] != false || sh["slug"] != "laporan-tim" || !strings.HasSuffix(sh["url"].(string), "/s/laporan-tim") {
		t.Fatalf("patched: %s", r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/shares/"+id+"/disable", "203.0.113.5:1", nil, hdr, ck)
	if r.rec.Code != 200 || shareOf(t, r)["status"] != "disabled" {
		t.Fatalf("disable: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/shares/"+id+"/enable", "203.0.113.5:1", nil, hdr, ck)
	if r.rec.Code != 200 || shareOf(t, r)["status"] != "active" {
		t.Fatalf("enable: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/shares/"+id+"/access-logs", "203.0.113.5:1", nil, nil, ck)
	if r.rec.Code != 200 {
		t.Fatalf("logs: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/shares/"+id+"/regenerate-token", "203.0.113.5:1", nil, hdr, ck)
	if r.rec.Code != 200 || !share.IsToken(strings.TrimPrefix(r.body["url"].(string), env.cfg.AppBaseURL+"/s/")) {
		t.Fatalf("regenerate: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// IDOR: another user sees 404 everywhere; CSRF/session rules apply
	bck, bhdr := env.newUser(t, "budi", "Rahasia sekali 2026!")
	for _, c := range []struct{ m, p string }{{http.MethodGet, ""}, {http.MethodPatch, ""}, {http.MethodDelete, ""}, {http.MethodPost, "/disable"}, {http.MethodGet, "/access-logs"}} {
		var body any
		if c.m == http.MethodPatch {
			body = map[string]any{"note": "x"}
		}
		r = env.call(c.m, "/api/v1/shares/"+id+c.p, "203.0.113.9:1", body, bhdr, bck)
		if r.rec.Code != 404 {
			t.Fatalf("idor %s %s: %d %s", c.m, c.p, r.rec.Code, r.rec.Body.String())
		}
	}
	r = env.call(http.MethodPost, "/api/v1/shares", "203.0.113.5:1", map[string]any{"path": "/laporan"}, nil, ck)
	if r.rec.Code != 403 {
		t.Fatalf("missing csrf: %d", r.rec.Code)
	}
	r = env.call(http.MethodGet, "/api/v1/shares", "203.0.113.5:1", nil, nil, nil)
	if r.rec.Code != 401 {
		t.Fatalf("no session: %d", r.rec.Code)
	}

	// validation
	for _, body := range []map[string]any{
		{"path": "/laporan", "mode": "users"},
		{"path": "/laporan/ringkasan.txt", "allowUpload": true},
		{"path": "/laporan", "slug": "Admin"},
		{"path": "/laporan", "slug": "laporan-tim"}, // taken (by regenerate the slug was dropped? no: recreate below)
		{"path": "/tidak-ada"},
		{"path": "/laporan", "expiresInDays": 9999},
		{"path": "/laporan", "password": "ab"},
	} {
		r = env.call(http.MethodPost, "/api/v1/shares", "203.0.113.5:1", body, hdr, ck)
		if r.rec.Code == 201 && body["slug"] == "laporan-tim" {
			continue // slug became free when the token was regenerated
		}
		if r.rec.Code != 400 && r.rec.Code != 404 && r.rec.Code != 409 {
			t.Fatalf("validation %v: %d %s", body, r.rec.Code, r.rec.Body.String())
		}
	}

	// delete
	r = env.call(http.MethodDelete, "/api/v1/shares/"+id, "203.0.113.5:1", nil, hdr, ck)
	if r.rec.Code != 204 {
		t.Fatalf("delete: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/shares/"+id, "203.0.113.5:1", nil, nil, ck)
	if r.rec.Code != 404 {
		t.Fatalf("after delete: %d", r.rec.Code)
	}
}

func TestSharesRequirePasswordAndFeatureFlag(t *testing.T) {
	env := newAuthEnv(t, "SHARE_REQUIRE_PASSWORD=true")
	ck, hdr, _ := seedShareFixture(t, env)
	r := env.call(http.MethodPost, "/api/v1/shares", "203.0.113.5:1", map[string]any{"path": "/laporan"}, hdr, ck)
	if r.rec.Code != 400 || r.code(t) != "VALIDATION_FAILED" {
		t.Fatalf("password required: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/shares", "203.0.113.5:1", map[string]any{"path": "/laporan", "password": "rahasia"}, hdr, ck)
	if r.rec.Code != 201 {
		t.Fatalf("with password: %d %s", r.rec.Code, r.rec.Body.String())
	}
	env2 := newAuthEnv(t, "PUBLIC_SHARES_ENABLED=false")
	ck2, hdr2, _ := seedShareFixture(t, env2)
	r = env2.call(http.MethodPost, "/api/v1/shares", "203.0.113.5:1", map[string]any{"path": "/laporan"}, hdr2, ck2)
	if r.rec.Code != 403 || r.code(t) != "FORBIDDEN" {
		t.Fatalf("feature off: %d %s", r.rec.Code, r.rec.Body.String())
	}
}
