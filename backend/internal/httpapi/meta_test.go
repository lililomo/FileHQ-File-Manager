package httpapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/quota"
)

func itemByName(r resp, name string) map[string]any {
	items, _ := r.body["items"].([]any)
	for _, it := range items {
		m := it.(map[string]any)
		if m["name"] == name {
			return m
		}
	}
	return nil
}

// TestMetaAC walks the Step 10b acceptance criteria over HTTP: favorites,
// pins, notes and tags survive rename/move, broken references, pinned items
// first in every sort, the tag filter, Recent + "open" records, the journal,
// the 501-character note, the storage analysis total and IDOR between users.
func TestMetaAC(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	ip := "203.0.113.5:1"
	seedFiles(t, home, "proyek/", "proyek/a.txt", "proyek/b.txt", "proyek/z.txt", "proyek/sub/", "proyek/sub/c.txt", "lain/", "lain/d.txt", "e.txt")
	ctx := context.Background()
	admin, err := env.store.GetUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}
	post := func(path string, body any) resp { return env.call(http.MethodPost, "/api/v1"+path, ip, body, hdr, ck) }
	put := func(path string, body any) resp { return env.call(http.MethodPut, "/api/v1"+path, ip, body, hdr, ck) }
	del := func(path string, body any) resp {
		return env.call(http.MethodDelete, "/api/v1"+path, ip, body, hdr, ck)
	}
	get := func(path string) resp { return env.call(http.MethodGet, "/api/v1"+path, ip, nil, hdr, ck) }

	// --- write metadata ---
	if r := put("/meta/favorites", map[string]any{"paths": []string{"/proyek/a.txt", "/proyek"}}); r.rec.Code != 200 || r.body["count"] != float64(2) {
		t.Fatalf("favorites put: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := put("/meta/pins", map[string]any{"path": "/proyek/z.txt"}); r.rec.Code != 200 {
		t.Fatalf("pins put: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := put("/meta/notes", map[string]any{"path": "/proyek/a.txt", "note": "catatan"}); r.rec.Code != 200 {
		t.Fatalf("note put: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := put("/meta/tags", map[string]any{"paths": []string{"/proyek/a.txt", "/proyek/sub/c.txt"}, "color": "red"}); r.rec.Code != 200 {
		t.Fatalf("tags put: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// AC: 501-character note → 400 VALIDATION_FAILED; 500 ok
	if r := put("/meta/notes", map[string]any{"path": "/proyek/a.txt", "note": strings.Repeat("x", 501)}); r.rec.Code != 400 || r.code(t) != "VALIDATION_FAILED" {
		t.Fatalf("501 note: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := put("/meta/notes", map[string]any{"path": "/proyek/b.txt", "note": strings.Repeat("y", 500)}); r.rec.Code != 200 {
		t.Fatalf("500 note: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// validation: unknown colour, missing path, root
	if r := put("/meta/tags", map[string]any{"path": "/e.txt", "color": "pink"}); r.rec.Code != 400 {
		t.Fatalf("bad colour: %d", r.rec.Code)
	}
	if r := put("/meta/favorites", map[string]any{"path": "/nope.txt"}); r.rec.Code != 404 {
		t.Fatalf("missing path: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := put("/meta/pins", map[string]any{"path": "/"}); r.rec.Code != 400 {
		t.Fatalf("root: %d", r.rec.Code)
	}

	// --- listing enrichment: pinned first in every sort, badges, tag filter ---
	for _, q := range []string{"sort=name", "sort=name&dir=desc", "sort=size", "sort=mtime&dir=desc", "sort=note"} {
		r := get("/files/list?path=/proyek&" + q)
		if r.rec.Code != 200 {
			t.Fatalf("list %s: %d %s", q, r.rec.Code, r.rec.Body.String())
		}
		items := r.body["items"].([]any)
		if first := items[0].(map[string]any); first["name"] != "z.txt" || first["pinned"] != true {
			t.Fatalf("pinned not first for %s: %v", q, itemNames(t, r))
		}
	}
	r := get("/files/list?path=/proyek")
	a := itemByName(r, "a.txt")
	if a["favorite"] != true || a["note"] != "catatan" || a["tag"] != "red" || a["pinned"] != false {
		t.Fatalf("a enrichment: %v", a)
	}
	if p := itemByName(get("/files/list?path=/"), "proyek"); p["favorite"] != true {
		t.Fatalf("folder favorite: %v", p)
	}
	if r := get("/files/list?path=/proyek&tag=red"); itemNames(t, r) != "a.txt" {
		t.Fatalf("tag filter: %s", itemNames(t, r))
	}
	if r := get("/files/list?path=/proyek&tag=magenta"); r.rec.Code != 400 {
		t.Fatalf("bad tag filter: %d", r.rec.Code)
	}
	if r := get("/files/stat?path=/proyek/a.txt"); r.body["favorite"] != true || r.body["tag"] != "red" {
		t.Fatalf("stat enrichment: %s", r.rec.Body.String())
	}
	if r := get("/meta/tagged?color=red"); r.body["total"] != float64(2) {
		t.Fatalf("tagged: %s", r.rec.Body.String())
	}
	if r := get("/meta/pins?dir=/proyek"); r.body["max"] != float64(50) || len(r.body["items"].([]any)) != 1 {
		t.Fatalf("pins list: %s", r.rec.Body.String())
	}

	// --- AC: metadata survives rename of the item and move of the parent ---
	if r := post("/files/rename", map[string]any{"path": "/proyek/a.txt", "newName": "a2.txt"}); r.rec.Code != 200 {
		t.Fatalf("rename: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := post("/files/move", map[string]any{"paths": []string{"/proyek"}, "to": "/lain"}); r.rec.Code != 200 {
		t.Fatalf("move: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = get("/files/list?path=/lain/proyek")
	a = itemByName(r, "a2.txt")
	if a == nil || a["favorite"] != true || a["note"] != "catatan" || a["tag"] != "red" {
		t.Fatalf("after rename+move: %v", a)
	}
	if z := itemByName(r, "z.txt"); z["pinned"] != true {
		t.Fatalf("pin after move: %v", z)
	}
	if c := itemByName(get("/files/list?path=/lain/proyek/sub"), "c.txt"); c["tag"] != "red" {
		t.Fatalf("nested tag after move: %v", c)
	}
	if rows, _ := env.store.ListFavorites(ctx, admin.ID); len(rows) != 2 || rows[0].Path != "/lain/proyek" || rows[1].Path != "/lain/proyek/a2.txt" {
		t.Fatalf("favorites rows: %+v", rows)
	}
	r = get("/meta/favorites")
	if r.rec.Code != 200 || r.body["total"] != float64(2) {
		t.Fatalf("favorites list: %d %s", r.rec.Code, r.rec.Body.String())
	}
	for _, it := range r.body["items"].([]any) {
		if it.(map[string]any)["broken"] == true {
			t.Fatalf("broken favorite after move: %v", it)
		}
	}

	// --- AC: file removed on disk (SSH) → broken favorite, no 500 ---
	if err := os.Remove(filepath.Join(home, "lain", "proyek", "a2.txt")); err != nil {
		t.Fatal(err)
	}
	r = get("/meta/favorites")
	if r.rec.Code != 200 {
		t.Fatalf("favorites with broken: %d %s", r.rec.Code, r.rec.Body.String())
	}
	broken := itemByName(r, "a2.txt")
	if broken == nil || broken["broken"] != true || broken["brokenReason"] != "missing" {
		t.Fatalf("broken flag: %v", broken)
	}
	if r := del("/meta/favorites", map[string]any{"path": "/lain/proyek/a2.txt"}); r.rec.Code != 204 {
		t.Fatalf("remove broken favorite: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := get("/meta/favorites"); r.body["total"] != float64(1) {
		t.Fatalf("favorites after remove: %s", r.rec.Body.String())
	}
	// other removals
	if r := del("/meta/pins", map[string]any{"paths": []string{"/lain/proyek/z.txt"}}); r.rec.Code != 204 {
		t.Fatalf("unpin: %d", r.rec.Code)
	}
	if r := del("/meta/tags", map[string]any{"paths": []string{"/lain/proyek/sub/c.txt"}}); r.rec.Code != 204 {
		t.Fatalf("untag: %d", r.rec.Code)
	}
	if r := del("/meta/notes", map[string]any{"path": "/lain/proyek/b.txt"}); r.rec.Code != 204 {
		t.Fatalf("unnote: %d", r.rec.Code)
	}
	r = get("/files/list?path=/lain/proyek")
	if z := itemByName(r, "z.txt"); z["pinned"] != false {
		t.Fatalf("still pinned: %v", z)
	}
	if b := itemByName(r, "b.txt"); b["note"] != nil {
		t.Fatalf("note still there: %v", b)
	}

	// --- Recent: create (mkdir/create handlers) and open records; only existing paths ---
	if r := post("/files/create", map[string]any{"path": "/", "name": "baru.txt", "content": "x"}); r.rec.Code != 201 {
		t.Fatalf("create: %d", r.rec.Code)
	}
	if r := post("/me/recent", map[string]any{"path": "/e.txt"}); r.rec.Code != 204 {
		t.Fatalf("record open: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := post("/me/recent", map[string]any{"path": "/gone.txt"}); r.rec.Code != 404 {
		t.Fatalf("record open missing: %d", r.rec.Code)
	}
	r = get("/me/recent?limit=10")
	if r.rec.Code != 200 {
		t.Fatalf("recent: %d %s", r.rec.Code, r.rec.Body.String())
	}
	recent := r.body["items"].([]any)
	if len(recent) < 2 {
		t.Fatalf("recent items: %s", r.rec.Body.String())
	}
	first := recent[0].(map[string]any)
	if first["path"] != "/e.txt" || first["location"] != "/" || first["action"] != "open" {
		t.Fatalf("recent first: %v", first)
	}
	if second := recent[1].(map[string]any); second["path"] != "/baru.txt" || second["action"] != "create" {
		t.Fatalf("recent second: %v", second)
	}
	// a2.txt was favourite-opened? no; a removed file must never appear
	for _, it := range recent {
		if it.(map[string]any)["path"] == "/lain/proyek/a2.txt" {
			t.Fatal("missing file in recent")
		}
	}
	if r := get("/me/recent?limit=0"); r.rec.Code != 400 {
		t.Fatalf("recent limit: %d", r.rec.Code)
	}

	// --- Activity journal ---
	r = get("/me/activity?limit=3")
	if r.rec.Code != 200 || r.body["nextCursor"] == nil || len(r.body["items"].([]any)) != 3 {
		t.Fatalf("activity: %d %s", r.rec.Code, r.rec.Body.String())
	}
	top := r.body["items"].([]any)[0].(map[string]any)
	if top["action"] != "open" || top["exists"] != true || top["details"] == nil {
		t.Fatalf("activity row: %v", top)
	}
	if r := get("/me/activity?action=rename"); len(r.body["items"].([]any)) != 1 {
		t.Fatalf("activity action filter: %s", r.rec.Body.String())
	}
	if r := get("/me/activity?q=proyek"); len(r.body["items"].([]any)) < 2 {
		t.Fatalf("activity q filter: %s", r.rec.Body.String())
	}
	if r := get("/me/activity?from=2026-10-14&to=2026-10-14"); r.rec.Code != 200 || len(r.body["items"].([]any)) == 0 {
		t.Fatalf("activity date filter: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := get("/me/activity?from=yesterday"); r.rec.Code != 400 {
		t.Fatalf("activity bad date: %d", r.rec.Code)
	}
	if r := get("/me/activity?cursor=abc"); r.rec.Code != 400 {
		t.Fatalf("activity bad cursor: %d", r.rec.Code)
	}

	// --- Storage analysis: total equals the quota walk ---
	if _, err := env.quota.Recalculate(ctx, quota.Target{UserID: admin.ID, Jail: mustJail(t, env.cfg.StorageRoot, "admin", admin.ID)}); err != nil {
		t.Fatal(err)
	}
	r = get("/me/storage-analysis")
	if r.rec.Code != 200 {
		t.Fatalf("analysis: %d %s", r.rec.Code, r.rec.Body.String())
	}
	usage := get("/me/quota")
	if r.body["total"] != usage.body["used"] {
		t.Fatalf("analysis total %v != quota used %v", r.body["total"], usage.body["used"])
	}
	tree := r.body["tree"].(map[string]any)
	if tree["path"] != "/" || tree["size"] != r.body["total"] || r.body["topFiles"] == nil || r.body["byType"] == nil || r.body["quota"] == nil {
		t.Fatalf("analysis shape: %s", r.rec.Body.String())
	}
	if r2 := get("/me/storage-analysis"); r2.body["cached"] != true {
		t.Fatalf("analysis not cached: %s", r2.rec.Body.String())
	}
	if r3 := post("/me/storage-analysis", nil); r3.body["cached"] != false {
		t.Fatalf("recompute: %s", r3.rec.Body.String())
	}

	// --- IDOR: user B cannot see or touch A's metadata ---
	ckB, hdrB := env.newUserWith(t, "budi", "Password budi yang panjang 2026", false)
	getB := func(path string) resp {
		return env.call(http.MethodGet, "/api/v1"+path, "203.0.113.9:1", nil, hdrB, ckB)
	}
	putB := func(path string, body any) resp {
		return env.call(http.MethodPut, "/api/v1"+path, "203.0.113.9:1", body, hdrB, ckB)
	}
	if r := getB("/meta/favorites"); r.rec.Code != 200 || r.body["total"] != float64(0) {
		t.Fatalf("B favorites: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := putB("/meta/favorites", map[string]any{"path": "/lain/proyek"}); r.rec.Code != 404 {
		t.Fatalf("B favorite A's path: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := getB("/me/recent"); r.body["total"] != float64(0) {
		t.Fatalf("B recent: %s", r.rec.Body.String())
	}
	if r := getB("/me/activity"); len(r.body["items"].([]any)) != 0 {
		t.Fatalf("B activity sees A's rows: %s", r.rec.Body.String())
	}
	if r := getB("/meta/tagged?color=red"); r.body["total"] != float64(0) {
		t.Fatalf("B tagged: %s", r.rec.Body.String())
	}

	// --- clear journal ---
	if r := del("/me/activity", nil); r.rec.Code != 200 || r.body["deleted"].(float64) < 4 {
		t.Fatalf("clear activity: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := get("/me/activity"); len(r.body["items"].([]any)) != 0 {
		t.Fatal("journal not cleared")
	}
}

func mustJail(t *testing.T, root, username, id string) *fsjail.Jail {
	t.Helper()
	j, err := fsjail.OpenUser(root, username, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	return j
}
