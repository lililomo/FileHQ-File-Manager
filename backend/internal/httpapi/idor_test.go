package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// Step 20 IDOR suite (SPEC §10.6 item 6, PROMPT Step 20 task 3): every
// mounted route is classified below; a route that is not classified fails
// the test so new endpoints cannot ship without an authorization probe.
//
//   - public:   reachable without a session (health, version, login, guest
//     share API); probed with a random token → never 2xx.
//   - signed:   authorised by a signed URL only; probed without a signature
//     → 403, never 2xx.
//   - self:     session-scoped resources addressed by path/body (user B's
//     paths live in B's jail, so A cannot name them); probed
//     anonymously → 401.
//   - resource: the URL carries a resource id; probed as user A with user
//     B's real id → 403/404/410, and anonymously → 401.
//   - admin:    admin only; probed as a regular user → 403.
type idorKind string

const (
	idorPublic   idorKind = "public"
	idorSigned   idorKind = "signed"
	idorSelf     idorKind = "self"
	idorResource idorKind = "resource"
	idorAdmin    idorKind = "admin"
)

var idorRoutes = map[string]idorKind{
	"GET /healthz": idorPublic, "GET /readyz": idorPublic, "GET /api/v1/version": idorPublic,
	"GET /api/v1/branding/logo": idorPublic, "POST /api/v1/auth/login": idorPublic,
	"GET /api/v1/public/shares/{token}": idorPublic, "GET /api/v1/public/shares/{token}/content": idorPublic,
	"GET /api/v1/public/shares/{token}/download": idorPublic, "HEAD /api/v1/public/shares/{token}/download": idorPublic,
	"GET /api/v1/public/shares/{token}/list": idorPublic, "GET /api/v1/public/shares/{token}/media-info": idorPublic,
	"GET /api/v1/public/shares/{token}/raw": idorPublic, "HEAD /api/v1/public/shares/{token}/raw": idorPublic,
	"GET /api/v1/public/shares/{token}/stat": idorPublic, "GET /api/v1/public/shares/{token}/subtitle": idorPublic,
	"HEAD /api/v1/public/shares/{token}/subtitle": idorPublic, "GET /api/v1/public/shares/{token}/thumb": idorPublic,
	"GET /api/v1/public/shares/{token}/zip": idorPublic, "HEAD /api/v1/public/shares/{token}/zip": idorPublic,
	"POST /api/v1/public/shares/{token}/zip": idorPublic, "POST /api/v1/public/shares/{token}/unlock": idorPublic,
	"POST /api/v1/public/shares/{token}/upload/precheck": idorPublic, "POST /api/v1/public/shares/{token}/upload/tus": idorPublic,
	"OPTIONS /api/v1/public/shares/{token}/upload/tus": idorPublic, "OPTIONS /api/v1/public/shares/{token}/upload/tus/{id}": idorPublic,
	"HEAD /api/v1/public/shares/{token}/upload/tus/{id}": idorPublic, "PATCH /api/v1/public/shares/{token}/upload/tus/{id}": idorPublic,
	"DELETE /api/v1/public/shares/{token}/upload/tus/{id}": idorPublic,

	"GET /api/v1/files/thumb": idorSigned, "GET /api/v1/files/download": idorSigned, "HEAD /api/v1/files/download": idorSigned,
	"GET /api/v1/files/raw": idorSigned, "HEAD /api/v1/files/raw": idorSigned, "GET /api/v1/files/zip": idorSigned,
	"HEAD /api/v1/files/zip": idorSigned, "GET /api/v1/files/subtitle": idorSigned,
	"GET /api/v1/archives/entry-content": idorSigned, "HEAD /api/v1/archives/entry-content": idorSigned,

	"GET /api/v1/auth/csrf": idorSelf, "GET /api/v1/auth/me": idorSelf, "POST /api/v1/auth/logout": idorSelf,
	"POST /api/v1/auth/change-password": idorSelf, "GET /api/v1/events": idorSelf,
	"GET /api/v1/files/content": idorSelf, "PUT /api/v1/files/content": idorSelf, "GET /api/v1/files/list": idorSelf,
	"GET /api/v1/files/media-info": idorSelf, "GET /api/v1/files/stat": idorSelf, "GET /api/v1/files/templates": idorSelf,
	"GET /api/v1/files/versions": idorSelf, "POST /api/v1/files/batch-rename": idorSelf, "POST /api/v1/files/batch-rename/preview": idorSelf,
	"POST /api/v1/files/checksum": idorSelf, "POST /api/v1/files/checksum/verify": idorSelf, "POST /api/v1/files/chmod": idorSelf,
	"POST /api/v1/files/copy": idorSelf, "POST /api/v1/files/create": idorSelf, "POST /api/v1/files/delete": idorSelf,
	"POST /api/v1/files/delete-permanent": idorSelf, "POST /api/v1/files/dir-size": idorSelf, "POST /api/v1/files/duplicate": idorSelf,
	"POST /api/v1/files/mkdir": idorSelf, "POST /api/v1/files/move": idorSelf, "POST /api/v1/files/rename": idorSelf,
	"POST /api/v1/files/sign": idorSelf, "POST /api/v1/files/symlink": idorSelf, "POST /api/v1/files/undo": idorSelf,
	"POST /api/v1/files/zip": idorSelf, "GET /api/v1/me/activity": idorSelf, "DELETE /api/v1/me/activity": idorSelf,
	"GET /api/v1/me/editor-session": idorSelf, "PUT /api/v1/me/editor-session": idorSelf, "GET /api/v1/me/folder-prefs": idorSelf,
	"PUT /api/v1/me/folder-prefs": idorSelf, "DELETE /api/v1/me/folder-prefs": idorSelf, "GET /api/v1/me/media-position": idorSelf,
	"PUT /api/v1/me/media-position": idorSelf, "GET /api/v1/me/preferences": idorSelf, "PUT /api/v1/me/preferences": idorSelf,
	"GET /api/v1/me/quota": idorSelf, "GET /api/v1/me/recent": idorSelf, "POST /api/v1/me/recent": idorSelf,
	"GET /api/v1/me/sessions": idorSelf, "POST /api/v1/me/sessions/revoke-others": idorSelf,
	"POST /api/v1/me/totp/setup": idorSelf, "POST /api/v1/me/totp/enable": idorSelf, "POST /api/v1/me/totp/disable": idorSelf,
	"POST /api/v1/me/totp/recovery-codes": idorSelf,
	"GET /api/v1/me/storage-analysis":     idorSelf, "POST /api/v1/me/storage-analysis": idorSelf,
	"GET /api/v1/me/duplicates": idorSelf, "POST /api/v1/me/duplicates": idorSelf, "POST /api/v1/me/duplicates/symlink": idorSelf,
	"GET /api/v1/meta/favorites": idorSelf, "PUT /api/v1/meta/favorites": idorSelf, "DELETE /api/v1/meta/favorites": idorSelf,
	"PUT /api/v1/meta/notes": idorSelf, "DELETE /api/v1/meta/notes": idorSelf, "GET /api/v1/meta/pins": idorSelf,
	"PUT /api/v1/meta/pins": idorSelf, "DELETE /api/v1/meta/pins": idorSelf, "GET /api/v1/meta/tagged": idorSelf,
	"PUT /api/v1/meta/tags": idorSelf, "DELETE /api/v1/meta/tags": idorSelf, "GET /api/v1/notifications": idorSelf,
	"POST /api/v1/notifications/read": idorSelf, "GET /api/v1/search/history": idorSelf, "DELETE /api/v1/search/history": idorSelf,
	"GET /api/v1/search/names": idorSelf, "POST /api/v1/search/content": idorSelf, "POST /api/v1/search/replace": idorSelf,
	"POST /api/v1/search/replace/preview": idorSelf, "GET /api/v1/shared-with-me": idorSelf, "GET /api/v1/shares": idorSelf,
	"GET /api/v1/shares/by-path": idorSelf, "GET /api/v1/shares/user-suggest": idorSelf, "GET /api/v1/users/suggest": idorSelf,
	"POST /api/v1/shares": idorSelf, "GET /api/v1/tasks": idorSelf, "GET /api/v1/trash": idorSelf, "POST /api/v1/trash/empty": idorSelf,
	"POST /api/v1/trash/purge": idorSelf, "POST /api/v1/trash/restore": idorSelf, "GET /api/v1/versions": idorSelf,
	"DELETE /api/v1/versions": idorSelf, "GET /api/v1/archives/capabilities": idorSelf, "GET /api/v1/archives/entries": idorSelf,
	"POST /api/v1/archives/add": idorSelf, "POST /api/v1/archives/compress": idorSelf, "POST /api/v1/archives/extract": idorSelf,
	"POST /api/v1/archives/extract-entries": idorSelf, "POST /api/v1/archives/remove-entries": idorSelf,
	"POST /api/v1/archives/remove-entry": idorSelf, "POST /api/v1/archives/sign": idorSelf,
	"POST /api/v1/remote-downloads": idorSelf, "POST /api/v1/remote-downloads/probe": idorSelf,
	"POST /api/v1/upload/precheck": idorSelf, "POST /api/v1/upload/tus": idorSelf, "OPTIONS /api/v1/upload/tus": idorPublic,

	"GET /api/v1/tasks/{id}": idorResource, "GET /api/v1/tasks/{id}/logs": idorResource, "POST /api/v1/tasks/{id}/cancel": idorResource,
	"POST /api/v1/tasks/{id}/retry": idorResource, "DELETE /api/v1/tasks/{id}": idorResource,
	"GET /api/v1/versions/{id}/content": idorResource, "GET /api/v1/versions/{id}/download": idorResource,
	"POST /api/v1/versions/{id}/restore": idorResource, "DELETE /api/v1/versions/{id}": idorResource,
	"GET /api/v1/shares/{id}": idorResource, "GET /api/v1/shares/{id}/access-logs": idorResource, "PATCH /api/v1/shares/{id}": idorResource,
	"DELETE /api/v1/shares/{id}": idorResource, "POST /api/v1/shares/{id}/disable": idorResource, "POST /api/v1/shares/{id}/enable": idorResource,
	"POST /api/v1/shares/{id}/regenerate-token": idorResource, "GET /api/v1/shared-with-me/{id}": idorResource,
	"DELETE /api/v1/shared-with-me/{id}": idorResource, "DELETE /api/v1/notifications/{id}": idorResource,
	"DELETE /api/v1/me/sessions/{id}": idorResource, "HEAD /api/v1/upload/tus/{id}": idorResource,
	"PATCH /api/v1/upload/tus/{id}": idorResource, "DELETE /api/v1/upload/tus/{id}": idorResource, "OPTIONS /api/v1/upload/tus/{id}": idorPublic, // preflight only, no resource access
	"POST /api/v1/upload/share/{shareId}/precheck": idorResource, "POST /api/v1/upload/share/{shareId}/tus": idorResource,
	"OPTIONS /api/v1/upload/share/{shareId}/tus": idorPublic, "HEAD /api/v1/upload/share/{shareId}/tus/{id}": idorResource,
	"PATCH /api/v1/upload/share/{shareId}/tus/{id}": idorResource, "DELETE /api/v1/upload/share/{shareId}/tus/{id}": idorResource,
	"OPTIONS /api/v1/upload/share/{shareId}/tus/{id}": idorPublic,

	"GET /api/v1/admin/audit": idorAdmin, "GET /api/v1/admin/audit/export.csv": idorAdmin, "GET /api/v1/admin/dashboard": idorAdmin,
	"GET /api/v1/admin/maintenance": idorAdmin, "POST /api/v1/admin/maintenance/{job}": idorAdmin,
	"GET /api/v1/admin/maintenance/backups": idorAdmin, "GET /api/v1/admin/maintenance/backups/{name}": idorAdmin,
	"GET /api/v1/admin/settings": idorAdmin, "PUT /api/v1/admin/settings": idorAdmin, "POST /api/v1/admin/settings/reset": idorAdmin,
	"GET /api/v1/admin/shares": idorAdmin, "POST /api/v1/admin/shares/{id}/revoke": idorAdmin, "GET /api/v1/admin/tasks": idorAdmin,
	"POST /api/v1/admin/tasks/{id}/cancel": idorAdmin, "GET /api/v1/admin/users": idorAdmin, "POST /api/v1/admin/users": idorAdmin,
	"GET /api/v1/admin/users/{id}": idorAdmin, "PATCH /api/v1/admin/users/{id}": idorAdmin, "DELETE /api/v1/admin/users/{id}": idorAdmin,
	"POST /api/v1/admin/users/{id}/disable": idorAdmin, "POST /api/v1/admin/users/{id}/enable": idorAdmin,
	"POST /api/v1/admin/users/{id}/reset-password": idorAdmin, "POST /api/v1/admin/users/{id}/revoke-sessions": idorAdmin,
	"POST /api/v1/admin/users/{id}/reset-totp": idorAdmin,
	"POST /api/v1/admin/branding/logo":         idorAdmin, "DELETE /api/v1/admin/branding/logo": idorAdmin,
}

var idorParamRe = regexp.MustCompile(`\{[a-zA-Z]+\}`)

// idorFixture creates user B's resources so A can be pointed at their ids.
type idorFixture struct {
	ids map[string]string // param name → B's id
}

func newIDORFixture(t *testing.T, env *authEnv) idorFixture {
	t.Helper()
	ip := "203.0.113.8:1"
	ckB, hdrB := env.newUser(t, "budi", readyPassword)
	ckC, _ := env.newUser(t, "citra", readyPassword)
	home := filepath.Join(env.cfg.StorageRoot, "budi")
	if err := os.MkdirAll(filepath.Join(home, "dir"), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"b.txt", "dir/x.txt", "gone.txt"} {
		if err := os.WriteFile(filepath.Join(home, f), []byte("budi\n"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	ids := map[string]string{"token": "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "name": "app-20260101-000000.db", "job": "backup_db"}

	// trash + version
	if r := env.call(http.MethodPost, "/api/v1/files/delete", ip, map[string]any{"paths": []string{"/gone.txt"}}, hdrB, ckB); r.rec.Code >= 300 {
		t.Fatalf("delete: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r := env.call(http.MethodGet, "/api/v1/files/content?path=/b.txt", ip, nil, hdrB, ckB)
	if r.rec.Code != 200 {
		t.Fatalf("content: %d %s", r.rec.Code, r.rec.Body.String())
	}
	h := map[string]string{"X-CSRF-Token": hdrB["X-CSRF-Token"], "If-Match": r.rec.Header().Get("ETag")}
	if r = env.call(http.MethodPut, "/api/v1/files/content", ip, map[string]any{"path": "/b.txt", "content": "v2\n", "eol": "keep"}, h, ckB); r.rec.Code >= 300 {
		t.Fatalf("put content: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/versions?path=/b.txt", ip, nil, hdrB, ckB)
	if items, _ := r.body["items"].([]any); len(items) > 0 {
		ids["id:version"] = items[0].(map[string]any)["id"].(string)
	} else {
		t.Fatalf("no version: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// task
	r = env.call(http.MethodPost, "/api/v1/archives/compress", ip, map[string]any{"paths": []string{"/dir"}, "format": "zip"}, hdrB, ckB)
	if r.rec.Code != 202 {
		t.Fatalf("compress: %d %s", r.rec.Code, r.rec.Body.String())
	}
	ids["id:task"] = r.body["task"].(map[string]any)["id"].(string)
	// public share + users share (citra is the recipient, so she gets a notification)
	ids["id:share"], ids["token:real"] = createShare(t, env, ckB, hdrB, map[string]any{"path": "/b.txt"})
	r = env.call(http.MethodPost, "/api/v1/shares", ip, map[string]any{"path": "/dir", "mode": "users", "recipients": []map[string]any{{"username": "citra", "permission": "editor"}}}, hdrB, ckB)
	if r.rec.Code != 201 {
		t.Fatalf("users share: %d %s", r.rec.Code, r.rec.Body.String())
	}
	ids["id:ushare"] = shareOf(t, r)["id"].(string)
	r = env.call(http.MethodGet, "/api/v1/notifications", ip, nil, nil, ckC)
	if items, _ := r.body["items"].([]any); len(items) > 0 {
		ids["id:notification"] = items[0].(map[string]any)["id"].(string)
	} else {
		t.Fatalf("citra has no notification: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// session + upload
	r = env.call(http.MethodGet, "/api/v1/me/sessions", ip, nil, hdrB, ckB)
	if items, _ := r.body["items"].([]any); len(items) > 0 {
		ids["id:session"] = items[0].(map[string]any)["id"].(string)
	} else {
		t.Fatalf("budi has no session: %d %s", r.rec.Code, r.rec.Body.String())
	}
	up := env.tusCall(http.MethodPost, "/api/v1/upload/tus/", nil, map[string]string{"X-CSRF-Token": hdrB["X-CSRF-Token"], "Upload-Length": "4", "Upload-Metadata": tusMeta(map[string]string{"filename": "u.txt", "path": "/"})}, ckB)
	if up.rec.Code != 201 {
		t.Fatalf("tus create: %d %s", up.rec.Code, up.rec.Body.String())
	}
	loc := up.rec.Header().Get("Location")
	ids["id:upload"] = loc[strings.LastIndex(loc, "/")+1:]
	r = env.call(http.MethodGet, "/api/v1/auth/me", ip, nil, hdrB, ckB)
	ids["id:user"] = r.body["user"].(map[string]any)["id"].(string)
	return idorFixture{ids: ids}
}

// target substitutes B's ids into a route pattern.
func (f idorFixture) target(route string) string {
	path := route
	switch {
	case strings.Contains(route, "/tasks/"):
		path = strings.Replace(path, "{id}", f.ids["id:task"], 1)
	case strings.Contains(route, "/versions/"):
		path = strings.Replace(path, "{id}", f.ids["id:version"], 1)
	case strings.Contains(route, "/shared-with-me/"):
		path = strings.Replace(path, "{id}", f.ids["id:ushare"], 1)
	case strings.Contains(route, "/shares/"):
		path = strings.Replace(path, "{id}", f.ids["id:share"], 1)
	case strings.Contains(route, "/notifications/"):
		path = strings.Replace(path, "{id}", f.ids["id:notification"], 1)
	case strings.Contains(route, "/sessions/"):
		path = strings.Replace(path, "{id}", f.ids["id:session"], 1)
	case strings.Contains(route, "/tus/"):
		path = strings.Replace(path, "{id}", f.ids["id:upload"], 1)
	case strings.Contains(route, "/users/"):
		path = strings.Replace(path, "{id}", f.ids["id:user"], 1)
	}
	path = strings.NewReplacer("{shareId}", f.ids["id:ushare"], "{token}", f.ids["token"], "{name}", f.ids["name"], "{job}", f.ids["job"], "{id}", "0123456789abcdef").Replace(path)
	return path
}

func TestIDORSuite(t *testing.T) {
	env := newAuthEnv(t)
	routes := chiRoutes(t, env.handler)
	fx := newIDORFixture(t, env)
	ckA, hdrA := env.newUser(t, "ani", readyPassword) // regular user, not a recipient, not an admin
	ipA := "203.0.113.11:1"

	var uncovered []string
	seen := map[string]bool{}
	for _, route := range routes {
		kind, ok := idorRoutes[route]
		if !ok {
			uncovered = append(uncovered, route)
			continue
		}
		seen[route] = true
		method, pattern, _ := strings.Cut(route, " ")
		target := fx.target(pattern)
		if idorParamRe.MatchString(target) {
			t.Errorf("%s: unresolved parameter in %s", route, target)
			continue
		}
		var body any
		if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch {
			body = map[string]any{}
		}
		// anonymous
		anon := env.call(method, target, "203.0.113.12:1", body, nil, nil)
		switch kind {
		case idorPublic:
			if strings.Contains(pattern, "{token}") && method != http.MethodOptions && anon.rec.Code/100 == 2 {
				t.Errorf("%s: random token answered %d", route, anon.rec.Code)
			}
		case idorSigned:
			if anon.rec.Code/100 == 2 || (method == http.MethodGet && anon.rec.Code != 403) {
				t.Errorf("%s: unsigned request answered %d (want 403)", route, anon.rec.Code)
			}
		default:
			if anon.rec.Code/100 == 2 || (method == http.MethodGet && anon.rec.Code != 401) {
				t.Errorf("%s: anonymous answered %d (want 401)", route, anon.rec.Code)
			}
		}
		if kind == idorPublic || kind == idorSigned || kind == idorSelf {
			continue
		}
		// user A against B's resource / admin routes
		r := env.call(method, target, ipA, body, hdrA, ckA)
		switch kind {
		case idorAdmin:
			if r.rec.Code != 403 {
				t.Errorf("%s: regular user answered %d (want 403): %s", route, r.rec.Code, r.rec.Body.String())
			}
		case idorResource:
			if r.rec.Code/100 == 2 {
				t.Errorf("%s: user A reached B's resource: %d %s", route, r.rec.Code, r.rec.Body.String())
			} else if method == http.MethodGet && r.rec.Code != 403 && r.rec.Code != 404 && r.rec.Code != 410 {
				t.Errorf("%s: user A got %d (want 403/404/410): %s", route, r.rec.Code, r.rec.Body.String())
			}
		}
	}
	if r := env.call(http.MethodGet, PublicSharePrefix+fx.ids["token:real"], "203.0.113.12:1", nil, nil, nil); r.rec.Code != 200 {
		t.Errorf("sanity: real public token answered %d", r.rec.Code)
	}
	if len(uncovered) > 0 {
		sort.Strings(uncovered)
		t.Fatalf("routes without IDOR classification (add them to idorRoutes):\n%s", strings.Join(uncovered, "\n"))
	}
	for route := range idorRoutes {
		if !seen[route] {
			t.Errorf("idorRoutes lists %s but the router does not mount it", route)
		}
	}
}
