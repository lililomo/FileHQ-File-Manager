package httpapi

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func zipBytes(t *testing.T, members map[string]string) string {
	t.Helper()
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	for name, body := range members {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

// TestArchiveBrowserHTTP covers PROMPT Step 14b through the API: listing
// one level with paging, entry-content streamed through a signed URL
// (inline policy, HTML forced to octet-stream, no tmp file), extract of
// selected entries, zip add/remove tasks, password handling (header only,
// never in URLs or logs, session id for entry-content), gates and IDOR.
func TestArchiveBrowserHTTP(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	ip := "203.0.113.5:1"
	members := map[string]string{"readme.txt": "hello from the archive", "docs/guide.txt": "GUIDE", "docs/img/pic.jpg": string(jpegBytes), "docs/page.html": "<html><script>1</script></html>", "src/main.go": "package main"}
	writeHome(t, env, "admin", "pack.zip", zipBytes(t, members))
	// listing: root, then a sub folder, paging, unknown folder
	r := env.call(http.MethodGet, "/api/v1/archives/entries?path=/pack.zip", ip, nil, nil, ck)
	if r.rec.Code != 200 {
		t.Fatalf("entries: %d %s", r.rec.Code, r.rec.Body.String())
	}
	items, _ := r.body["items"].([]any)
	if len(items) != 3 || r.body["format"] != "zip" || r.body["supportsModify"] != true || r.body["totalEntries"] != float64(8) {
		t.Fatalf("root listing: %s", r.rec.Body.String())
	}
	first, _ := items[0].(map[string]any)
	if first["name"] != "docs" || first["isDir"] != true || first["itemCount"] != float64(3) {
		t.Fatalf("first item: %v", first)
	}
	r = env.call(http.MethodGet, "/api/v1/archives/entries?path=/pack.zip&inner=docs&limit=2", ip, nil, nil, ck)
	items, _ = r.body["items"].([]any)
	if r.rec.Code != 200 || len(items) != 2 || r.body["nextCursor"] != "o2" || r.body["total"] != float64(3) {
		t.Fatalf("docs page 1: %s", r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/archives/entries?path=/pack.zip&inner=docs&limit=2&cursor=o2", ip, nil, nil, ck)
	items, _ = r.body["items"].([]any)
	if r.rec.Code != 200 || len(items) != 1 || r.body["nextCursor"] != nil {
		t.Fatalf("docs page 2: %s", r.rec.Body.String())
	}
	if r := env.call(http.MethodGet, "/api/v1/archives/entries?path=/pack.zip&inner=nope", ip, nil, nil, ck); r.rec.Code != 404 {
		t.Fatalf("unknown folder: %d", r.rec.Code)
	}
	if r := env.call(http.MethodGet, "/api/v1/archives/entries?path=/pack.zip&password=x", ip, nil, nil, ck); r.rec.Code != 400 {
		t.Fatalf("password in query must be refused: %d", r.rec.Code)
	}
	if r := env.call(http.MethodGet, "/api/v1/archives/entries?path=/pack.zip", ip, nil, nil, nil); r.rec.Code != 401 {
		t.Fatalf("no session: %d", r.rec.Code)
	}
	writeHome(t, env, "admin", "text.txt", "not an archive")
	if r := env.call(http.MethodGet, "/api/v1/archives/entries?path=/text.txt", ip, nil, nil, ck); r.rec.Code != 415 {
		t.Fatalf("not an archive: %d", r.rec.Code)
	}
	// entry-content through a signed URL (no cookie), inline policy like /raw
	sign := func(inner string, extra map[string]any) string {
		body := map[string]any{"path": "/pack.zip", "inner": inner}
		for k, v := range extra {
			body[k] = v
		}
		r := env.call(http.MethodPost, "/api/v1/archives/sign", ip, body, hdr, ck)
		if r.rec.Code != 200 {
			t.Fatalf("sign %s: %d %s", inner, r.rec.Code, r.rec.Body.String())
		}
		u, _ := r.body["url"].(string)
		return u
	}
	u := sign("readme.txt", nil)
	if !strings.HasPrefix(u, "/api/v1/archives/entry-content?") || !strings.Contains(u, "purpose=entry") {
		t.Fatalf("entry url: %s", u)
	}
	r = env.call(http.MethodGet, u, ip, nil, nil, nil)
	if r.rec.Code != 200 || r.rec.Body.String() != members["readme.txt"] {
		t.Fatalf("entry-content: %d %q", r.rec.Code, r.rec.Body.String())
	}
	h := r.rec.Header()
	if !strings.HasPrefix(h.Get("Content-Type"), "text/plain") || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment;") || h.Get("Content-Security-Policy") == "" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Content-Length") != "22" {
		t.Fatalf("text headers: %v", h)
	}
	r = env.call(http.MethodGet, sign("docs/img/pic.jpg", nil), ip, nil, nil, nil)
	if r.rec.Code != 200 || r.rec.Header().Get("Content-Type") != "image/jpeg" || !strings.HasPrefix(r.rec.Header().Get("Content-Disposition"), "inline;") {
		t.Fatalf("jpeg inline: %d %v", r.rec.Code, r.rec.Header())
	}
	r = env.call(http.MethodGet, sign("docs/page.html", nil), ip, nil, nil, nil)
	if r.rec.Code != 200 || r.rec.Header().Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(r.rec.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("html forced to attachment: %d %v", r.rec.Code, r.rec.Header())
	}
	r = env.call(http.MethodHead, sign("docs/guide.txt", nil), ip, nil, nil, nil)
	if r.rec.Code != 200 || r.rec.Body.Len() != 0 || r.rec.Header().Get("Content-Length") != "5" {
		t.Fatalf("HEAD: %d %d %v", r.rec.Code, r.rec.Body.Len(), r.rec.Header())
	}
	if r := env.call(http.MethodGet, sign("docs", nil), ip, nil, nil, nil); r.rec.Code != 404 {
		t.Fatalf("folder as entry: %d", r.rec.Code)
	}
	if r := env.call(http.MethodGet, strings.Replace(u, "readme.txt", "docs%2Fguide.txt", 1), ip, nil, nil, nil); r.rec.Code != 403 || r.code(t) != "SIGNATURE_INVALID" {
		t.Fatalf("tampered inner: %d %s", r.rec.Code, r.code(t))
	}
	if r := env.call(http.MethodPost, "/api/v1/files/sign", ip, map[string]any{"path": "/pack.zip", "purpose": "entry"}, hdr, ck); r.rec.Code != 400 {
		t.Fatalf("files/sign must refuse purpose entry: %d", r.rec.Code)
	}
	// nothing was extracted to serve the previews
	tmp, _ := os.ReadDir(filepath.Join(env.cfg.DataDir, "tmp"))
	for _, d := range tmp {
		if strings.HasPrefix(d.Name(), "ar-") {
			t.Fatalf("preview left %s in DATA_DIR/tmp", d.Name())
		}
	}
	// extract selected entries (a folder and a file) into a new folder
	r = env.call(http.MethodPost, "/api/v1/archives/extract-entries", ip, map[string]any{"path": "/pack.zip", "entries": []string{"docs/img", "readme.txt"}, "destPath": "/partial", "onConflict": "rename"}, hdr, ck)
	if r.rec.Code != 202 {
		t.Fatalf("extract-entries: %d %s", r.rec.Code, r.rec.Body.String())
	}
	task := waitTask(t, env, ck, hdr, taskID(t, r), "done")
	res, _ := task["result"].(map[string]any)
	if res["files"] != float64(2) {
		t.Fatalf("partial extract result: %v", res)
	}
	for _, p := range []string{"partial/readme.txt", "partial/docs/img/pic.jpg"} {
		if _, err := os.Stat(filepath.Join(env.cfg.StorageRoot, "admin", filepath.FromSlash(p))); err != nil {
			t.Fatalf("%s missing", p)
		}
	}
	if _, err := os.Stat(filepath.Join(env.cfg.StorageRoot, "admin", "partial", "docs", "guide.txt")); err == nil {
		t.Fatal("guide.txt extracted although not selected")
	}
	if r := env.call(http.MethodPost, "/api/v1/archives/extract-entries", ip, map[string]any{"path": "/pack.zip", "entries": []string{"ghost.txt"}}, hdr, ck); r.rec.Code != 404 {
		t.Fatalf("unknown entry: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := env.call(http.MethodPost, "/api/v1/archives/extract-entries", ip, map[string]any{"path": "/pack.zip", "entries": []string{}}, hdr, ck); r.rec.Code != 400 {
		t.Fatalf("no entries: %d", r.rec.Code)
	}
	// zip add + remove as tasks
	writeHome(t, env, "admin", "extra/new.txt", "NEW")
	r = env.call(http.MethodPost, "/api/v1/archives/add", ip, map[string]any{"path": "/pack.zip", "sources": []string{"/extra/new.txt"}, "innerDir": "docs"}, hdr, ck)
	if r.rec.Code != 202 {
		t.Fatalf("add: %d %s", r.rec.Code, r.rec.Body.String())
	}
	task = waitTask(t, env, ck, hdr, taskID(t, r), "done")
	if res, _ := task["result"].(map[string]any); res["added"] != float64(1) {
		t.Fatalf("add result: %v", res)
	}
	r = env.call(http.MethodGet, "/api/v1/archives/entries?path=/pack.zip&inner=docs", ip, nil, nil, ck)
	if r.rec.Code != 200 || r.body["total"] != float64(4) {
		t.Fatalf("listing after add: %s", r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/archives/remove-entry", ip, map[string]any{"path": "/pack.zip", "entries": []string{"src", "docs/new.txt"}}, hdr, ck)
	if r.rec.Code != 202 {
		t.Fatalf("remove: %d %s", r.rec.Code, r.rec.Body.String())
	}
	task = waitTask(t, env, ck, hdr, taskID(t, r), "done")
	if res, _ := task["result"].(map[string]any); res["removed"] != float64(2) {
		t.Fatalf("remove result: %v", res)
	}
	r = env.call(http.MethodGet, "/api/v1/archives/entries?path=/pack.zip", ip, nil, nil, ck)
	if r.rec.Code != 200 || r.body["totalEntries"] != float64(6) {
		t.Fatalf("listing after remove: %s", r.rec.Body.String())
	}
	writeHome(t, env, "admin", "t.tar", string(tarOf(t, "x.txt", "X")))
	if r := env.call(http.MethodPost, "/api/v1/archives/add", ip, map[string]any{"path": "/t.tar", "sources": []string{"/extra/new.txt"}}, hdr, ck); r.rec.Code != 415 {
		t.Fatalf("add to tar: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// gates: CSRF on the mutating routes
	for _, p := range []string{"/api/v1/archives/add", "/api/v1/archives/remove-entries", "/api/v1/archives/extract-entries", "/api/v1/archives/sign"} {
		if r := env.call(http.MethodPost, p, ip, map[string]any{"path": "/pack.zip", "entries": []string{"x"}, "inner": "x"}, nil, ck); r.rec.Code != 403 || r.code(t) != "CSRF_INVALID" {
			t.Fatalf("%s without csrf: %d %s", p, r.rec.Code, r.code(t))
		}
	}
	// IDOR: user B cannot list, sign, edit or extract A's archive (resolves in B's root → 404)
	ckB, hdrB := env.newUser(t, "budi", "Budi-passw0rd-2026!")
	if r := env.call(http.MethodGet, "/api/v1/archives/entries?path=/pack.zip", ip, nil, nil, ckB); r.rec.Code != 404 {
		t.Fatalf("idor entries: %d", r.rec.Code)
	}
	idor := map[string]map[string]any{
		"/api/v1/archives/add":             {"path": "/pack.zip", "sources": []string{"/extra/new.txt"}},
		"/api/v1/archives/remove-entries":  {"path": "/pack.zip", "entries": []string{"readme.txt"}},
		"/api/v1/archives/extract-entries": {"path": "/pack.zip", "entries": []string{"readme.txt"}},
	}
	for p, body := range idor {
		if r := env.call(http.MethodPost, p, ip, body, hdrB, ckB); r.rec.Code != 404 {
			t.Fatalf("idor %s: %d %s", p, r.rec.Code, r.rec.Body.String())
		}
	}
	// a URL signed for A, used by anyone, still serves A's file (bearer) but B's own signature for the same path hits B's root
	rb := env.call(http.MethodPost, "/api/v1/archives/sign", ip, map[string]any{"path": "/pack.zip", "inner": "readme.txt"}, hdrB, ckB)
	ub, _ := rb.body["url"].(string)
	if r := env.call(http.MethodGet, ub, ip, nil, nil, nil); r.rec.Code != 404 {
		t.Fatalf("B's signed url on A's path: %d", r.rec.Code)
	}
}

func tarOf(t *testing.T, name, body string) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}
	_, _ = tw.Write([]byte(body))
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// TestArchiveBrowserPasswordNeverLeaks: an encrypted zip lists with its
// members flagged; a wrong password is 422, the right one (header) yields a
// session id that the signed entry-content URL carries instead of the
// password; without it the entry answers 401 ARCHIVE_PASSWORD_REQUIRED;
// the password appears nowhere in logs, URLs or task payloads.
func TestArchiveBrowserPasswordNeverLeaks(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	ip := "203.0.113.5:1"
	const pw = "Rahasia-Arsip-77"
	writeHome(t, env, "admin", "src/secret.txt", "classified")
	r := env.call(http.MethodPost, "/api/v1/archives/compress", ip, map[string]any{"paths": []string{"/src"}, "format": "zip", "name": "locked", "password": pw, "destPath": "/"}, hdr, ck)
	if r.rec.Code != 202 {
		t.Fatalf("compress: %d %s", r.rec.Code, r.rec.Body.String())
	}
	waitTask(t, env, ck, hdr, taskID(t, r), "done")
	r = env.call(http.MethodGet, "/api/v1/archives/entries?path=/locked.zip&inner=src", ip, nil, nil, ck)
	if r.rec.Code != 200 || r.body["encrypted"] != true || r.body["session"] != nil || r.body["supportsModify"] != false {
		t.Fatalf("encrypted listing: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/archives/entries?path=/locked.zip", ip, nil, map[string]string{ArchivePasswordHeader: "wrong"}, ck)
	if r.rec.Code != 422 || r.code(t) != "ARCHIVE_PASSWORD_WRONG" {
		t.Fatalf("wrong password: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/archives/entries?path=/locked.zip", ip, nil, map[string]string{ArchivePasswordHeader: pw}, ck)
	sess, _ := r.body["session"].(string)
	if r.rec.Code != 200 || len(sess) != 32 {
		t.Fatalf("right password: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// entry without a session → 401 ARCHIVE_PASSWORD_REQUIRED; with it → content
	r = env.call(http.MethodPost, "/api/v1/archives/sign", ip, map[string]any{"path": "/locked.zip", "inner": "src/secret.txt"}, hdr, ck)
	plain, _ := r.body["url"].(string)
	if r := env.call(http.MethodGet, plain, ip, nil, nil, nil); r.rec.Code != 401 || r.code(t) != "ARCHIVE_PASSWORD_REQUIRED" {
		t.Fatalf("entry without session: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/archives/sign", ip, map[string]any{"path": "/locked.zip", "inner": "src/secret.txt", "session": sess}, hdr, ck)
	withSess, _ := r.body["url"].(string)
	if r.rec.Code != 200 || !strings.Contains(withSess, "sess="+sess) || strings.Contains(withSess, pw) {
		t.Fatalf("sign with session: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if _, err := url.Parse(withSess); err != nil {
		t.Fatal(err)
	}
	r = env.call(http.MethodGet, withSess, ip, nil, nil, nil)
	if r.rec.Code != 200 || r.rec.Body.String() != "classified" {
		t.Fatalf("entry with session: %d %q", r.rec.Code, r.rec.Body.String())
	}
	if r := env.call(http.MethodPost, "/api/v1/archives/sign", ip, map[string]any{"path": "/locked.zip", "inner": "src/secret.txt", "session": strings.Repeat("0", 32)}, hdr, ck); r.rec.Code != 404 {
		t.Fatalf("unknown session: %d", r.rec.Code)
	}
	// a session of user B is useless for A's archive, and B cannot use A's session on B's own path
	ckB, hdrB := env.newUser(t, "sari", "Sari-passw0rd-2026!")
	if r := env.call(http.MethodPost, "/api/v1/archives/sign", ip, map[string]any{"path": "/locked.zip", "inner": "src/secret.txt", "session": sess}, hdrB, ckB); r.rec.Code != 404 {
		t.Fatalf("session across users: %d", r.rec.Code)
	}
	if r := env.call(http.MethodGet, strings.Replace(withSess, "sess="+sess, "sess="+strings.Repeat("f", 32), 1), ip, nil, nil, nil); r.rec.Code != 404 {
		t.Fatalf("forged session id: %d", r.rec.Code)
	}
	// extract-entries with the password in the body; the task payload carries only hasPassword
	r = env.call(http.MethodPost, "/api/v1/archives/extract-entries", ip, map[string]any{"path": "/locked.zip", "entries": []string{"src/secret.txt"}, "destPath": "/out", "password": pw}, hdr, ck)
	if r.rec.Code != 202 {
		t.Fatalf("extract-entries: %d %s", r.rec.Code, r.rec.Body.String())
	}
	task := waitTask(t, env, ck, hdr, taskID(t, r), "done")
	if b, err := os.ReadFile(filepath.Join(env.cfg.StorageRoot, "admin", "out", "src", "secret.txt")); err != nil || string(b) != "classified" {
		t.Fatalf("extracted: %q %v", b, err)
	}
	payload, _ := task["payload"].(map[string]any)
	if payload["hasPassword"] != true || payload["password"] != nil {
		t.Fatalf("payload: %v", payload)
	}
	if strings.Contains(env.logs.String(), pw) {
		t.Fatal("password leaked into logs")
	}
	tl := env.call(http.MethodGet, "/api/v1/tasks/"+taskID(t, r)+"/logs", ip, nil, nil, ck)
	if strings.Contains(tl.rec.Body.String(), pw) {
		t.Fatal("password leaked into task logs")
	}
}
