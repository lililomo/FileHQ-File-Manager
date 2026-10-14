package httpapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"kppbwi.app/filemanager/internal/db"
)

func details(r resp) map[string]any {
	e, _ := r.body["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	return d
}

func listNames(t *testing.T, env *authEnv, ck *http.Cookie, hdr map[string]string, dir string) string {
	t.Helper()
	r := env.call(http.MethodGet, "/api/v1/files/list?path="+dir, "203.0.113.5:1", nil, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("list %s: %d %s", dir, r.rec.Code, r.rec.Body.String())
	}
	return itemNames(t, r)
}

// TestFileOpsAC walks the acceptance criteria of PROMPT Step 9a over HTTP.
func TestFileOpsAC(t *testing.T) {
	env := newAuthEnv(t, "BLOCKED_EXTENSIONS=exe,sh")
	ck, hdr := readyAdmin(t, env)
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	ip := "203.0.113.5:1"
	seedFiles(t, home, "a.txt", "dir/", "dir/sub/", "dir/x.txt", "b.txt", "dest/", "dest/b.txt")
	ctx := context.Background()
	admin, err := env.store.GetUserByUsername(ctx, "admin")
	if err != nil {
		t.Fatal(err)
	}

	// AC1: rename + undo
	r := env.call(http.MethodPost, "/api/v1/files/rename", ip, map[string]any{"path": "/a.txt", "newName": "renamed.txt"}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("rename: %d %s", r.rec.Code, r.rec.Body.String())
	}
	token, _ := r.body["undoToken"].(string)
	undo, _ := r.body["undo"].(map[string]any)
	item, _ := r.body["item"].(map[string]any)
	if token == "" || undo["kind"] != "rename" || item["path"] != "/renamed.txt" {
		t.Fatalf("rename body: %v", r.body)
	}
	if listNames(t, env, ck, hdr, "/") != "dest,dir,b.txt,renamed.txt" {
		t.Fatalf("listing after rename: %s", listNames(t, env, ck, hdr, "/"))
	}
	r = env.call(http.MethodPost, "/api/v1/files/undo", ip, map[string]any{"token": token}, hdr, ck)
	if r.rec.Code != 200 || r.body["undo"] != nil {
		t.Fatalf("undo: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(home, "a.txt")); err != nil {
		t.Fatal("undo did not restore a.txt")
	}
	r = env.call(http.MethodPost, "/api/v1/files/undo", ip, map[string]any{"token": token}, hdr, ck)
	if r.rec.Code != 404 || details(r)["reason"] != "undo_expired" {
		t.Fatalf("second undo: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// snake_case alias and blocked extension
	r = env.call(http.MethodPost, "/api/v1/files/rename", ip, map[string]any{"path": "/a.txt", "new_name": "a.exe"}, hdr, ck)
	if r.rec.Code != 415 || r.code(t) != "UNSUPPORTED_TYPE" {
		t.Fatalf("blocked ext: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/files/rename", ip, map[string]any{"path": "/a.txt", "newName": "b.txt"}, hdr, ck)
	if r.rec.Code != 409 || r.code(t) != "ALREADY_EXISTS" || details(r)["existing"] == nil {
		t.Fatalf("rename conflict: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// AC2: move into itself
	r = env.call(http.MethodPost, "/api/v1/files/move", ip, map[string]any{"paths": []string{"/dir"}, "to": "/dir/sub"}, hdr, ck)
	if r.rec.Code != 400 || r.code(t) != "VALIDATION_FAILED" || details(r)["reason"] != "cannot_move_into_self" {
		t.Fatalf("move into self: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// AC3: copy conflict without policy, then on_conflict=rename
	r = env.call(http.MethodPost, "/api/v1/files/copy", ip, map[string]any{"paths": []string{"/b.txt"}, "to": "/dest"}, hdr, ck)
	if r.rec.Code != 409 || r.code(t) != "ALREADY_EXISTS" {
		t.Fatalf("copy conflict: %d %s", r.rec.Code, r.rec.Body.String())
	}
	d := details(r)
	existing, _ := d["existing"].(map[string]any)
	incoming, _ := d["incoming"].(map[string]any)
	if existing["size"] != float64(len("dest/b.txt")) || incoming["size"] != float64(len("b.txt")) || existing["mtime"] == nil || d["conflicts"] == nil {
		t.Fatalf("conflict details: %v", d)
	}
	r = env.call(http.MethodPost, "/api/v1/files/copy", ip, map[string]any{"paths": []string{"/b.txt"}, "to": "/dest", "on_conflict": "rename"}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("copy rename: %d %s", r.rec.Code, r.rec.Body.String())
	}
	results := r.body["results"].([]any)
	if first := results[0].(map[string]any); first["to"] != "/dest/b (1).txt" || first["status"] != "renamed" {
		t.Fatalf("copy results: %v", results)
	}
	if listNames(t, env, ck, hdr, "/dest") != "b (1).txt,b.txt" {
		t.Fatalf("dest listing: %s", listNames(t, env, ck, hdr, "/dest"))
	}
	r = env.call(http.MethodPost, "/api/v1/files/copy", ip, map[string]any{"paths": []string{"/b.txt"}, "to": "/dest", "onConflict": "bogus"}, hdr, ck)
	if r.rec.Code != 400 {
		t.Fatalf("bad policy: %d", r.rec.Code)
	}

	// AC4: delete → listing, trash_items, physical file
	r = env.call(http.MethodPost, "/api/v1/files/delete", ip, map[string]any{"paths": []string{"/dest/b (1).txt", "/dir"}}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("delete: %d %s", r.rec.Code, r.rec.Body.String())
	}
	batchID, _ := r.body["batchId"].(string)
	if batchID == "" || r.body["undoToken"] == nil {
		t.Fatalf("delete body: %v", r.body)
	}
	if names := listNames(t, env, ck, hdr, "/"); names != "dest,a.txt,b.txt" {
		t.Fatalf("listing after delete: %s", names)
	}
	rows, err := env.store.ListTrashByBatch(ctx, admin.ID, batchID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("trash rows: %v %+v", err, rows)
	}
	for _, row := range rows {
		if _, err := os.Lstat(filepath.Join(env.cfg.DataDir, "trash", admin.ID, row.ID)); err != nil {
			t.Fatalf("physical trash item: %v", err)
		}
	}
	r = env.call(http.MethodPost, "/api/v1/files/undo", ip, map[string]any{"undoToken": r.body["undoToken"]}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("undo delete: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(home, "dir/x.txt")); err != nil {
		t.Fatal("undo delete did not restore the folder")
	}

	// AC5: rename folder updates favorites/file_notes in one transaction
	now := db.Now()
	_ = env.store.UpsertFavorite(ctx, admin.ID, "/dir/sub", now)
	_ = env.store.UpsertFileNote(ctx, admin.ID, "/dir/x.txt", "catatan", now)
	_ = env.store.UpsertFileNote(ctx, admin.ID, "/director/y.txt", "sibling prefix", now)
	r = env.call(http.MethodPost, "/api/v1/files/rename", ip, map[string]any{"path": "/dir", "newName": "proyek2"}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("rename folder: %d %s", r.rec.Code, r.rec.Body.String())
	}
	favs, _ := env.store.ListFavorites(ctx, admin.ID)
	notes, _ := env.store.ListFileNotes(ctx, admin.ID)
	if len(favs) != 1 || favs[0].Path != "/proyek2/sub" {
		t.Fatalf("favorites: %+v", favs)
	}
	if len(notes) != 2 || notes[0].Path != "/director/y.txt" || notes[1].Path != "/proyek2/x.txt" {
		t.Fatalf("notes: %+v", notes)
	}

	// AC7: batch rename preview detects collision; apply → 409
	seedFiles(t, home, "p1.txt", "P1.txt")
	body := map[string]any{"paths": []string{"/p1.txt", "/P1.txt"}, "rule": map[string]any{"case": "lower"}}
	r = env.call(http.MethodPost, "/api/v1/files/batch-rename/preview", ip, body, hdr, ck)
	if r.rec.Code != 200 || r.body["clean"] != false {
		t.Fatalf("preview: %d %s", r.rec.Code, r.rec.Body.String())
	}
	items := r.body["items"].([]any)
	if items[1].(map[string]any)["status"] != "collision" {
		t.Fatalf("preview items: %v", items)
	}
	r = env.call(http.MethodPost, "/api/v1/files/batch-rename", ip, body, hdr, ck)
	if r.rec.Code != 409 || r.code(t) != "CONFLICT" || details(r)["items"] == nil {
		t.Fatalf("apply with collision: %d %s", r.rec.Code, r.rec.Body.String())
	}
	body = map[string]any{"paths": []string{"/p1.txt", "/P1.txt"}, "rule": map[string]any{"numbering": map[string]any{"pattern": "foto-{n}", "start": 1, "pad": 2}}}
	r = env.call(http.MethodPost, "/api/v1/files/batch-rename", ip, body, hdr, ck)
	if r.rec.Code != 200 || r.body["undoToken"] == nil {
		t.Fatalf("apply: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if names := listNames(t, env, ck, hdr, "/"); names != "dest,proyek2,a.txt,b.txt,foto-01.txt,foto-02.txt" {
		t.Fatalf("after batch rename: %s", names)
	}
	r = env.call(http.MethodPost, "/api/v1/files/undo", ip, map[string]any{"token": r.body["undoToken"]}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("undo batch: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if names := listNames(t, env, ck, hdr, "/"); names != "dest,proyek2,a.txt,b.txt,P1.txt,p1.txt" {
		t.Fatalf("after undo batch rename: %s", names)
	}

	// duplicate, symlink, chmod
	r = env.call(http.MethodPost, "/api/v1/files/duplicate", ip, map[string]any{"path": "/a.txt"}, hdr, ck)
	if r.rec.Code != 201 || r.body["item"].(map[string]any)["path"] != "/a (1).txt" {
		t.Fatalf("duplicate: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/files/symlink", ip, map[string]any{"path": "/link", "target": "a.txt"}, hdr, ck)
	if r.rec.Code != 201 || r.body["item"].(map[string]any)["isSymlink"] != true {
		t.Fatalf("symlink: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/files/symlink", ip, map[string]any{"path": "/esc", "target": "../../etc/passwd"}, hdr, ck)
	if r.rec.Code != 400 || r.code(t) != "SYMLINK_ESCAPE" {
		t.Fatalf("symlink escape: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/files/chmod", ip, map[string]any{"paths": []string{"/a.txt"}, "mode": "4600"}, hdr, ck)
	if r.rec.Code != 200 || r.body["undo"] != nil {
		t.Fatalf("chmod: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if st, _ := os.Stat(filepath.Join(home, "a.txt")); st.Mode().Perm() != 0o600 || st.Mode()&os.ModeSetuid != 0 {
		t.Fatalf("mode: %v", st.Mode())
	}
	r = env.call(http.MethodPost, "/api/v1/files/chmod", ip, map[string]any{"paths": []string{"/a.txt"}, "mode": "rwx"}, hdr, ck)
	if r.rec.Code != 400 {
		t.Fatalf("bad mode: %d", r.rec.Code)
	}
	// .system and root are rejected
	r = env.call(http.MethodPost, "/api/v1/files/delete", ip, map[string]any{"paths": []string{"/.system"}}, hdr, ck)
	if r.rec.Code != 404 {
		t.Fatalf(".system: %d", r.rec.Code)
	}
	r = env.call(http.MethodPost, "/api/v1/files/delete", ip, map[string]any{"paths": []string{"/"}}, hdr, ck)
	if r.rec.Code != 400 {
		t.Fatalf("root: %d", r.rec.Code)
	}
	// unknown field / CSRF
	r = env.call(http.MethodPost, "/api/v1/files/delete", ip, map[string]any{"paths": []string{"/a.txt"}, "force": true}, hdr, ck)
	if r.rec.Code != 400 {
		t.Fatalf("unknown field: %d", r.rec.Code)
	}
	r = env.call(http.MethodPost, "/api/v1/files/delete", ip, map[string]any{"paths": []string{"/a.txt"}}, nil, ck)
	if r.rec.Code != 403 || r.code(t) != "CSRF_INVALID" {
		t.Fatalf("csrf: %d %s", r.rec.Code, r.rec.Body.String())
	}
}

// AC6: two concurrent moves of the same path → one 200, one 409.
func TestFileOpsConcurrentMoveLocked(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	seedFiles(t, home, "f.txt", "d1/", "d2/")
	ip := "203.0.113.5:1"
	started := make(chan struct{})
	proceed := make(chan struct{})
	var once sync.Once
	env.files.TestHook = func(string) {
		once.Do(func() {
			close(started)
			<-proceed
		})
	}
	var first resp
	done := make(chan struct{})
	go func() {
		first = env.call(http.MethodPost, "/api/v1/files/move", ip, map[string]any{"paths": []string{"/f.txt"}, "to": "/d1"}, hdr, ck)
		close(done)
	}()
	<-started
	second := env.call(http.MethodPost, "/api/v1/files/move", ip, map[string]any{"paths": []string{"/f.txt"}, "to": "/d2"}, hdr, ck)
	close(proceed)
	<-done
	if first.rec.Code != 200 {
		t.Fatalf("first: %d %s", first.rec.Code, first.rec.Body.String())
	}
	if second.rec.Code != 409 || second.code(t) != "OPERATION_IN_PROGRESS" || details(second)["by"] != "move" {
		t.Fatalf("second: %d %s", second.rec.Code, second.rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(home, "d1/f.txt")); err != nil {
		t.Fatal("first move must have happened")
	}
}

// IDOR: user B cannot touch user A's items or use A's undo token.
func TestFileOpsUserIsolation(t *testing.T) {
	env := newAuthEnv(t)
	ckA, hdrA := env.newUser(t, "alice", readyPassword)
	ckB, hdrB := env.newUser(t, "bob", readyPassword)
	homeA := filepath.Join(env.cfg.StorageRoot, "alice")
	seedFiles(t, homeA, "secret.txt", "dir/")
	ip := "203.0.113.9:1"

	r := env.call(http.MethodPost, "/api/v1/files/rename", ip, map[string]any{"path": "/secret.txt", "newName": "x.txt"}, hdrB, ckB)
	if r.rec.Code != 404 {
		t.Fatalf("bob rename alice's file: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/files/delete", ip, map[string]any{"paths": []string{"/secret.txt"}}, hdrB, ckB)
	if r.rec.Code != 404 {
		t.Fatalf("bob delete: %d", r.rec.Code)
	}
	r = env.call(http.MethodPost, "/api/v1/files/rename", ip, map[string]any{"path": "/../alice/secret.txt", "newName": "x.txt"}, hdrB, ckB)
	if r.rec.Code != 400 || r.code(t) != "PATH_INVALID" {
		t.Fatalf("bob traversal: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/files/rename", ip, map[string]any{"path": "/secret.txt", "newName": "x.txt"}, hdrA, ckA)
	if r.rec.Code != 200 {
		t.Fatalf("alice rename: %d %s", r.rec.Code, r.rec.Body.String())
	}
	token := r.body["undoToken"].(string)
	r = env.call(http.MethodPost, "/api/v1/files/undo", ip, map[string]any{"token": token}, hdrB, ckB)
	if r.rec.Code != 404 {
		t.Fatalf("bob undo alice's token: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// the token is burned, alice cannot use it either (single use, wrong user)
	r = env.call(http.MethodPost, "/api/v1/files/undo", ip, map[string]any{"token": token}, hdrA, ckA)
	if r.rec.Code != 404 {
		t.Fatalf("burned token: %d", r.rec.Code)
	}
	if _, err := os.Stat(filepath.Join(homeA, "x.txt")); err != nil {
		t.Fatal("alice's file must still be x.txt")
	}
	// trash is per user: bob's delete lands in bob's trash dir only
	seedFiles(t, filepath.Join(env.cfg.StorageRoot, "bob"), "mine.txt")
	r = env.call(http.MethodPost, "/api/v1/files/delete", ip, map[string]any{"paths": []string{"/mine.txt"}}, hdrB, ckB)
	if r.rec.Code != 200 {
		t.Fatalf("bob delete own: %d %s", r.rec.Code, r.rec.Body.String())
	}
	alice, _ := env.store.GetUserByUsername(context.Background(), "alice")
	if rows, _ := env.store.ListTrashItems(context.Background(), alice.ID); len(rows) != 0 {
		t.Fatalf("alice's trash must be empty: %+v", rows)
	}
}

func TestFileOpsTooLargeNeedsTaskEngine(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	seedFiles(t, home, "big/", "dest/")
	big := filepath.Join(home, "big", "blob.bin")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(201 << 20); err != nil { // sparse: no real disk use
		t.Fatal(err)
	}
	_ = f.Close()
	r := env.call(http.MethodPost, "/api/v1/files/copy", "203.0.113.5:1", map[string]any{"paths": []string{"/big"}, "to": "/dest"}, hdr, ck)
	if r.rec.Code != 413 || r.code(t) != "PAYLOAD_TOO_LARGE" || details(r)["reason"] != "task_required" {
		t.Fatalf("too large: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(home, "dest", "big")); err == nil {
		t.Fatal("nothing must be copied")
	}
}
