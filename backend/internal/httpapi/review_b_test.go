package httpapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Review B probes over HTTP (PROMPT Step R-B checklist a, d, g). Kept as
// regression tests.

// (d) POST /files/create writes bytes: it must reserve against the quota
// (SPEC §8.2 "semua writer") and account the bytes it wrote.
func TestReviewBCreateRespectsQuota(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	ip := "203.0.113.5:1"
	ctx := context.Background()
	admin, err := env.store.GetUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.UpdateUserQuota(ctx, sqlc.UpdateUserQuotaParams{QuotaBytes: 1500, UpdatedAt: db.Now(), ID: admin.ID}); err != nil {
		t.Fatal(err)
	}
	post := func(body any) resp {
		return env.call(http.MethodPost, "/api/v1/files/create", ip, body, hdr, ck)
	}
	r := post(map[string]any{"path": "/", "name": "a.txt", "content": strings.Repeat("a", 1000)})
	if r.rec.Code != 201 {
		t.Fatalf("first create: %d %s", r.rec.Code, r.rec.Body.String())
	}
	q := env.call(http.MethodGet, "/api/v1/me/quota", ip, nil, hdr, ck)
	if q.rec.Code != 200 || q.body["used"] != float64(1000) {
		t.Fatalf("used after create: %d %s", q.rec.Code, q.rec.Body.String())
	}
	r = post(map[string]any{"path": "/", "name": "b.txt", "content": strings.Repeat("b", 1000)})
	if r.rec.Code != 413 || r.code(t) != "QUOTA_EXCEEDED" || details(r)["reason"] != "quota" {
		t.Fatalf("create beyond quota: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(env.cfg.StorageRoot, "admin", "b.txt")); err == nil {
		t.Fatal("file must not be created when the quota is exceeded")
	}
	// an empty file and a folder never need bytes
	if r := post(map[string]any{"path": "/", "name": "empty.txt"}); r.rec.Code != 201 {
		t.Fatalf("empty create: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := env.call(http.MethodPost, "/api/v1/files/mkdir", ip, map[string]any{"path": "/", "name": "d"}, hdr, ck); r.rec.Code != 201 {
		t.Fatalf("mkdir: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// a template fits in the remaining 500 bytes only if it is small enough
	r = post(map[string]any{"path": "/", "name": "c.md", "template": "md"})
	if r.rec.Code != 201 && r.rec.Code != 413 {
		t.Fatalf("template create: %d %s", r.rec.Code, r.rec.Body.String())
	}
	q = env.call(http.MethodGet, "/api/v1/me/quota", ip, nil, hdr, ck)
	st, _ := os.Stat(filepath.Join(env.cfg.StorageRoot, "admin", "c.md"))
	want := int64(1000)
	if st != nil {
		want += st.Size()
	}
	if q.body["used"] != float64(want) {
		t.Fatalf("used after template: %v want %d", q.body["used"], want)
	}
}

// (a) user B with user A's ids/paths on every endpoint added in Steps 10a
// and 10b: nothing of A is visible, touched or deleted.
func TestReviewBIsolationOnNewEndpoints(t *testing.T) {
	env := newAuthEnv(t)
	ckA, hdrA := env.newUser(t, "alice", readyPassword)
	ckB, hdrB := env.newUser(t, "bob", readyPassword)
	ctx := context.Background()
	alice, _ := env.store.GetUserByUsername(ctx, "alice")
	homeA := filepath.Join(env.cfg.StorageRoot, "alice")
	seedFiles(t, homeA, "secret.txt", "dir/", "dir/x.txt", "gone/")
	ipA, ipB := "203.0.113.8:1", "203.0.113.9:1"
	callA := func(method, path string, body any) resp {
		return env.call(method, "/api/v1"+path, ipA, body, hdrA, ckA)
	}
	callB := func(method, path string, body any) resp {
		return env.call(method, "/api/v1"+path, ipB, body, hdrB, ckB)
	}
	must := func(r resp, code int, what string) {
		t.Helper()
		if r.rec.Code != code {
			t.Fatalf("%s: %d %s", what, r.rec.Code, r.rec.Body.String())
		}
	}

	// A builds state: metadata, an open, a trashed folder
	must(callA(http.MethodPut, "/meta/favorites", map[string]any{"paths": []string{"/secret.txt"}}), 200, "A favorite")
	must(callA(http.MethodPut, "/meta/pins", map[string]any{"paths": []string{"/secret.txt"}}), 200, "A pin")
	must(callA(http.MethodPut, "/meta/notes", map[string]any{"path": "/secret.txt", "note": "rahasia"}), 200, "A note")
	must(callA(http.MethodPut, "/meta/tags", map[string]any{"paths": []string{"/secret.txt"}, "color": "red"}), 200, "A tag")
	must(callA(http.MethodPost, "/me/recent", map[string]any{"path": "/secret.txt"}), 204, "A open")
	r := callA(http.MethodPost, "/files/delete", map[string]any{"paths": []string{"/gone"}})
	must(r, 200, "A delete")
	trashID := r.body["results"].([]any)[0].(map[string]any)["trashId"].(string)

	// B probes with A's paths and ids
	must(callB(http.MethodPost, "/files/dir-size", map[string]any{"path": "/dir"}), 404, "B dir-size")
	must(callB(http.MethodPost, "/files/checksum", map[string]any{"path": "/secret.txt", "algos": []string{"sha256"}}), 404, "B checksum")
	must(callB(http.MethodPost, "/files/checksum/verify", map[string]any{"path": "/secret.txt", "algo": "sha256", "expected": "00"}), 404, "B verify")
	r = callB(http.MethodPost, "/files/delete-permanent", map[string]any{"paths": []string{"/secret.txt", "/dir"}})
	must(r, 200, "B delete-permanent")
	if r.body["deleted"] != float64(0) {
		t.Fatalf("B deleted something: %s", r.rec.Body.String())
	}
	for _, f := range []string{"secret.txt", "dir/x.txt"} {
		if _, err := os.Stat(filepath.Join(homeA, f)); err != nil {
			t.Fatalf("A's %s is gone", f)
		}
	}
	must(callB(http.MethodPost, "/trash/restore", map[string]any{"ids": []string{trashID}}), 404, "B restore")
	must(callB(http.MethodPost, "/trash/restore", map[string]any{"ids": []string{trashID}, "to": "/"}), 404, "B restore to")
	r = callB(http.MethodPost, "/trash/purge", map[string]any{"ids": []string{trashID}})
	must(r, 200, "B purge")
	if r.body["deleted"] != float64(0) {
		t.Fatalf("B purged A's item: %s", r.rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(env.cfg.DataDir, "trash", alice.ID, trashID)); err != nil {
		t.Fatal("A's trash payload is gone")
	}
	r = callB(http.MethodPost, "/trash/empty", map[string]any{"confirm": "HAPUS"})
	must(r, 200, "B empty")
	if rows, _ := env.store.ListTrashItems(ctx, alice.ID); len(rows) != 1 {
		t.Fatalf("A's trash rows: %d", len(rows))
	}
	for _, p := range []string{"/meta/favorites", "/meta/tagged?color=red", "/meta/pins?dir=/", "/me/recent", "/me/activity"} {
		r := callB(http.MethodGet, p, nil)
		must(r, 200, "B "+p)
		if n, _ := r.body["total"].(float64); n != 0 {
			t.Fatalf("B %s sees %v items: %s", p, n, r.rec.Body.String())
		}
		if items, ok := r.body["items"].([]any); ok && len(items) != 0 {
			t.Fatalf("B %s sees items: %s", p, r.rec.Body.String())
		}
	}
	must(callB(http.MethodPut, "/meta/notes", map[string]any{"path": "/secret.txt", "note": "ubah"}), 404, "B note on A's path")
	must(callB(http.MethodPut, "/meta/favorites", map[string]any{"paths": []string{"/secret.txt"}}), 404, "B favorite A's path")
	must(callB(http.MethodPut, "/meta/tags", map[string]any{"paths": []string{"/secret.txt"}, "color": "blue"}), 404, "B tag A's path")
	// removals are per user: B's 204 must not touch A's rows
	must(callB(http.MethodDelete, "/meta/favorites", map[string]any{"paths": []string{"/secret.txt"}}), 204, "B unfavorite")
	must(callB(http.MethodDelete, "/meta/notes", map[string]any{"paths": []string{"/secret.txt"}}), 204, "B delete note")
	must(callB(http.MethodDelete, "/meta/tags", map[string]any{"paths": []string{"/secret.txt"}}), 204, "B delete tag")
	must(callB(http.MethodDelete, "/meta/pins", map[string]any{"paths": []string{"/secret.txt"}}), 204, "B unpin")
	must(callB(http.MethodDelete, "/me/activity", nil), 200, "B clear activity")
	r = callA(http.MethodGet, "/files/stat?path=/secret.txt", nil)
	must(r, 200, "A stat")
	it := r.body // stat merges the item fields into the top level (itemView)
	if it["favorite"] != true || it["pinned"] != true || it["note"] != "rahasia" || it["tag"] != "red" {
		t.Fatalf("A's metadata changed: %v", it)
	}
	if n, _ := env.store.CountActivity(ctx, alice.ID); n == 0 {
		t.Fatal("A's activity journal was cleared by B")
	}
	r = callB(http.MethodGet, "/me/storage-analysis", nil)
	must(r, 200, "B analysis")
	if r.body["total"] != float64(0) || strings.Contains(r.rec.Body.String(), "secret") {
		t.Fatalf("B's analysis leaks A: %s", r.rec.Body.String())
	}
	// (g) B can only sign URLs in its own scope; the scope is part of the signature
	r = callB(http.MethodPost, "/files/sign", map[string]any{"paths": []string{"/secret.txt"}, "purpose": "thumb"})
	must(r, 200, "B sign")
	u := r.body["urls"].([]any)[0].(map[string]any)["url"].(string)
	bob, _ := env.store.GetUserByUsername(ctx, "bob")
	if !strings.Contains(u, "scope=u%3A"+bob.ID) || strings.Contains(u, alice.ID) {
		t.Fatalf("signed scope: %s", u)
	}
	if strings.Contains(u, env.cfg.StorageRoot) {
		t.Fatalf("host path in signed url: %s", u)
	}
	must(callB(http.MethodPost, "/files/sign", map[string]any{"paths": []string{"/secret.txt"}, "purpose": "thumb", "scope": "u:" + alice.ID}), 400, "B sign with scope field")
}
