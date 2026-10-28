package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/db"
)

// Review F (PROGRESS note Step 20 → R-F): routes classified "self" in the
// IDOR suite are only probed anonymously there. The ones that address rows
// by id in the body (trash restore/purge, notifications read) must ignore
// ids owned by somebody else: no effect, no 2xx that reports success.
func TestReviewFSelfRoutesIgnoreForeignIDs(t *testing.T) {
	env := newAuthEnv(t)
	ip := "203.0.113.21:1"
	ckB, hdrB := env.newUser(t, "budi", readyPassword)
	ckA, hdrA := env.newUser(t, "ani", readyPassword)
	home := filepath.Join(env.cfg.StorageRoot, "budi")
	if err := os.WriteFile(filepath.Join(home, "rahasia.txt"), []byte("budi\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	if r := env.call(http.MethodPost, "/api/v1/files/delete", ip, map[string]any{"paths": []string{"/rahasia.txt"}}, hdrB, ckB); r.rec.Code >= 300 {
		t.Fatalf("delete: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r := env.call(http.MethodGet, "/api/v1/trash", ip, nil, hdrB, ckB)
	items, _ := r.body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("budi trash: %d %s", r.rec.Code, r.rec.Body.String())
	}
	trashID := items[0].(map[string]any)["id"].(string)
	if _, err := env.notify.Create(context.Background(), budiID(t, env, ckB, hdrB), "share_received", "t", "b", "/"); err != nil {
		t.Fatal(err)
	}
	r = env.call(http.MethodGet, "/api/v1/notifications", ip, nil, hdrB, ckB)
	nitems, _ := r.body["items"].([]any)
	if len(nitems) != 1 {
		t.Fatalf("budi notifications: %d %s", r.rec.Code, r.rec.Body.String())
	}
	notifID := nitems[0].(map[string]any)["id"].(string)

	cases := []struct {
		name, path string
		body       map[string]any
	}{
		{"trash restore", "/api/v1/trash/restore", map[string]any{"ids": []string{trashID}}},
		{"trash restore to", "/api/v1/trash/restore", map[string]any{"ids": []string{trashID}, "to": "/"}},
		{"trash purge", "/api/v1/trash/purge", map[string]any{"ids": []string{trashID}}},
		{"notifications read", "/api/v1/notifications/read", map[string]any{"ids": []string{notifID}}},
	}
	for _, c := range cases {
		r := env.call(http.MethodPost, c.path, ip, c.body, hdrA, ckA)
		if r.rec.Code/100 == 2 {
			// a 2xx is only acceptable when it reports that nothing happened
			if n, _ := r.body["restored"].(float64); n > 0 {
				t.Errorf("%s: user A restored B's item: %s", c.name, r.rec.Body.String())
			}
			if n, _ := r.body["purged"].(float64); n > 0 {
				t.Errorf("%s: user A purged B's item: %s", c.name, r.rec.Body.String())
			}
			if n, _ := r.body["marked"].(float64); n > 0 {
				t.Errorf("%s: user A marked B's notification: %s", c.name, r.rec.Body.String())
			}
			if n, _ := r.body["updated"].(float64); n > 0 {
				t.Errorf("%s: user A touched B's rows: %s", c.name, r.rec.Body.String())
			}
		}
		if r.rec.Code >= 500 {
			t.Errorf("%s: %d %s", c.name, r.rec.Code, r.rec.Body.String())
		}
	}
	// B's state is untouched
	r = env.call(http.MethodGet, "/api/v1/trash", ip, nil, hdrB, ckB)
	if items, _ := r.body["items"].([]any); len(items) != 1 {
		t.Fatalf("budi trash changed: %s", r.rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(home, "rahasia.txt")); err == nil {
		t.Fatal("budi's file was restored by ani")
	}
	r = env.call(http.MethodGet, "/api/v1/notifications", ip, nil, hdrB, ckB)
	if nitems, _ := r.body["items"].([]any); len(nitems) != 1 || nitems[0].(map[string]any)["readAt"] != nil {
		t.Fatalf("budi notification changed: %s", r.rec.Body.String())
	}
	// anonymous bodies with real ids → 401
	for _, c := range cases {
		if r := env.call(http.MethodPost, c.path, ip, c.body, nil, nil); r.rec.Code != 401 {
			t.Errorf("%s anonymous: %d", c.name, r.rec.Code)
		}
	}
}

func budiID(t *testing.T, env *authEnv, ck *http.Cookie, hdr map[string]string) string {
	t.Helper()
	r := env.call(http.MethodGet, "/api/v1/auth/me", "203.0.113.21:1", nil, hdr, ck)
	return r.body["user"].(map[string]any)["id"].(string)
}

// Review F: the thumbnail limiter is keyed by the signed scope. An unsigned
// request naming somebody else's scope must not spend that account's
// budget (otherwise anybody who learns a user id can starve their grid).
func TestReviewFThumbLimitKeyedOnVerifiedScope(t *testing.T) {
	env := newAuthEnv(t, "RATE_LIMIT_THUMB=1/1m")
	ckA, hdrA := env.newUser(t, "ani", readyPassword)
	if err := os.WriteFile(filepath.Join(env.cfg.StorageRoot, "ani", "a.png"), []byte("not really png"), 0o640); err != nil {
		t.Fatal(err)
	}
	r := env.call(http.MethodPost, "/api/v1/files/sign", "203.0.113.5:1", map[string]any{"paths": []string{"/a.png"}, "purpose": "thumb", "size": 128}, hdrA, ckA)
	if r.rec.Code != 200 {
		t.Fatalf("sign: %d %s", r.rec.Code, r.rec.Body.String())
	}
	signed := r.body["urls"].([]any)[0].(map[string]any)["url"].(string)
	scope := signed[strings.Index(signed, "scope=")+6:]
	scope = scope[:strings.Index(scope, "&")]
	forged := "/api/v1/files/thumb?scope=" + scope + "&path=/a.png&size=128&purpose=thumb&exp=4102444800&sig=forged"
	// the attacker burns "ani's" budget from another address
	for i := 0; i < 3; i++ {
		if r := env.call(http.MethodGet, forged, "198.51.100.9:1", nil, nil, nil); r.rec.Code/100 == 2 {
			t.Fatalf("forged signature served: %d", r.rec.Code)
		}
	}
	// ani's first real request is still served (not 429)
	if r := env.call(http.MethodGet, signed, "203.0.113.5:1", nil, nil, nil); r.rec.Code == 429 {
		t.Fatalf("ani was rate limited by forged requests naming her scope: %s", r.rec.Body.String())
	}
	// and her own second request within the window is limited
	expectRateLimited(t, env.call(http.MethodGet, signed, "203.0.113.5:1", nil, nil, nil), "thumb")
}

// Review F (review E m-10): admin.browse_user audit rows for writes under
// ?as_user= must name the paths from the JSON body, and the handler must
// still receive the body unchanged.
func TestReviewFAsUserAuditNamesBodyPaths(t *testing.T) {
	env := newAuthEnv(t, "ADMIN_CAN_BROWSE_USERS=true")
	ck, hdr := readyAdmin(t, env)
	ckB, _ := env.newUser(t, "budi", readyPassword)
	_ = ckB
	ip := "203.0.113.31:1"
	if r := env.call(http.MethodPost, "/api/v1/files/mkdir?as_user=budi", ip, map[string]any{"path": "/dari-admin"}, hdr, ck); r.rec.Code != 201 {
		t.Fatalf("mkdir as_user: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := env.call(http.MethodPost, "/api/v1/files/rename?as_user=budi", ip, map[string]any{"path": "/dari-admin", "newName": "diganti"}, hdr, ck); r.rec.Code != 200 {
		t.Fatalf("rename as_user: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := env.call(http.MethodPost, "/api/v1/files/delete?as_user=budi", ip, map[string]any{"paths": []string{"/diganti"}}, hdr, ck); r.rec.Code/100 != 2 {
		t.Fatalf("delete as_user: %d %s", r.rec.Code, r.rec.Body.String())
	}
	rows, err := env.store.ListAudit(context.Background(), db.AuditFilter{Action: db.Ptr(audit.ActionAdminBrowseUser), Limit: 10})
	if err != nil || len(rows) < 3 {
		t.Fatalf("audit rows: %d %v", len(rows), err)
	}
	want := map[string]string{"/api/v1/files/mkdir": "/dari-admin", "/api/v1/files/rename": "/dari-admin", "/api/v1/files/delete": "/diganti"}
	for _, row := range rows {
		var d map[string]any
		if row.DetailsJson == nil {
			continue
		}
		if err := json.Unmarshal([]byte(*row.DetailsJson), &d); err != nil {
			t.Fatal(err)
		}
		ep, _ := d["endpoint"].(string)
		exp, ok := want[ep]
		if !ok {
			continue
		}
		paths, _ := d["paths"].([]any)
		found := false
		for _, p := range paths {
			if p == exp {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: audit paths %v do not name %s (details %s)", ep, paths, exp, *row.DetailsJson)
		}
		delete(want, ep)
	}
	if len(want) > 0 {
		t.Errorf("no audit row for %v", want)
	}
	// a body over the JSON limit still answers 413 after the peek
	big := map[string]any{"path": strings.Repeat("a", int(JSONBodyLimit)+10)}
	if r := env.call(http.MethodPost, "/api/v1/files/mkdir?as_user=budi", ip, big, hdr, ck); r.rec.Code != 413 {
		t.Fatalf("oversized body as_user: %d", r.rec.Code)
	}
}
