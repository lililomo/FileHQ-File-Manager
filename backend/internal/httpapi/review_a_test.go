package httpapi

import (
	"bufio"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Review A (independent review of Steps 1–5): attack probes and regression
// tests for the findings in docs/reviews/review-A.md.

const budiPassword = "Password budi yang panjang 2026"

// TestReviewAPathAttacksAndIsolation probes GET /files/list|stat and POST
// /files/mkdir|create with traversal, encoding and name tricks from user B
// and checks that nothing of user A (or of the host) is reachable.
func TestReviewAPathAttacksAndIsolation(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	ip := "203.0.113.5:1"
	adminHome := filepath.Join(env.cfg.StorageRoot, "admin")
	seedFiles(t, adminHome, "secret-admin.txt", "shared-name/", "é.txt")
	bck, bhdr := env.newUser(t, "budi", budiPassword)
	budiHome := filepath.Join(env.cfg.StorageRoot, "budi")
	seedFiles(t, budiHome, "shared-name/", "shared-name/budi-only.txt")

	type probe struct {
		raw    string
		status int
		code   string
		reason string
	}
	probes := []probe{
		{"/../admin", 400, "PATH_INVALID", "parent_segment"},
		{"..", 400, "PATH_INVALID", "parent_segment"},
		{"/shared-name/../../admin", 400, "PATH_INVALID", "parent_segment"},
		{"%2e%2e/admin", 400, "PATH_INVALID", "parent_segment"},
		{"%2e%2e%2fadmin%2fsecret-admin.txt", 400, "PATH_INVALID", "parent_segment"},
		{"/%c0%ae%c0%ae/admin", 400, "PATH_INVALID", "invalid_utf8"}, // overlong UTF-8 ".."
		{"/%00", 400, "PATH_INVALID", "control_char"},
		{"/a%00b", 400, "PATH_INVALID", "control_char"},
		{"/x%7f", 400, "PATH_INVALID", "control_char"},
		{"/./admin", 400, "PATH_INVALID", "dot_segment"},
		{"/.system", 404, "NOT_FOUND", ""},
		{"/.system/db/app.db", 404, "NOT_FOUND", ""},
		{"/admin/secret-admin.txt", 404, "NOT_FOUND", ""}, // "absolute" host-looking path is virtual
		{"/etc/passwd", 404, "NOT_FOUND", ""},
		{"/..%5cadmin", 404, "NOT_FOUND", ""}, // "..\admin" is one (missing) segment on Linux
		{"/" + strings.Repeat("a", 256), 400, "PATH_INVALID", "segment_too_long"},
		{"/" + strings.Repeat("a/", 65), 400, "PATH_INVALID", "too_deep"},
	}
	for _, ep := range []string{"/api/v1/files/list?path=", "/api/v1/files/stat?path="} {
		for _, p := range probes {
			r := env.call(http.MethodGet, ep+p.raw, ip, nil, nil, bck)
			if r.rec.Code != p.status || r.code(t) != p.code {
				t.Fatalf("%s%s: got %d %s want %d %s (%s)", ep, p.raw, r.rec.Code, r.code(t), p.status, p.code, r.rec.Body.String())
			}
			if p.reason != "" {
				details, _ := r.body["error"].(map[string]any)["details"].(map[string]any)
				if details["reason"] != p.reason {
					t.Fatalf("%s%s: reason %v want %s", ep, p.raw, details["reason"], p.reason)
				}
			}
			if strings.Contains(r.rec.Body.String(), env.cfg.StorageRoot) {
				t.Fatalf("%s%s leaks the storage root: %s", ep, p.raw, r.rec.Body.String())
			}
		}
	}

	// NFD input resolves to the NFC entry; names in responses are NFC.
	nfd := "e\u0301.txt"
	r := env.call(http.MethodGet, "/api/v1/files/stat?path=/"+nfd, ip, nil, nil, ck)
	if r.rec.Code != 200 || r.body["name"] != "é.txt" || r.body["path"] != "/é.txt" {
		t.Fatalf("NFD stat: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/files/mkdir", ip, map[string]any{"path": "/", "name": "cafe\u0301"}, hdr, ck)
	if r.rec.Code != 201 {
		t.Fatalf("NFD mkdir: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if _, err := os.Lstat(filepath.Join(adminHome, "café")); err != nil {
		t.Fatalf("NFD mkdir must create the NFC name on disk: %v", err)
	}

	// mkdir/create: traversal in name or path never leaves the user root.
	type body = map[string]any
	for _, ep := range []string{"/api/v1/files/mkdir", "/api/v1/files/create"} {
		cases := []struct {
			req    body
			status int
			code   string
		}{
			{body{"path": "/", "name": "../x"}, 400, "NAME_INVALID"},
			{body{"path": "/", "name": "..\\x"}, 400, "NAME_INVALID"},
			{body{"path": "/", "name": "a/b"}, 400, "NAME_INVALID"},
			{body{"path": "/", "name": "a\x00b"}, 400, "NAME_INVALID"},
			{body{"path": "/", "name": ".system"}, 400, "NAME_INVALID"},
			{body{"path": "/", "name": "."}, 400, "NAME_INVALID"},
			{body{"path": "/", "name": strings.Repeat("x", 256)}, 400, "NAME_INVALID"},
			{body{"path": "/../admin/x"}, 400, "PATH_INVALID"},
			{body{"path": "/../admin", "name": "x"}, 400, "PATH_INVALID"},
			{body{"path": "/.system"}, 404, "NOT_FOUND"},
			{body{"path": "/.system", "name": "x"}, 404, "NOT_FOUND"},
			{body{"path": "/"}, 400, "VALIDATION_FAILED"},
			{body{"path": "/nope/x"}, 404, "NOT_FOUND"},
			// Finding M-03: the full-path form must apply the name rules too.
			{body{"path": "/a\\b"}, 400, "NAME_INVALID"},
			{body{"path": "/ok/a\\b"}, 400, "NAME_INVALID"},
		}
		for _, c := range cases {
			r := env.call(http.MethodPost, ep, ip, c.req, bhdr, bck)
			if r.rec.Code != c.status || r.code(t) != c.code {
				t.Fatalf("%s %v: got %d %s want %d %s (%s)", ep, c.req, r.rec.Code, r.code(t), c.status, c.code, r.rec.Body.String())
			}
		}
	}
	if entries, _ := os.ReadDir(budiHome); len(entries) != 1 || entries[0].Name() != "shared-name" {
		t.Fatalf("budi home polluted: %v", entries)
	}
	if _, err := os.Lstat(filepath.Join(adminHome, "x")); err == nil {
		t.Fatal("file created in admin home from budi's session")
	}

	// Same folder name in two homes: the listing cache is keyed per user.
	for i := 0; i < 2; i++ {
		r = env.call(http.MethodGet, "/api/v1/files/list?path=/shared-name", ip, nil, nil, ck)
		if r.rec.Code != 200 || r.body["total"] != float64(0) {
			t.Fatalf("admin shared-name: %d %s", r.rec.Code, r.rec.Body.String())
		}
		r = env.call(http.MethodGet, "/api/v1/files/list?path=/shared-name", ip, nil, nil, bck)
		if r.rec.Code != 200 || itemNames(t, r) != "budi-only.txt" {
			t.Fatalf("budi shared-name: %d %s", r.rec.Code, r.rec.Body.String())
		}
	}
	// budi creates inside his own shared-name; admin's folder stays empty.
	r = env.call(http.MethodPost, "/api/v1/files/create", ip, body{"path": "/shared-name/new.txt"}, bhdr, bck)
	if r.rec.Code != 201 {
		t.Fatalf("budi create: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if _, err := os.Lstat(filepath.Join(adminHome, "shared-name", "new.txt")); err == nil {
		t.Fatal("budi's file landed in admin's folder")
	}
	r = env.call(http.MethodGet, "/api/v1/files/list?path=/shared-name", ip, nil, nil, ck)
	if r.body["total"] != float64(0) {
		t.Fatalf("admin sees budi's file after create: %s", r.rec.Body.String())
	}
}

// TestReviewACursorTamper covers forged, foreign and re-targeted cursors.
func TestReviewACursorTamper(t *testing.T) {
	env := newAuthEnv(t)
	ck, _ := readyAdmin(t, env)
	ip := "203.0.113.5:1"
	seedFiles(t, filepath.Join(env.cfg.StorageRoot, "admin"), "a1", "a2", "a3")
	bck, _ := env.newUser(t, "budi", budiPassword)
	seedFiles(t, filepath.Join(env.cfg.StorageRoot, "budi"), "b1", "b2", "b3")

	r := env.call(http.MethodGet, "/api/v1/files/list?path=/&limit=1", ip, nil, nil, ck)
	cur, _ := r.body["nextCursor"].(string)
	if r.rec.Code != 200 || cur == "" {
		t.Fatalf("first page: %d %s", r.rec.Code, r.rec.Body.String())
	}
	bad := func(c string) {
		t.Helper()
		r := env.call(http.MethodGet, "/api/v1/files/list?path=/&limit=1&cursor="+c, ip, nil, nil, ck)
		if r.rec.Code != 400 || r.code(t) != "VALIDATION_FAILED" || errorField(r, "cursor") != "invalid" {
			t.Fatalf("cursor %q accepted: %d %s", c, r.rec.Code, r.rec.Body.String())
		}
	}
	flipped := []byte(cur)
	if flipped[2] == 'A' {
		flipped[2] = 'B'
	} else {
		flipped[2] = 'A'
	}
	bad(string(flipped))
	bad(cur[:len(cur)-2])
	bad("eyJ2IjoxfQ")
	bad("%20")
	// A cursor made for name:asc is refused for another sort spec.
	r = env.call(http.MethodGet, "/api/v1/files/list?path=/&limit=1&sort=size&cursor="+cur, ip, nil, nil, ck)
	if r.rec.Code != 400 {
		t.Fatalf("cursor for other sort accepted: %d", r.rec.Code)
	}
	// Admin's cursor used by budi is just a position: only budi's items come back.
	r = env.call(http.MethodGet, "/api/v1/files/list?path=/&limit=10&cursor="+cur, ip, nil, nil, bck)
	if r.rec.Code != 200 {
		t.Fatalf("foreign cursor: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if names := itemNames(t, r); strings.Contains(names, "a") {
		t.Fatalf("foreign cursor leaked admin items: %s", names)
	}
}

// TestReviewAMustChangePasswordGate enumerates every mounted route and
// checks the S+P gate (SPEC §10.6 item 9).
func TestReviewAMustChangePasswordGate(t *testing.T) {
	env := newAuthEnv(t)
	ip := "203.0.113.9:1"
	ck, hdr := env.newUserWith(t, "sari", "Password sari yang panjang 2026", true)
	blocked := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/me/preferences"},
		{http.MethodPut, "/api/v1/me/preferences"},
		{http.MethodGet, "/api/v1/files/list?path=/"},
		{http.MethodGet, "/api/v1/files/stat?path=/"},
		{http.MethodGet, "/api/v1/files/media-info?path=/x.jpg"},
		{http.MethodGet, "/api/v1/files/templates"},
		{http.MethodPost, "/api/v1/files/mkdir"},
		{http.MethodPost, "/api/v1/files/create"},
		{http.MethodGet, "/api/v1/me/folder-prefs?path=/"},
		{http.MethodPut, "/api/v1/me/folder-prefs"},
		{http.MethodDelete, "/api/v1/me/folder-prefs?path=/"},
		{http.MethodPost, "/api/v1/files/sign"},
		// Step 9a file operations
		{http.MethodPost, "/api/v1/files/rename"},
		{http.MethodPost, "/api/v1/files/copy"},
		{http.MethodPost, "/api/v1/files/move"},
		{http.MethodPost, "/api/v1/files/duplicate"},
		{http.MethodPost, "/api/v1/files/symlink"},
		{http.MethodPost, "/api/v1/files/chmod"},
		{http.MethodPost, "/api/v1/files/delete"},
		{http.MethodPost, "/api/v1/files/batch-rename/preview"},
		{http.MethodPost, "/api/v1/files/batch-rename"},
		{http.MethodPost, "/api/v1/files/undo"},
		// Step 10a
		{http.MethodGet, "/api/v1/me/quota"},
		{http.MethodPost, "/api/v1/files/delete-permanent"},
		{http.MethodPost, "/api/v1/files/dir-size"},
		{http.MethodPost, "/api/v1/files/checksum"},
		{http.MethodPost, "/api/v1/files/checksum/verify"},
		{http.MethodGet, "/api/v1/trash"},
		{http.MethodPost, "/api/v1/trash/restore"},
		{http.MethodPost, "/api/v1/trash/purge"},
		{http.MethodPost, "/api/v1/trash/empty"},
		// Step 10b
		{http.MethodGet, "/api/v1/meta/favorites"},
		{http.MethodPut, "/api/v1/meta/favorites"},
		{http.MethodDelete, "/api/v1/meta/favorites"},
		{http.MethodGet, "/api/v1/meta/pins"},
		{http.MethodPut, "/api/v1/meta/pins"},
		{http.MethodDelete, "/api/v1/meta/pins"},
		{http.MethodPut, "/api/v1/meta/notes"},
		{http.MethodDelete, "/api/v1/meta/notes"},
		{http.MethodPut, "/api/v1/meta/tags"},
		{http.MethodDelete, "/api/v1/meta/tags"},
		{http.MethodGet, "/api/v1/meta/tagged"},
		{http.MethodGet, "/api/v1/me/recent"},
		{http.MethodPost, "/api/v1/me/recent"},
		{http.MethodGet, "/api/v1/me/activity"},
		{http.MethodDelete, "/api/v1/me/activity"},
		{http.MethodGet, "/api/v1/me/storage-analysis"},
		{http.MethodPost, "/api/v1/me/storage-analysis"},
		// Step 11
		{http.MethodGet, "/api/v1/tasks"},
		{http.MethodGet, "/api/v1/tasks/x"},
		{http.MethodDelete, "/api/v1/tasks/x"},
		{http.MethodGet, "/api/v1/tasks/x/logs"},
		{http.MethodPost, "/api/v1/tasks/x/cancel"},
		{http.MethodPost, "/api/v1/tasks/x/retry"},
		{http.MethodGet, "/api/v1/events"},
		{http.MethodGet, "/api/v1/notifications"},
		{http.MethodPost, "/api/v1/notifications/read"},
		{http.MethodDelete, "/api/v1/notifications/x"},
		// Step 12a: precheck and every tus verb (OPTIONS/HEAD included: the
		// gate runs before tusd sees the request)
		{http.MethodPost, "/api/v1/upload/precheck"},
		{http.MethodPost, "/api/v1/upload/tus/"},
		{http.MethodOptions, "/api/v1/upload/tus/"},
		{http.MethodHead, "/api/v1/upload/tus/x"},
		{http.MethodPatch, "/api/v1/upload/tus/x"},
		{http.MethodDelete, "/api/v1/upload/tus/x"},
		{http.MethodOptions, "/api/v1/upload/tus/x"},
		// Step 12b: zip preparation is gated; the signed GET routes are exempt (see known)
		{http.MethodPost, "/api/v1/files/zip"},
		// Step 13
		{http.MethodPost, "/api/v1/remote-downloads"},
		{http.MethodPost, "/api/v1/remote-downloads/probe"},
		// Step 14a
		{http.MethodGet, "/api/v1/archives/capabilities"},
		{http.MethodPost, "/api/v1/archives/compress"},
		{http.MethodPost, "/api/v1/archives/extract"},
		// Step 14b: archive browser (entry-content is signed-only, see known)
		{http.MethodGet, "/api/v1/archives/entries?path=/x"},
		{http.MethodPost, "/api/v1/archives/sign"},
		{http.MethodPost, "/api/v1/archives/extract-entries"},
		{http.MethodPost, "/api/v1/archives/add"},
		{http.MethodPost, "/api/v1/archives/remove-entries"},
		{http.MethodPost, "/api/v1/archives/remove-entry"},
	}
	for _, b := range blocked {
		var body any
		if b.method != http.MethodGet {
			body = map[string]any{"path": "/x", "prefs": map[string]any{}}
		}
		r := env.call(b.method, b.path, ip, body, hdr, ck)
		if r.rec.Code != 403 || r.code(t) != "PASSWORD_CHANGE_REQUIRED" {
			t.Fatalf("%s %s: %d %s", b.method, b.path, r.rec.Code, r.rec.Body.String())
		}
	}
	if r := env.call(http.MethodGet, "/api/v1/auth/me", ip, nil, nil, ck); r.rec.Code != 200 {
		t.Fatalf("auth/me: %d", r.rec.Code)
	}
	if r := env.call(http.MethodGet, "/api/v1/auth/csrf", ip, nil, nil, ck); r.rec.Code != 200 {
		t.Fatalf("auth/csrf: %d", r.rec.Code)
	}
	r := env.call(http.MethodPost, "/api/v1/auth/change-password", ip,
		map[string]string{"currentPassword": "wrong", "newPassword": "Password sari yang baru 2026"}, hdr, ck)
	if r.rec.Code != 400 || r.code(t) != "VALIDATION_FAILED" {
		t.Fatalf("change-password reachable: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := env.call(http.MethodPost, "/api/v1/auth/logout", ip, nil, hdr, ck); r.rec.Code != 204 {
		t.Fatalf("logout: %d", r.rec.Code)
	}
	// Every mounted route is either whitelisted or gated: fail if a new route
	// is mounted without being covered by this test.
	known := map[string]bool{
		"GET /healthz": true, "GET /readyz": true, "GET /api/v1/version": true,
		"POST /api/v1/auth/login": true, "POST /api/v1/auth/logout": true, "GET /api/v1/auth/csrf": true,
		"GET /api/v1/auth/me": true, "POST /api/v1/auth/change-password": true,
		"GET /api/v1/me/preferences": true, "PUT /api/v1/me/preferences": true,
		"GET /api/v1/me/folder-prefs": true, "PUT /api/v1/me/folder-prefs": true, "DELETE /api/v1/me/folder-prefs": true,
		"GET /api/v1/files/list": true, "GET /api/v1/files/stat": true, "GET /api/v1/files/templates": true,
		"GET /api/v1/files/media-info": true,
		"POST /api/v1/files/mkdir":     true, "POST /api/v1/files/create": true,
		"POST /api/v1/files/sign":   true,
		"POST /api/v1/files/rename": true, "POST /api/v1/files/copy": true, "POST /api/v1/files/move": true,
		"POST /api/v1/files/duplicate": true, "POST /api/v1/files/symlink": true, "POST /api/v1/files/chmod": true,
		"POST /api/v1/files/delete": true, "POST /api/v1/files/batch-rename/preview": true,
		"POST /api/v1/files/batch-rename": true, "POST /api/v1/files/undo": true,
		"GET /api/v1/me/quota": true, "POST /api/v1/files/delete-permanent": true, "POST /api/v1/files/dir-size": true,
		"POST /api/v1/files/checksum": true, "POST /api/v1/files/checksum/verify": true,
		"GET /api/v1/trash": true, "POST /api/v1/trash/restore": true, "POST /api/v1/trash/purge": true, "POST /api/v1/trash/empty": true,
		"GET /api/v1/meta/favorites": true, "PUT /api/v1/meta/favorites": true, "DELETE /api/v1/meta/favorites": true,
		"GET /api/v1/meta/pins": true, "PUT /api/v1/meta/pins": true, "DELETE /api/v1/meta/pins": true,
		"PUT /api/v1/meta/notes": true, "DELETE /api/v1/meta/notes": true,
		"PUT /api/v1/meta/tags": true, "DELETE /api/v1/meta/tags": true, "GET /api/v1/meta/tagged": true,
		"GET /api/v1/me/recent": true, "POST /api/v1/me/recent": true,
		"GET /api/v1/me/activity": true, "DELETE /api/v1/me/activity": true,
		"GET /api/v1/me/storage-analysis": true, "POST /api/v1/me/storage-analysis": true,
		// Step 11: tasks, SSE, notifications (all gated above)
		"GET /api/v1/tasks": true, "GET /api/v1/tasks/{id}": true, "DELETE /api/v1/tasks/{id}": true,
		"GET /api/v1/tasks/{id}/logs": true, "POST /api/v1/tasks/{id}/cancel": true, "POST /api/v1/tasks/{id}/retry": true,
		"GET /api/v1/events": true, "GET /api/v1/notifications": true, "POST /api/v1/notifications/read": true,
		"DELETE /api/v1/notifications/{id}": true,
		// Step 12a: uploads (gated above)
		"POST /api/v1/upload/precheck": true, "POST /api/v1/upload/tus": true, "OPTIONS /api/v1/upload/tus": true,
		"HEAD /api/v1/upload/tus/{id}": true, "PATCH /api/v1/upload/tus/{id}": true,
		"DELETE /api/v1/upload/tus/{id}": true, "OPTIONS /api/v1/upload/tus/{id}": true,
		// signed URL only (no cookie): a user who must change the password cannot
		// obtain one because /files/sign and POST /files/zip are gated (Steps 8, 12b).
		"GET /api/v1/files/thumb":    true,
		"GET /api/v1/files/download": true, "HEAD /api/v1/files/download": true,
		"GET /api/v1/files/raw": true, "HEAD /api/v1/files/raw": true,
		"GET /api/v1/files/zip": true, "HEAD /api/v1/files/zip": true,
		"POST /api/v1/files/zip":        true,
		"POST /api/v1/remote-downloads": true, "POST /api/v1/remote-downloads/probe": true,
		// Step 14a: archives (gated above)
		"GET /api/v1/archives/capabilities": true, "POST /api/v1/archives/compress": true, "POST /api/v1/archives/extract": true,
		// Step 14b: archive browser (gated above); entry-content is signed-only and
		// its URLs come from the gated POST /archives/sign
		"GET /api/v1/archives/entries": true, "POST /api/v1/archives/sign": true,
		"GET /api/v1/archives/entry-content": true, "HEAD /api/v1/archives/entry-content": true,
		"POST /api/v1/archives/extract-entries": true, "POST /api/v1/archives/add": true,
		"POST /api/v1/archives/remove-entries": true, "POST /api/v1/archives/remove-entry": true,
		// Step 15: search + versions (gated above)
		"GET /api/v1/search/names": true, "POST /api/v1/search/content": true,
		"POST /api/v1/search/replace/preview": true, "POST /api/v1/search/replace": true,
		"GET /api/v1/search/history": true, "DELETE /api/v1/search/history": true,
		"GET /api/v1/versions": true, "POST /api/v1/versions/{id}/restore": true, "DELETE /api/v1/versions/{id}": true,
		// Step 16b: versions completed (gated above)
		"GET /api/v1/files/versions": true, "DELETE /api/v1/versions": true,
		"GET /api/v1/versions/{id}/content": true, "GET /api/v1/versions/{id}/download": true,
		// Step 16a: editor (gated above)
		"GET /api/v1/files/content": true, "PUT /api/v1/files/content": true,
		"GET /api/v1/me/editor-session": true, "PUT /api/v1/me/editor-session": true,
		// Step 17b: playback positions (gated above); subtitles are signed-only
		// and their URLs come from the gated GET /files/media-info
		"GET /api/v1/me/media-position": true, "PUT /api/v1/me/media-position": true,
		"GET /api/v1/files/subtitle": true,
	}
	// the signed endpoints reject cookie-only access: no signature → 403
	for _, p := range []string{"/api/v1/files/thumb?path=/x&size=128", "/api/v1/files/download?path=/x", "/api/v1/files/raw?path=/x&purpose=media", "/api/v1/files/zip?token=x", "/api/v1/archives/entry-content?path=/x&inner=a", "/api/v1/files/subtitle?path=/x.srt&purpose=media"} {
		if r := env.call(http.MethodGet, p, ip, nil, nil, ck); r.rec.Code != 403 || r.code(t) != "SIGNATURE_INVALID" {
			t.Fatalf("%s without signature: %d %s", p, r.rec.Code, r.rec.Body.String())
		}
	}
	for _, rt := range chiRoutes(t, env.handler) {
		if !known[rt] {
			t.Fatalf("route %s is not covered by the password-change gate test", rt)
		}
	}
}

// TestReviewALockoutUsernameVariants: case, whitespace and NBSP variations
// of the username all count against the same account, and the lock holds
// from another IP.
func TestReviewALockoutUsernameVariants(t *testing.T) {
	env := newAuthEnv(t)
	variants := []string{"ADMIN", " admin", "admin ", "Admin\t", " aDmIn"}
	for i, v := range variants {
		r, _, _ := env.login(t, v, "wrong-password-"+v, fmt.Sprintf("198.51.100.%d:1", 10+i))
		if r.rec.Code != 401 {
			t.Fatalf("variant %q: %d %s", v, r.rec.Code, r.rec.Body.String())
		}
	}
	r, _, _ := env.login(t, "admin", initialAdminPassword, "198.51.100.99:1")
	if r.rec.Code != 423 || r.code(t) != "ACCOUNT_LOCKED" {
		t.Fatalf("lock not reached through username variants: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// Variants of the locked name are locked as well.
	r, _, _ = env.login(t, " ADMIN ", initialAdminPassword, "198.51.100.98:1")
	if r.rec.Code != 423 {
		t.Fatalf("variant bypasses lock: %d", r.rec.Code)
	}
}

// TestReviewACSRFVariants: token of another session, Origin null/scheme
// mismatch, cross-site fetch metadata and a logged-out cookie.
func TestReviewACSRFVariants(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	ip := "203.0.113.5:1"
	_, bhdr := env.newUser(t, "budi", budiPassword)
	body := map[string]any{"path": "/csrf-probe"}

	cases := []struct {
		name string
		hdr  map[string]string
		code string
	}{
		{"foreign token", map[string]string{"X-CSRF-Token": bhdr["X-CSRF-Token"]}, "CSRF_INVALID"},
		{"garbage token", map[string]string{"X-CSRF-Token": "!!"}, "CSRF_INVALID"},
		{"origin null", map[string]string{"X-CSRF-Token": hdr["X-CSRF-Token"], "Origin": "null"}, "CSRF_INVALID"},
		{"origin scheme", map[string]string{"X-CSRF-Token": hdr["X-CSRF-Token"], "Origin": "http://file.example.test"}, "CSRF_INVALID"},
		{"origin subdomain", map[string]string{"X-CSRF-Token": hdr["X-CSRF-Token"], "Origin": "https://evil.file.example.test", "Sec-Fetch-Site": "same-site"}, "CSRF_INVALID"},
		{"cross-site", map[string]string{"X-CSRF-Token": hdr["X-CSRF-Token"], "Sec-Fetch-Site": "cross-site"}, "CSRF_INVALID"},
	}
	for _, c := range cases {
		r := env.call(http.MethodPost, "/api/v1/files/mkdir", ip, body, c.hdr, ck)
		if r.rec.Code != 403 || r.code(t) != c.code {
			t.Fatalf("%s: %d %s", c.name, r.rec.Code, r.rec.Body.String())
		}
	}
	if _, err := os.Lstat(filepath.Join(env.cfg.StorageRoot, "admin", "csrf-probe")); err == nil {
		t.Fatal("folder created despite CSRF rejection")
	}
	ok := map[string]string{"X-CSRF-Token": hdr["X-CSRF-Token"], "Origin": "https://file.example.test", "Sec-Fetch-Site": "same-origin"}
	if r := env.call(http.MethodPost, "/api/v1/files/mkdir", ip, body, ok, ck); r.rec.Code != 201 {
		t.Fatalf("legit request: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// After logout the cookie and its token are dead: 401 before any CSRF check.
	if r := env.call(http.MethodPost, "/api/v1/auth/logout", ip, nil, hdr, ck); r.rec.Code != 204 {
		t.Fatalf("logout: %d", r.rec.Code)
	}
	r := env.call(http.MethodPost, "/api/v1/files/mkdir", ip, map[string]any{"path": "/after-logout"}, hdr, ck)
	if r.rec.Code != 401 || r.code(t) != "UNAUTHENTICATED" {
		t.Fatalf("logged-out cookie: %d %s", r.rec.Code, r.rec.Body.String())
	}
}

// TestReviewASessionTimers: idle (sliding) and absolute expiry, cookie flags.
func TestReviewASessionTimers(t *testing.T) {
	env := newAuthEnv(t)
	ip := "203.0.113.5:1"
	r, ck, _ := env.login(t, "admin", initialAdminPassword, ip)
	if r.rec.Code != 200 {
		t.Fatalf("login: %d", r.rec.Code)
	}
	if !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" || ck.MaxAge != int((168*time.Hour).Seconds()) {
		t.Fatalf("cookie flags: %+v", ck)
	}
	// Sliding idle: activity at 1h59 keeps the session alive past 2h.
	env.clock.Advance(119 * time.Minute)
	if r := env.call(http.MethodGet, "/api/v1/auth/me", ip, nil, nil, ck); r.rec.Code != 200 {
		t.Fatalf("at 1h59: %d", r.rec.Code)
	}
	env.clock.Advance(119 * time.Minute)
	if r := env.call(http.MethodGet, "/api/v1/auth/me", ip, nil, nil, ck); r.rec.Code != 200 {
		t.Fatalf("at 3h58 after touch: %d", r.rec.Code)
	}
	env.clock.Advance(2*time.Hour + time.Second)
	r = env.call(http.MethodGet, "/api/v1/auth/me", ip, nil, nil, ck)
	if r.rec.Code != 401 || r.code(t) != "UNAUTHENTICATED" {
		t.Fatalf("idle expiry: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// Absolute: constant activity cannot extend past 168h.
	r, ck, _ = env.login(t, "admin", initialAdminPassword, ip)
	if r.rec.Code != 200 {
		t.Fatalf("login 2: %d", r.rec.Code)
	}
	for i := 0; i < 167; i++ {
		env.clock.Advance(time.Hour)
		if r := env.call(http.MethodGet, "/api/v1/auth/me", ip, nil, nil, ck); r.rec.Code != 200 {
			t.Fatalf("hour %d: %d", i+1, r.rec.Code)
		}
	}
	env.clock.Advance(time.Hour + time.Second)
	if r := env.call(http.MethodGet, "/api/v1/auth/me", ip, nil, nil, ck); r.rec.Code != 401 {
		t.Fatalf("absolute expiry: %d", r.rec.Code)
	}
}

// TestReviewANoSecretsInDebugLog runs login, password change, listing and
// mkdir with a debug logger and greps the output for secrets.
func TestReviewANoSecretsInDebugLog(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	ip := "203.0.113.5:1"
	env.call(http.MethodGet, "/api/v1/files/list?path=/&cursor=bogus&limit=5", ip, nil, nil, ck)
	env.call(http.MethodPost, "/api/v1/files/mkdir", ip, map[string]any{"path": "/x"}, hdr, ck)
	env.login(t, "admin", "wrong-password-for-log", ip)
	logs := env.logs.String()
	for _, secret := range []string{initialAdminPassword, readyPassword, "wrong-password-for-log", ck.Value, hdr["X-CSRF-Token"], strings.Repeat("ab", 32)} {
		if secret != "" && strings.Contains(logs, secret) {
			t.Fatalf("secret %q found in debug log:\n%s", secret, logs)
		}
	}
	if strings.Contains(logs, "cursor=bogus") {
		t.Fatal("query string logged")
	}
}

// TestReviewAAPIResponseHeaders checks the SPEC §10.3 headers on JSON
// responses (finding M-01: Cache-Control no-store and CORP were missing).
func TestReviewAAPIResponseHeaders(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	ip := "203.0.113.5:1"
	checks := []struct {
		method, path string
		body         any
		cookie       *http.Cookie
	}{
		{http.MethodGet, "/api/v1/auth/me", nil, ck},
		{http.MethodGet, "/api/v1/files/list?path=/", nil, ck},
		{http.MethodPost, "/api/v1/files/mkdir", map[string]any{"path": "/h"}, ck},
		{http.MethodGet, "/api/v1/files/list?path=/", nil, nil}, // 401 envelope too
		{http.MethodGet, "/api/v1/nope", nil, nil},
		{http.MethodGet, "/healthz", nil, nil},
	}
	for _, c := range checks {
		r := env.call(c.method, c.path, ip, c.body, hdr, c.cookie)
		h := r.rec.Header()
		want := map[string]string{
			"Cache-Control":                "no-store",
			"Cross-Origin-Resource-Policy": "same-origin",
			"Cross-Origin-Opener-Policy":   "same-origin",
			"X-Content-Type-Options":       "nosniff",
			"X-Frame-Options":              "DENY",
			"Referrer-Policy":              "strict-origin-when-cross-origin",
			"X-Robots-Tag":                 "noindex, nofollow",
		}
		for k, v := range want {
			if got := h.Get(k); got != v {
				t.Fatalf("%s %s (%d): header %s=%q want %q", c.method, c.path, r.rec.Code, k, got, v)
			}
		}
		if h.Get("Content-Security-Policy") == "" || h.Get("Permissions-Policy") == "" {
			t.Fatalf("%s %s: CSP/Permissions-Policy missing", c.method, c.path)
		}
	}
	// Hashed assets keep their immutable cache policy (served by webassets).
	r := env.call(http.MethodGet, "/assets/app.js", ip, nil, nil, nil)
	if cc := r.rec.Header().Get("Cache-Control"); cc == "no-store" {
		t.Fatalf("static asset got no-store: %s", cc)
	}
}

// TestReviewABlockedExtensionOnCreate: BLOCKED_EXTENSIONS must be enforced
// by POST /files/create (SPEC §5.2, finding M-02).
func TestReviewABlockedExtensionOnCreate(t *testing.T) {
	env := newAuthEnv(t, "BLOCKED_EXTENSIONS=exe, PHP ,.sh")
	ck, hdr := readyAdmin(t, env)
	ip := "203.0.113.5:1"
	for _, p := range []map[string]any{
		{"path": "/x.exe"}, {"path": "/x.EXE"}, {"path": "/", "name": "shell.php"}, {"path": "/run.sh"}, {"path": "/x.tar.exe"},
	} {
		r := env.call(http.MethodPost, "/api/v1/files/create", ip, p, hdr, ck)
		if r.rec.Code != 415 || r.code(t) != "UNSUPPORTED_TYPE" {
			t.Fatalf("create %v: %d %s", p, r.rec.Code, r.rec.Body.String())
		}
	}
	if entries, _ := os.ReadDir(filepath.Join(env.cfg.StorageRoot, "admin")); len(entries) != 0 {
		t.Fatalf("blocked files created: %v", entries)
	}
	for _, p := range []map[string]any{{"path": "/x.txt"}, {"path": "/exe"}, {"path": "/x.exe.txt"}} {
		r := env.call(http.MethodPost, "/api/v1/files/create", ip, p, hdr, ck)
		if r.rec.Code != 201 {
			t.Fatalf("create %v: %d %s", p, r.rec.Code, r.rec.Body.String())
		}
	}
	// Folders are not files: the extension rule does not apply to mkdir.
	if r := env.call(http.MethodPost, "/api/v1/files/mkdir", ip, map[string]any{"path": "/tools.exe"}, hdr, ck); r.rec.Code != 201 {
		t.Fatalf("mkdir: %d %s", r.rec.Code, r.rec.Body.String())
	}
}

// TestReviewAListLimitBoundedByPageSize: limit above LISTING_PAGE_SIZE is
// a validation error (SPEC §3.3, §6.1; finding m-04).
func TestReviewAListLimitBoundedByPageSize(t *testing.T) {
	env := newAuthEnv(t, "LISTING_PAGE_SIZE=50")
	ck, _ := readyAdmin(t, env)
	ip := "203.0.113.5:1"
	r := env.call(http.MethodGet, "/api/v1/files/list?path=/&limit=51", ip, nil, nil, ck)
	if r.rec.Code != 400 || errorField(r, "limit") == "" {
		t.Fatalf("limit 51 with page size 50: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := env.call(http.MethodGet, "/api/v1/files/list?path=/&limit=50", ip, nil, nil, ck); r.rec.Code != 200 {
		t.Fatalf("limit 50: %d", r.rec.Code)
	}
}

// TestReviewAOpenAPIContract is the route ↔ docs/openapi.yaml contract test
// required by SPEC §6.7 (finding M-04: it did not exist).
func TestReviewAOpenAPIContract(t *testing.T) {
	env := newAuthEnv(t)
	routes := chiRoutes(t, env.handler)
	documented := openAPIOperations(t, filepath.Join("..", "..", "..", "docs", "openapi.yaml"))
	for _, rt := range routes {
		if !documented[rt] {
			t.Errorf("route %s is not documented in docs/openapi.yaml", rt)
		}
	}
	mounted := map[string]bool{}
	for _, rt := range routes {
		mounted[rt] = true
	}
	for op := range documented {
		if !mounted[op] {
			t.Errorf("docs/openapi.yaml documents %s but the router does not mount it", op)
		}
	}
}

// chiRoutes lists "METHOD /path" for every route of the handler.
func chiRoutes(t *testing.T, h http.Handler) []string {
	t.Helper()
	r, ok := h.(chi.Routes)
	if !ok {
		t.Fatal("handler is not a chi router")
	}
	var out []string
	err := chi.Walk(r, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		route = strings.TrimSuffix(route, "/")
		if route == "" {
			route = "/"
		}
		out = append(out, method+" "+route)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(out)
	return out
}

var (
	openAPIPathRe   = regexp.MustCompile(`^  (/\S*):\s*$`)
	openAPIMethodRe = regexp.MustCompile(`^    (get|post|put|patch|delete|head|options):\s*$`)
)

// openAPIOperations scans the two-space-indented paths block of
// docs/openapi.yaml (no YAML dependency needed for this layout).
func openAPIOperations(t *testing.T, file string) map[string]bool {
	t.Helper()
	f, err := os.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	out := map[string]bool{}
	inPaths, current := false, ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "paths:":
			inPaths = true
			continue
		case inPaths && len(line) > 0 && line[0] != ' ' && line[0] != '#':
			inPaths = false
		}
		if !inPaths {
			continue
		}
		if m := openAPIPathRe.FindStringSubmatch(line); m != nil {
			current = m[1]
			continue
		}
		if m := openAPIMethodRe.FindStringSubmatch(line); m != nil && current != "" {
			out[strings.ToUpper(m[1])+" "+current] = true
		}
	}
	if len(out) == 0 {
		t.Fatalf("no operations parsed from %s", file)
	}
	return out
}

// TestReviewASeedNotRetriggeredByDeletedUsers: soft-deleting every user
// (or changing the env password) never re-seeds or resets the admin.
func TestReviewASeedNotRetriggeredByDeletedUsers(t *testing.T) {
	env := newAuthEnv(t)
	ctx := context.Background()
	readyAdmin(t, env)
	before, _ := env.store.GetUserByUsername(ctx, "admin")
	if err := env.store.SoftDeleteUser(ctx, sqlc.SoftDeleteUserParams{DeletedAt: ptr(t, "2026-10-14T10:00:00.000Z"), UpdatedAt: "2026-10-14T10:00:00.000Z", ID: before.ID}); err != nil {
		t.Fatal(err)
	}
	env.cfg.AdminInitialPassword = "AnotherInitialPassword2026!"
	if err := env.auth.Seed(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := env.store.GetUserByUsername(ctx, "admin")
	if n, _ := env.store.CountAllUsers(ctx); n != 1 || after.PasswordHash != before.PasswordHash || after.MustChangePassword != 0 {
		t.Fatalf("seed re-triggered: n=%d changed=%v", n, after.PasswordHash != before.PasswordHash)
	}
	// A deleted user cannot log in with either password.
	for _, pw := range []string{readyPassword, "AnotherInitialPassword2026!"} {
		if r, _, _ := env.login(t, "admin", pw, "203.0.113.5:1"); r.rec.Code != 401 {
			t.Fatalf("deleted admin login with %q: %d", pw, r.rec.Code)
		}
	}
}

// errorField returns details.fields[name] of an error envelope ("" if absent).
func errorField(r resp, name string) string {
	e, _ := r.body["error"].(map[string]any)
	details, _ := e["details"].(map[string]any)
	fields, _ := details["fields"].(map[string]any)
	v, _ := fields[name].(string)
	return v
}

func ptr(t *testing.T, s string) *string {
	t.Helper()
	return &s
}
