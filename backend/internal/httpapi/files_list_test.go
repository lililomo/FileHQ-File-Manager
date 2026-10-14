package httpapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kppbwi.app/filemanager/internal/auth"
	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

const readyPassword = "Kelapa muda 2026 segar!"

// readyAdmin logs the seeded admin in and completes the forced password
// change so S+P endpoints are reachable. Returns cookie + CSRF header.
func readyAdmin(t *testing.T, env *authEnv) (*http.Cookie, map[string]string) {
	t.Helper()
	r, ck, csrf := env.login(t, "admin", initialAdminPassword, "203.0.113.5:1")
	if r.rec.Code != 200 {
		t.Fatalf("login: %d %s", r.rec.Code, r.rec.Body.String())
	}
	hdr := map[string]string{"X-CSRF-Token": csrf}
	r = env.call(http.MethodPost, "/api/v1/auth/change-password", "203.0.113.5:1",
		map[string]string{"currentPassword": initialAdminPassword, "newPassword": readyPassword}, hdr, ck)
	if r.rec.Code != 204 {
		t.Fatalf("change password: %d %s", r.rec.Code, r.rec.Body.String())
	}
	return ck, hdr
}

// newUser creates an active user without a forced password change and logs
// it in (which provisions its home).
func (e *authEnv) newUser(t *testing.T, username, password string) (*http.Cookie, map[string]string) {
	t.Helper()
	return e.newUserWith(t, username, password, false)
}

func (e *authEnv) newUserWith(t *testing.T, username, password string, mustChange bool) (*http.Cookie, map[string]string) {
	t.Helper()
	mc := int64(0)
	if mustChange {
		mc = 1
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	now := db.Now()
	if _, err := e.store.CreateUser(context.Background(), sqlc.CreateUserParams{
		ID: db.NewID(), Username: username, DisplayName: strings.ToUpper(username[:1]) + username[1:], Role: "user", Status: "active",
		PasswordHash: hash, MustChangePassword: mc, QuotaBytes: 10 << 30, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	r, ck, csrf := e.login(t, username, password, "203.0.113.9:1")
	if r.rec.Code != 200 {
		t.Fatalf("login %s: %d %s", username, r.rec.Code, r.rec.Body.String())
	}
	return ck, map[string]string{"X-CSRF-Token": csrf}
}

func itemNames(t *testing.T, r resp) string {
	t.Helper()
	items, _ := r.body["items"].([]any)
	out := make([]string, 0, len(items))
	for _, it := range items {
		m := it.(map[string]any)
		out = append(out, m["name"].(string))
	}
	return strings.Join(out, ",")
}

func seedFiles(t *testing.T, home string, names ...string) {
	t.Helper()
	for _, n := range names {
		p := filepath.Join(home, n)
		if strings.HasSuffix(n, "/") {
			if err := os.MkdirAll(strings.TrimSuffix(p, "/"), 0o750); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(p, []byte(n), 0o640); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFilesListAC(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	seedFiles(t, home, "file1", "file2", "file10", "File3", "é.txt")

	r := env.call(http.MethodGet, "/api/v1/files/list?path=/", "203.0.113.5:1", nil, nil, ck)
	if r.rec.Code != 200 {
		t.Fatalf("list: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if got := itemNames(t, r); got != "é.txt,file1,file2,File3,file10" {
		t.Fatalf("natural order: %s", got)
	}
	if r.body["total"] != float64(5) || r.body["nextCursor"] != nil || r.body["parentPath"] != nil {
		t.Fatalf("meta: %v", r.body)
	}
	first := r.body["items"].([]any)[0].(map[string]any)
	for _, k := range []string{"name", "path", "type", "isDir", "size", "mime", "typeLabel", "ext", "mtime", "mode", "modeOctal", "etag", "shared"} {
		if _, ok := first[k]; !ok {
			t.Fatalf("item missing %s: %v", k, first)
		}
	}
	if first["path"] != "/é.txt" || first["typeLabel"] != "type.text.plain" {
		t.Fatalf("item: %v", first)
	}

	// Cursor paging continues without duplicates even when a file appears.
	r = env.call(http.MethodGet, "/api/v1/files/list?path=/&limit=2", "203.0.113.5:1", nil, nil, ck)
	cur, _ := r.body["nextCursor"].(string)
	if r.rec.Code != 200 || itemNames(t, r) != "é.txt,file1" || cur == "" {
		t.Fatalf("page1: %d %s", r.rec.Code, r.rec.Body.String())
	}
	seedFiles(t, home, "file12")
	r = env.call(http.MethodGet, "/api/v1/files/list?path=/&limit=2&cursor="+cur, "203.0.113.5:1", nil, nil, ck)
	if r.rec.Code != 200 || itemNames(t, r) != "file2,File3" {
		t.Fatalf("page2: %d %s", r.rec.Code, itemNames(t, r))
	}
	cur, _ = r.body["nextCursor"].(string)
	r = env.call(http.MethodGet, "/api/v1/files/list?path=/&limit=2&cursor="+cur, "203.0.113.5:1", nil, nil, ck)
	if r.rec.Code != 200 || r.body["nextCursor"] != nil {
		t.Fatalf("page3: %d %s", r.rec.Code, r.rec.Body.String())
	}
	names3 := itemNames(t, r)
	if names3 != "file10,file12" && names3 != "file10" {
		t.Fatalf("page3 names: %s", names3)
	}

	// Validation.
	for _, c := range []struct {
		q, code string
		status  int
	}{
		{"path=../x", "PATH_INVALID", 400},
		{"path=/a%00b", "PATH_INVALID", 400},
		{"path=/.system", "NOT_FOUND", 404},
		{"path=/missing", "NOT_FOUND", 404},
		{"path=/file1", "VALIDATION_FAILED", 400},
		{"path=/&sort=color", "VALIDATION_FAILED", 400},
		{"path=/&dir=sideways", "VALIDATION_FAILED", 400},
		{"path=/&limit=0", "VALIDATION_FAILED", 400},
		{"path=/&limit=1001", "VALIDATION_FAILED", 400},
		{"path=/&types=nope", "VALIDATION_FAILED", 400},
		{"path=/&cursor=garbage", "VALIDATION_FAILED", 400},
		{"path=/&share=abc", "VALIDATION_FAILED", 400},
	} {
		r = env.call(http.MethodGet, "/api/v1/files/list?"+c.q, "203.0.113.5:1", nil, nil, ck)
		if r.rec.Code != c.status || r.code(t) != c.code {
			t.Fatalf("%s: got %d %s, want %d %s", c.q, r.rec.Code, r.code(t), c.status, c.code)
		}
	}
	// Sort by size desc + hidden + filter.
	seedFiles(t, home, ".secret")
	r = env.call(http.MethodGet, "/api/v1/files/list?path=/&hidden=false&filter=FILE1", "203.0.113.5:1", nil, nil, ck)
	if r.rec.Code != 200 || itemNames(t, r) != "file1,file10,file12" {
		t.Fatalf("filter: %d %s", r.rec.Code, itemNames(t, r))
	}
	r = env.call(http.MethodGet, "/api/v1/files/list?path=/&sort=size&dir=desc&hidden=false", "203.0.113.5:1", nil, nil, ck)
	if r.rec.Code != 200 || !strings.HasPrefix(itemNames(t, r), "é.txt,file10,file12,") {
		t.Fatalf("size desc: %s", itemNames(t, r))
	}
	// Auth gates.
	r = env.call(http.MethodGet, "/api/v1/files/list?path=/", "203.0.113.5:1", nil, nil, nil)
	if r.rec.Code != 401 {
		t.Fatalf("no session: %d", r.rec.Code)
	}
	_ = hdr
}

func TestFilesMkdirCreateStat(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	ip := "203.0.113.5:1"

	// mkdir "con": created with a Windows-compatibility warning.
	r := env.call(http.MethodPost, "/api/v1/files/mkdir", ip, map[string]any{"path": "/", "name": "con"}, hdr, ck)
	if r.rec.Code != 201 {
		t.Fatalf("mkdir con: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if w := r.body["warnings"].([]any); len(w) != 1 || w[0] != "windows_incompatible" {
		t.Fatalf("warnings: %v", r.body["warnings"])
	}
	if item := r.body["item"].(map[string]any); item["isDir"] != true || item["path"] != "/con" || item["itemCount"] != float64(0) {
		t.Fatalf("item: %v", item)
	}
	if info, err := os.Stat(filepath.Join(home, "con")); err != nil || !info.IsDir() || info.Mode().Perm() != 0o750 {
		t.Fatalf("on disk: %v %v", info, err)
	}
	// Invalid names.
	r = env.call(http.MethodPost, "/api/v1/files/mkdir", ip, map[string]any{"path": "/", "name": "a/b"}, hdr, ck)
	if r.rec.Code != 400 || r.code(t) != "NAME_INVALID" {
		t.Fatalf("a/b: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/files/mkdir", ip, map[string]any{"path": "/..", "name": "x"}, hdr, ck)
	if r.rec.Code != 400 || r.code(t) != "PATH_INVALID" {
		t.Fatalf("..: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/files/mkdir", ip, map[string]any{"path": "/.system"}, hdr, ck)
	if r.rec.Code != 404 {
		t.Fatalf(".system: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/files/mkdir", ip, map[string]any{"path": "/"}, hdr, ck)
	if r.rec.Code != 400 || r.code(t) != "VALIDATION_FAILED" {
		t.Fatalf("root: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// Duplicate → 409 with existing details; full-path form (SPEC).
	r = env.call(http.MethodPost, "/api/v1/files/mkdir", ip, map[string]any{"path": "/con"}, hdr, ck)
	if r.rec.Code != 409 || r.code(t) != "ALREADY_EXISTS" {
		t.Fatalf("dup: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if ex := r.body["error"].(map[string]any)["details"].(map[string]any)["existing"].(map[string]any); ex["isDir"] != true {
		t.Fatalf("existing: %v", ex)
	}
	// parents=true creates intermediate folders; parents=false fails.
	r = env.call(http.MethodPost, "/api/v1/files/mkdir", ip, map[string]any{"path": "/a/b/c"}, hdr, ck)
	if r.rec.Code != 404 {
		t.Fatalf("no parents: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/files/mkdir", ip, map[string]any{"path": "/a/b/c", "parents": true}, hdr, ck)
	if r.rec.Code != 201 {
		t.Fatalf("parents: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// CSRF required.
	r = env.call(http.MethodPost, "/api/v1/files/mkdir", ip, map[string]any{"path": "/nocsrf"}, nil, ck)
	if r.rec.Code != 403 || r.code(t) != "CSRF_INVALID" {
		t.Fatalf("csrf: %d", r.rec.Code)
	}

	// create from template, with content, empty, duplicate, unknown template.
	r = env.call(http.MethodPost, "/api/v1/files/create", ip, map[string]any{"path": "/a", "name": "notes.md", "template": "md"}, hdr, ck)
	if r.rec.Code != 201 {
		t.Fatalf("create md: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if b, err := os.ReadFile(filepath.Join(home, "a", "notes.md")); err != nil || string(b) != "# Judul\n\n" {
		t.Fatalf("template content: %q %v", b, err)
	}
	item := r.body["item"].(map[string]any)
	if item["mime"] != "text/markdown" || item["size"] != float64(9) || item["ext"] != "md" {
		t.Fatalf("created item: %v", item)
	}
	r = env.call(http.MethodPost, "/api/v1/files/create", ip, map[string]any{"path": "/a/data.json", "content": `{"a":1}`}, hdr, ck)
	if r.rec.Code != 201 {
		t.Fatalf("create content: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if b, _ := os.ReadFile(filepath.Join(home, "a", "data.json")); string(b) != `{"a":1}` {
		t.Fatalf("content: %q", b)
	}
	r = env.call(http.MethodPost, "/api/v1/files/create", ip, map[string]any{"path": "/a/empty"}, hdr, ck)
	if r.rec.Code != 201 || r.body["item"].(map[string]any)["size"] != float64(0) {
		t.Fatalf("create empty: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/files/create", ip, map[string]any{"path": "/a/empty"}, hdr, ck)
	if r.rec.Code != 409 || r.code(t) != "ALREADY_EXISTS" {
		t.Fatalf("create dup: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/files/create", ip, map[string]any{"path": "/a/x.exe", "template": "exe"}, hdr, ck)
	if r.rec.Code != 400 || r.code(t) != "VALIDATION_FAILED" {
		t.Fatalf("unknown template: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/files/create", ip, map[string]any{"path": "/con", "name": "x.txt"}, hdr, ck)
	if r.rec.Code != 201 {
		t.Fatalf("create inside con: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// The listing cache was invalidated by the mutations.
	r = env.call(http.MethodGet, "/api/v1/files/list?path=/a&counts=1", ip, nil, nil, ck)
	if r.rec.Code != 200 || itemNames(t, r) != "b,data.json,empty,notes.md" {
		t.Fatalf("list /a: %d %s", r.rec.Code, itemNames(t, r))
	}
	if b := r.body["items"].([]any)[0].(map[string]any); b["itemCount"] != float64(1) || r.body["parentPath"] != "/" {
		t.Fatalf("counts/parent: %v %v", b["itemCount"], r.body["parentPath"])
	}

	// stat
	r = env.call(http.MethodGet, "/api/v1/files/stat?path=/a/notes.md", ip, nil, nil, ck)
	if r.rec.Code != 200 || r.body["name"] != "notes.md" || r.body["stat"] == nil || r.body["versionsCount"] != float64(0) {
		t.Fatalf("stat file: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if w := r.body["warnings"].([]any); len(w) != 0 {
		t.Fatalf("warnings: %v", w)
	}
	r = env.call(http.MethodGet, "/api/v1/files/stat?path=/con", ip, nil, nil, ck)
	if r.rec.Code != 200 || r.body["isDir"] != true || r.body["itemCount"] != float64(1) || r.body["warnings"].([]any)[0] != "windows_incompatible" {
		t.Fatalf("stat dir: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/files/stat?path=/", ip, nil, nil, ck)
	if r.rec.Code != 200 || r.body["path"] != "/" || r.body["isDir"] != true {
		t.Fatalf("stat root: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/files/stat?path=/nope", ip, nil, nil, ck)
	if r.rec.Code != 404 {
		t.Fatalf("stat missing: %d", r.rec.Code)
	}
}

func TestFilesUserIsolationAndGates(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	ip := "203.0.113.5:1"
	seedFiles(t, filepath.Join(env.cfg.StorageRoot, "admin"), "secret-admin.txt")

	bck, bhdr := env.newUser(t, "budi", "Password budi yang panjang 2026")
	r := env.call(http.MethodGet, "/api/v1/files/list?path=/", ip, nil, nil, bck)
	if r.rec.Code != 200 || r.body["total"] != float64(0) {
		t.Fatalf("budi root: %d %s", r.rec.Code, r.rec.Body.String())
	}
	for _, p := range []string{"/../admin", "/../admin/secret-admin.txt", "/%2e%2e/admin", "/../.system/db"} {
		r = env.call(http.MethodGet, "/api/v1/files/list?path="+p, ip, nil, nil, bck)
		if r.rec.Code != 400 || r.code(t) != "PATH_INVALID" {
			t.Fatalf("%s: %d %s", p, r.rec.Code, r.code(t))
		}
	}
	// Budi's files stay invisible to admin and vice versa.
	r = env.call(http.MethodPost, "/api/v1/files/mkdir", ip, map[string]any{"path": "/rahasia-budi"}, bhdr, bck)
	if r.rec.Code != 201 {
		t.Fatalf("budi mkdir: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/files/list?path=/", ip, nil, nil, ck)
	if r.rec.Code != 200 || strings.Contains(itemNames(t, r), "rahasia") || !strings.Contains(itemNames(t, r), "secret-admin") {
		t.Fatalf("admin sees: %s", itemNames(t, r))
	}
	r = env.call(http.MethodGet, "/api/v1/files/list?path=/", ip, nil, nil, bck)
	if r.rec.Code != 200 || itemNames(t, r) != "rahasia-budi" {
		t.Fatalf("budi sees: %s", itemNames(t, r))
	}
	// The home directory is a symlink → refused by the jail cache (it could
	// alias another user's home). Tested at the cache level: a cached handle
	// opened before the swap keeps pointing at the old inode until evicted.
	if err := os.RemoveAll(filepath.Join(env.cfg.StorageRoot, "budi")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(env.cfg.StorageRoot, "admin"), filepath.Join(env.cfg.StorageRoot, "budi")); err != nil {
		t.Fatal(err)
	}
	jc := newJailCache(nil)
	if _, _, err := jc.acquire(env.cfg.StorageRoot, sqlc.User{ID: "budi-id", Username: "budi"}); err == nil {
		t.Fatal("symlinked home must be refused")
	}
	if _, _, err := jc.acquire(env.cfg.StorageRoot, sqlc.User{ID: "nobody", Username: "nobody"}); err == nil {
		t.Fatal("missing home must be refused")
	}
	j, release, err := jc.acquire(env.cfg.StorageRoot, sqlc.User{ID: "admin-id", Username: "admin"})
	if err != nil || j == nil {
		t.Fatalf("admin home: %v", err)
	}
	j2, release2, _ := jc.acquire(env.cfg.StorageRoot, sqlc.User{ID: "admin-id", Username: "admin"})
	if j2 != j {
		t.Fatal("handle must be reused per user")
	}
	release()
	release2()
	jc.evict("admin-id")
	if _, ok := jc.m["admin-id"]; ok {
		t.Fatal("evict failed")
	}

	// must_change_password gate: a user with the flag cannot list.
	nck, _ := env.newUserWith(t, "sari", "Password sari yang panjang 2026", true)
	r = env.call(http.MethodGet, "/api/v1/files/list?path=/", ip, nil, nil, nck)
	if r.rec.Code != 403 || r.code(t) != "PASSWORD_CHANGE_REQUIRED" {
		t.Fatalf("gate: %d %s", r.rec.Code, r.rec.Body.String())
	}
	_ = hdr
}

func TestFilesTemplates(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	ip := "203.0.113.5:1"
	r := env.call(http.MethodGet, "/api/v1/files/templates", ip, nil, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("templates: %d %s", r.rec.Code, r.rec.Body.String())
	}
	list, _ := r.body["templates"].([]any)
	if len(list) == 0 {
		t.Fatalf("templates empty: %v", r.body)
	}
	seen := map[string]bool{}
	for _, v := range list {
		seen[v.(string)] = true
	}
	for _, want := range []string{"txt", "md", "json"} {
		if !seen[want] {
			t.Errorf("template %q missing from %v", want, list)
		}
	}
	// Anonymous callers are rejected like every other /files endpoint.
	r = env.call(http.MethodGet, "/api/v1/files/templates", ip, nil, nil, nil)
	if r.rec.Code != 401 {
		t.Fatalf("anonymous: %d", r.rec.Code)
	}
}
