package httpapi

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

func dupWrite(t *testing.T, home, rel, content string) {
	t.Helper()
	p := filepath.Join(home, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

func dupGroups(t *testing.T, r resp) []map[string]any {
	t.Helper()
	raw, _ := r.body["groups"].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, g := range raw {
		out = append(out, g.(map[string]any))
	}
	return out
}

// TestDuplicatesHTTP covers Step 23c's endpoints: POST /me/duplicates
// (202, 409 already_running, validation, ?share= rejected), GET (404 before
// a scan / for another user / after 24 h, 200 with shared flags and pruning),
// POST /me/duplicates/symlink (server re-hash mismatch → 400, success,
// undo restores the originals).
func TestDuplicatesHTTP(t *testing.T) {
	env := newAuthEnv(t, "TEST_TASK_ITEM_DELAY_MS=40")
	ck, hdr := readyAdmin(t, env)
	ip := "203.0.113.5:1"
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	dupWrite(t, home, "a.txt", "dup!")
	dupWrite(t, home, "dir/b.txt", "dup!")
	dupWrite(t, home, "dir/c.txt", "dup!")
	dupWrite(t, home, "odd.txt", "odd!") // same size, other content
	for _, f := range []string{"u1.txt", "u2.txt", "u3.txt"} {
		dupWrite(t, home, f, "unique "+f)
	}
	createShare(t, env, ck, hdr, map[string]any{"path": "/dir"})

	if r := env.call(http.MethodGet, "/api/v1/me/duplicates", ip, nil, hdr, ck); r.rec.Code != 404 {
		t.Fatalf("GET before scan: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// validation and scope
	for _, c := range []struct {
		target string
		body   any
		code   int
		field  string
	}{
		{"/api/v1/me/duplicates?share=abc", map[string]any{}, 400, "share"},
		{"/api/v1/me/duplicates", map[string]any{"path": "/a.txt"}, 400, "path"},
		{"/api/v1/me/duplicates", map[string]any{"path": "/missing"}, 404, ""},
		{"/api/v1/me/duplicates", map[string]any{"path": "../x"}, 400, ""},
		{"/api/v1/me/duplicates", map[string]any{"bogus": 1}, 400, ""},
	} {
		r := env.call(http.MethodPost, c.target, ip, c.body, hdr, ck)
		if r.rec.Code != c.code {
			t.Errorf("%s %v: %d %s", c.target, c.body, r.rec.Code, r.rec.Body.String())
		}
	}
	for _, target := range []string{"/api/v1/me/duplicates?share=abc", "/api/v1/me/duplicates/symlink?share=abc"} {
		method := http.MethodPost
		if r := env.call(method, target, ip, map[string]any{"keep": "/a.txt", "paths": []string{"/dir/b.txt"}}, hdr, ck); r.rec.Code != 400 {
			t.Errorf("%s with scope: %d", target, r.rec.Code)
		}
	}
	if r := env.call(http.MethodGet, "/api/v1/me/duplicates?share=abc", ip, nil, hdr, ck); r.rec.Code != 400 {
		t.Errorf("GET with scope: %d", r.rec.Code)
	}

	// start + already running
	r := env.call(http.MethodPost, "/api/v1/me/duplicates", ip, map[string]any{}, hdr, ck)
	if r.rec.Code != 202 {
		t.Fatalf("start: %d %s", r.rec.Code, r.rec.Body.String())
	}
	id := taskID(t, r)
	r2 := env.call(http.MethodPost, "/api/v1/me/duplicates", ip, map[string]any{"path": "/dir"}, hdr, ck)
	if r2.rec.Code != 409 || errDetails(r2)["reason"] != "already_running" || errDetails(r2)["taskId"] != id {
		t.Fatalf("second start: %d %s", r2.rec.Code, r2.rec.Body.String())
	}
	task := waitTask(t, env, ck, hdr, id, "done")
	if task["type"] != "duplicate_finder" {
		t.Fatalf("task: %v", task)
	}

	r = env.call(http.MethodGet, "/api/v1/me/duplicates", ip, nil, hdr, ck)
	if r.rec.Code != 200 || r.body["taskId"] != id || r.body["path"] != "/" || r.body["reclaimable"] != float64(8) || r.body["scanned"] != float64(7) {
		t.Fatalf("GET: %d %s", r.rec.Code, r.rec.Body.String())
	}
	groups := dupGroups(t, r)
	if len(groups) != 1 {
		t.Fatalf("groups: %v", groups)
	}
	paths := groups[0]["paths"].([]any)
	shared := map[string]bool{}
	for _, p := range paths {
		m := p.(map[string]any)
		shared[m["path"].(string)] = m["shared"].(bool)
	}
	if len(shared) != 3 || !shared["/dir/b.txt"] || !shared["/dir/c.txt"] || shared["/a.txt"] {
		t.Fatalf("paths/shared: %v", shared)
	}

	// another user sees nothing
	ckB, hdrB := env.newUser(t, "budi", readyPassword)
	if r := env.call(http.MethodGet, "/api/v1/me/duplicates", ip, nil, hdrB, ckB); r.rec.Code != 404 {
		t.Fatalf("user B GET: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// user B cannot point the symlink endpoint at A's files (B's jail)
	if r := env.call(http.MethodPost, "/api/v1/me/duplicates/symlink", ip, map[string]any{"keep": "/a.txt", "paths": []string{"/dir/b.txt"}}, hdrB, ckB); r.rec.Code != 404 {
		t.Fatalf("user B symlink: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// symlink: validation, server-side re-hash
	for _, c := range []struct {
		body   map[string]any
		reason string
	}{
		{map[string]any{"keep": "/a.txt", "paths": []string{"/dir/b.txt", "/odd.txt"}}, "hash_mismatch"},
		{map[string]any{"keep": "/a.txt", "paths": []string{"/a.txt"}}, "contains_keep"},
		{map[string]any{"keep": "/a.txt", "paths": []string{"/u1.txt"}}, "size_mismatch"},
		{map[string]any{"keep": "/dir", "paths": []string{"/a.txt"}}, "not_a_file"},
		{map[string]any{"paths": []string{"/a.txt"}}, ""},
		{map[string]any{"keep": "/a.txt", "paths": []string{}}, ""},
	} {
		r := env.call(http.MethodPost, "/api/v1/me/duplicates/symlink", ip, c.body, hdr, ck)
		if r.rec.Code != 400 || (c.reason != "" && errDetails(r)["reason"] != c.reason) {
			t.Errorf("symlink %v: %d %s", c.body, r.rec.Code, r.rec.Body.String())
		}
	}
	if _, err := os.Lstat(filepath.Join(home, "dir", "b.txt")); err != nil {
		t.Fatal("refused request moved a file")
	}
	r = env.call(http.MethodPost, "/api/v1/me/duplicates/symlink", ip, map[string]any{"keep": "/a.txt", "paths": []string{"/dir/b.txt"}}, hdr, ck)
	token, _ := r.body["undoToken"].(string)
	if r.rec.Code != 200 || token == "" {
		t.Fatalf("symlink: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if target, err := os.Readlink(filepath.Join(home, "dir", "b.txt")); err != nil || target != "../a.txt" {
		t.Fatalf("link: %q %v", target, err)
	}
	// the stored result is pruned: /dir/b.txt is a symlink now
	r = env.call(http.MethodGet, "/api/v1/me/duplicates", ip, nil, hdr, ck)
	if groups := dupGroups(t, r); len(groups) != 1 || len(groups[0]["paths"].([]any)) != 2 || r.body["reclaimable"] != float64(4) {
		t.Fatalf("pruned GET: %s", r.rec.Body.String())
	}
	// undo removes the link and restores the original
	if r := env.call(http.MethodPost, "/api/v1/files/undo", ip, map[string]any{"token": token}, hdr, ck); r.rec.Code != 200 {
		t.Fatalf("undo: %d %s", r.rec.Code, r.rec.Body.String())
	}
	fi, err := os.Lstat(filepath.Join(home, "dir", "b.txt"))
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("after undo: %v %v", fi, err)
	}
	if b, _ := os.ReadFile(filepath.Join(home, "dir", "b.txt")); string(b) != "dup!" {
		t.Fatalf("after undo content %q", b)
	}

	// several groups in one request: one batch, one undo token
	r = env.call(http.MethodPost, "/api/v1/me/duplicates/symlink", ip, map[string]any{"groups": []map[string]any{
		{"keep": "/a.txt", "paths": []string{"/dir/b.txt"}}, {"keep": "/a.txt", "paths": []string{"/dir/b.txt"}}}}, hdr, ck)
	if r.rec.Code != 400 || errDetails(r)["reason"] != "duplicate_path" {
		t.Fatalf("groups duplicate path: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/me/duplicates/symlink", ip, map[string]any{"groups": []map[string]any{
		{"keep": "/a.txt", "paths": []string{"/dir/b.txt"}}, {"keep": "/a.txt", "paths": []string{"/dir/c.txt"}}}}, hdr, ck)
	token, _ = r.body["undoToken"].(string)
	if r.rec.Code != 200 || token == "" || len(r.body["results"].([]any)) != 2 {
		t.Fatalf("groups symlink: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := env.call(http.MethodPost, "/api/v1/files/undo", ip, map[string]any{"token": token}, hdr, ck); r.rec.Code != 200 {
		t.Fatalf("groups undo: %d %s", r.rec.Code, r.rec.Body.String())
	}
	for _, f := range []string{"b.txt", "c.txt"} {
		if fi, err := os.Lstat(filepath.Join(home, "dir", f)); err != nil || !fi.Mode().IsRegular() {
			t.Fatalf("%s after groups undo: %v %v", f, fi, err)
		}
	}

	// a scoped scan below a shared folder flags the copies (one query with
	// ancestors + prefix)
	r = env.call(http.MethodPost, "/api/v1/me/duplicates", ip, map[string]any{"path": "/dir"}, hdr, ck)
	if r.rec.Code != 202 {
		t.Fatalf("scoped start: %d %s", r.rec.Code, r.rec.Body.String())
	}
	waitTask(t, env, ck, hdr, taskID(t, r), "done")
	r = env.call(http.MethodGet, "/api/v1/me/duplicates", ip, nil, hdr, ck)
	if groups := dupGroups(t, r); r.body["path"] != "/dir" || len(groups) != 1 {
		t.Fatalf("scoped GET: %s", r.rec.Body.String())
	} else {
		for _, p := range groups[0]["paths"].([]any) {
			if m := p.(map[string]any); m["shared"] != true {
				t.Fatalf("scoped scan lost the shared flag: %v", m)
			}
		}
	}
	scopedID := r.body["taskId"].(string)

	// results older than 24 h are not served
	old := db.FormatTime(time.Now().Add(-25 * time.Hour))
	for _, tid := range []string{id, scopedID} {
		row, err := env.store.GetTask(context.Background(), tid)
		if err != nil {
			t.Fatal(err)
		}
		if err := env.store.SetTaskFinal(context.Background(), sqlc.SetTaskFinalParams{ID: tid, State: row.State, ResultJson: row.ResultJson,
			ProgressDone: row.ProgressDone, ProgressTotal: row.ProgressTotal, FinishedAt: &old, UpdatedAt: old}); err != nil {
			t.Fatal(err)
		}
	}
	if r := env.call(http.MethodGet, "/api/v1/me/duplicates", ip, nil, hdr, ck); r.rec.Code != 404 {
		t.Fatalf("GET after 24 h: %d %s", r.rec.Code, r.rec.Body.String())
	}
}

func errDetails(r resp) map[string]any {
	e, _ := r.body["error"].(map[string]any)
	d, _ := e["details"].(map[string]any)
	return d
}
