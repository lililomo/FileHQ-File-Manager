package httpapi

import (
	"archive/zip"
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func writeHome(t *testing.T, env *authEnv, username, rel, content string) {
	t.Helper()
	p := filepath.Join(env.cfg.StorageRoot, username, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

func taskID(t *testing.T, r resp) string {
	t.Helper()
	task, _ := r.body["task"].(map[string]any)
	id, _ := task["id"].(string)
	if id == "" {
		t.Fatalf("no task in %d %s", r.rec.Code, r.rec.Body.String())
	}
	return id
}

// TestArchivesHTTP covers the API side of PROMPT Step 14a: capabilities,
// compress → 202 task → archive exists, extract with the zip-slip fixture →
// task failed ARCHIVE_UNSAFE with a reason, validation (format, level,
// password on tar, single-file formats, names), password required/wrong,
// IDOR (user B cannot compress or extract A's paths), gates (401/403).
func TestArchivesHTTP(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	ip := "203.0.113.5:1"
	r := env.call(http.MethodGet, "/api/v1/archives/capabilities", ip, nil, nil, ck)
	if r.rec.Code != 200 {
		t.Fatalf("capabilities: %d %s", r.rec.Code, r.rec.Body.String())
	}
	formats, _ := r.body["formats"].([]any)
	if len(formats) < 10 || r.body["passwordStdin"] != true {
		t.Fatalf("capabilities body: %s", r.rec.Body.String())
	}
	if r := env.call(http.MethodGet, "/api/v1/archives/capabilities", ip, nil, nil, nil); r.rec.Code != 401 {
		t.Fatalf("capabilities without session: %d", r.rec.Code)
	}
	writeHome(t, env, "admin", "proyek/a.txt", "AAA")
	writeHome(t, env, "admin", "proyek/sub/b.txt", "BBB")
	// gates
	if r := env.call(http.MethodPost, "/api/v1/archives/compress", ip, map[string]any{"paths": []string{"/proyek"}, "format": "zip"}, nil, ck); r.rec.Code != 403 || r.code(t) != "CSRF_INVALID" {
		t.Fatalf("no csrf: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// compress → task → archive
	r = env.call(http.MethodPost, "/api/v1/archives/compress", ip, map[string]any{"paths": []string{"/proyek"}, "format": "zip", "level": "max", "destPath": "/", "deleteSource": false, "splitVolumeMb": 0}, hdr, ck)
	if r.rec.Code != 202 {
		t.Fatalf("compress: %d %s", r.rec.Code, r.rec.Body.String())
	}
	task := waitTask(t, env, ck, hdr, taskID(t, r), "done")
	res, _ := task["result"].(map[string]any)
	if res["path"] != "/proyek.zip" {
		t.Fatalf("compress result: %v", res)
	}
	if _, err := os.Stat(filepath.Join(env.cfg.StorageRoot, "admin", "proyek.zip")); err != nil {
		t.Fatal(err)
	}
	// validation
	bad := []map[string]any{
		{"paths": []string{"/proyek"}, "format": "arj"},
		{"paths": []string{"/proyek"}, "format": "zip", "level": "ultra"},
		{"paths": []string{"/proyek"}, "format": "tar", "password": "x"},
		{"paths": []string{"/proyek"}, "format": "gz"},
		{"paths": []string{"/proyek/a.txt", "/proyek/sub/b.txt"}, "format": "gz"},
		{"paths": []string{"/proyek"}, "format": "zip", "name": "../x.zip"},
		{"paths": []string{"/proyek"}, "format": "zip", "encrypt_names": true},
		{"paths": []string{}, "format": "zip"},
		{"paths": []string{"/proyek"}, "format": "zip", "dest_path": "/proyek/sub"},
	}
	for i, body := range bad {
		if r := env.call(http.MethodPost, "/api/v1/archives/compress", ip, body, hdr, ck); r.rec.Code != 400 {
			t.Fatalf("bad request %d: %d %s", i, r.rec.Code, r.rec.Body.String())
		}
	}
	if r := env.call(http.MethodPost, "/api/v1/archives/compress", ip, map[string]any{"paths": []string{"/nope"}, "format": "zip"}, hdr, ck); r.rec.Code != 404 {
		t.Fatalf("missing source: %d", r.rec.Code)
	}
	if r := env.call(http.MethodPost, "/api/v1/archives/compress", ip, map[string]any{"paths": []string{"/proyek"}, "format": "rar"}, hdr, ck); r.rec.Code != 415 {
		t.Fatalf("rar write: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// extract: zip-slip fixture fails the task with ARCHIVE_UNSAFE
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	w, _ := zw.Create("../../evil.txt")
	_, _ = w.Write([]byte("x"))
	_ = zw.Close()
	writeHome(t, env, "admin", "slip.zip", buf.String())
	r = env.call(http.MethodPost, "/api/v1/archives/extract", ip, map[string]any{"path": "/slip.zip", "dest_path": "new"}, hdr, ck)
	if r.rec.Code != 202 {
		t.Fatalf("extract: %d %s", r.rec.Code, r.rec.Body.String())
	}
	task = waitTask(t, env, ck, hdr, taskID(t, r), "failed")
	terr, _ := task["error"].(map[string]any)
	if terr["code"] != "ARCHIVE_UNSAFE" {
		t.Fatalf("slip task error: %v", terr)
	}
	if _, err := os.Stat(filepath.Join(env.cfg.StorageRoot, "evil.txt")); err == nil {
		t.Fatal("zip-slip escaped")
	}
	// extract the real archive next to it, then validation cases
	r = env.call(http.MethodPost, "/api/v1/archives/extract", ip, map[string]any{"archive": "/proyek.zip", "dest": "/keluar", "encoding": "auto", "onConflict": "rename"}, hdr, ck)
	if r.rec.Code != 202 {
		t.Fatalf("extract 2: %d %s", r.rec.Code, r.rec.Body.String())
	}
	waitTask(t, env, ck, hdr, taskID(t, r), "done")
	if b, err := os.ReadFile(filepath.Join(env.cfg.StorageRoot, "admin", "keluar", "proyek", "sub", "b.txt")); err != nil || string(b) != "BBB" {
		t.Fatalf("extracted content: %q %v", b, err)
	}
	writeHome(t, env, "admin", "text.txt", "just text")
	cases := map[string]map[string]any{
		"not archive":  {"path": "/text.txt"},
		"bad encoding": {"path": "/proyek.zip", "name_encoding": "ebcdic"},
		"bad policy":   {"path": "/proyek.zip", "on_conflict": "merge"},
		"bad entry":    {"path": "/proyek.zip", "entries": []string{"../x"}},
	}
	want := map[string]int{"not archive": 415, "bad encoding": 400, "bad policy": 400, "bad entry": 400}
	for name, body := range cases {
		if r := env.call(http.MethodPost, "/api/v1/archives/extract", ip, body, hdr, ck); r.rec.Code != want[name] {
			t.Fatalf("%s: %d %s", name, r.rec.Code, r.rec.Body.String())
		}
	}
	if r := env.call(http.MethodPost, "/api/v1/archives/extract", ip, map[string]any{"path": "/missing.zip"}, hdr, ck); r.rec.Code != 404 {
		t.Fatalf("missing archive: %d", r.rec.Code)
	}
	// password: compress with one, extract without → task PASSWORD_REQUIRED; wrong → PASSWORD_WRONG
	r = env.call(http.MethodPost, "/api/v1/archives/compress", ip, map[string]any{"paths": []string{"/proyek"}, "format": "zip", "name": "rahasia", "password": "p4ss"}, hdr, ck)
	if r.rec.Code != 202 {
		t.Fatalf("compress pw: %d %s", r.rec.Code, r.rec.Body.String())
	}
	waitTask(t, env, ck, hdr, taskID(t, r), "done")
	for pw, code := range map[string]string{"": "ARCHIVE_PASSWORD_REQUIRED", "wrong": "ARCHIVE_PASSWORD_WRONG"} {
		body := map[string]any{"path": "/rahasia.zip", "dest": "/pw-" + code}
		if pw != "" {
			body["password"] = pw
		}
		r = env.call(http.MethodPost, "/api/v1/archives/extract", ip, body, hdr, ck)
		task = waitTask(t, env, ck, hdr, taskID(t, r), "failed")
		terr, _ = task["error"].(map[string]any)
		if terr["code"] != code {
			t.Fatalf("password %q: %v", pw, terr)
		}
	}
	if env.logs.String() != "" && bytes.Contains(env.logs.Bytes(), []byte("p4ss")) {
		t.Fatal("password leaked into logs")
	}
	// retry of the password task is refused (secret gone)
	r = env.call(http.MethodPost, "/api/v1/tasks/"+task["id"].(string)+"/retry", ip, nil, hdr, ck)
	if r.rec.Code != 202 && r.rec.Code != 200 {
		t.Fatalf("retry: %d %s", r.rec.Code, r.rec.Body.String())
	}
	retried := waitTask(t, env, ck, hdr, taskID(t, r), "failed")
	if e, _ := retried["error"].(map[string]any); e["code"] != "ARCHIVE_PASSWORD_REQUIRED" {
		t.Fatalf("retry without secret: %v", e)
	}
	// IDOR: user B cannot reach A's paths (they resolve inside B's own root → 404)
	ckB, hdrB := env.newUser(t, "budi", "Budi-passw0rd-2026!")
	if r := env.call(http.MethodPost, "/api/v1/archives/compress", ip, map[string]any{"paths": []string{"/proyek"}, "format": "zip"}, hdrB, ckB); r.rec.Code != 404 {
		t.Fatalf("idor compress: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := env.call(http.MethodPost, "/api/v1/archives/extract", ip, map[string]any{"path": "/proyek.zip"}, hdrB, ckB); r.rec.Code != 404 {
		t.Fatalf("idor extract: %d %s", r.rec.Code, r.rec.Body.String())
	}
}
