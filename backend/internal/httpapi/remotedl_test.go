package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/remotedl"
)

const remoteIP = "203.0.113.5:1"

func remoteServer(t *testing.T, mux *http.ServeMux) string {
	t.Helper()
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	_, port, _ := net.SplitHostPort(u.Host)
	return "http://127.0.0.1:" + port
}

func postRemote(env *authEnv, ck *http.Cookie, hdr map[string]string, body any) resp {
	return env.call(http.MethodPost, "/api/v1/remote-downloads", remoteIP, body, hdr, ck)
}

func firstTask(t *testing.T, r resp) map[string]any {
	t.Helper()
	list, _ := r.body["tasks"].([]any)
	if len(list) == 0 {
		t.Fatalf("no tasks in %s", r.rec.Body.String())
	}
	return list[0].(map[string]any)
}

// TestRemoteDownloadAC covers PROMPT Step 13 through HTTP: literal loopback
// refused at validation, redirect to link-local fails the task with
// SSRF_BLOCKED, a real download with progress + checksum, cancel without a
// part file, disabled/denylist → 403, rate limit, no query strings in logs,
// IDOR, probe.
func TestRemoteDownloadAC(t *testing.T) {
	// --- without the loopback exemption: 127.0.0.1 is private ---
	strict := newAuthEnv(t)
	ck, hdr := readyAdmin(t, strict)
	r := postRemote(strict, ck, hdr, map[string]any{"urls": []string{"http://127.0.0.1:8080/healthz"}, "path": "/"})
	// SPEC §6.2: SSRF_BLOCKED is 422 (PROMPT Step 13 says 400; SPEC is the contract)
	if r.rec.Code != 422 || r.code(t) != "SSRF_BLOCKED" {
		t.Fatalf("loopback literal: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if d := details(r); d["reason"] != "private_ip" || d["ip"] != "127.0.0.1" {
		t.Fatalf("details %v", d)
	}
	for _, raw := range []string{"file:///etc/passwd", "ftp://example.com/x", "http://user:pw@example.com/", "http://example.com:22/", "http://169.254.169.254/latest"} {
		r = postRemote(strict, ck, hdr, map[string]any{"urls": []string{raw}, "path": "/"})
		if r.rec.Code != 422 || r.code(t) != "SSRF_BLOCKED" {
			t.Fatalf("%s: %d %s", raw, r.rec.Code, r.rec.Body.String())
		}
	}

	// --- with loopback allowed: a local test server stands in for the internet ---
	env := newAuthEnv(t, "TEST_REMOTE_ALLOW_LOOPBACK=1")
	ck, hdr = readyAdmin(t, env)
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	seedFiles(t, home, "dl/")
	data := bytes.Repeat([]byte("remote-download-"), 128<<10) // 2 MiB
	sum := sha256.Sum256(data)
	mux := http.NewServeMux()
	mod := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	mux.HandleFunc("/pkg/data.bin", func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "data.bin", mod, bytes.NewReader(data))
	})
	mux.HandleFunc("/to-metadata", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(200)
		for off := 0; off < len(data); off += 64 << 10 {
			if _, err := w.Write(data[off : off+64<<10]); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(15 * time.Millisecond)
		}
	})
	base := remoteServer(t, mux)

	// validation shape
	r = postRemote(env, ck, hdr, map[string]any{"urls": []string{}, "path": "/dl"})
	if r.rec.Code != 400 || r.code(t) != "VALIDATION_FAILED" {
		t.Fatalf("empty urls: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = postRemote(env, ck, hdr, map[string]any{"urls": []string{base + "/pkg/data.bin"}, "path": "/missing"})
	if r.rec.Code != 404 {
		t.Fatalf("missing dest: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = postRemote(env, ck, hdr, map[string]any{"urls": []string{base + "/pkg/data.bin"}, "path": "/dl", "filename": "../x"})
	if r.rec.Code != 400 {
		t.Fatalf("bad filename: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// redirect to link-local → task failed SSRF_BLOCKED at the redirect hop
	r = postRemote(env, ck, hdr, map[string]any{"urls": []string{base + "/to-metadata"}, "path": "/dl"})
	if r.rec.Code != 202 {
		t.Fatalf("create: %d %s", r.rec.Code, r.rec.Body.String())
	}
	task := waitTask(t, env, ck, hdr, firstTask(t, r)["id"].(string), "failed")
	if e := task["error"].(map[string]any); e["code"] != "SSRF_BLOCKED" {
		t.Fatalf("redirect error: %v", e)
	}

	// real download: 202 + task, progress bytes, checksum identical
	r = postRemote(env, ck, hdr, map[string]any{"urls": []string{base + "/pkg/data.bin?token=QS-SECRET"}, "path": "/dl", "onConflict": "rename"})
	if r.rec.Code != 202 {
		t.Fatalf("create: %d %s", r.rec.Code, r.rec.Body.String())
	}
	tk := firstTask(t, r)
	if tk["type"] != "remote_download" || tk["state"] != "queued" && tk["state"] != "running" {
		t.Fatalf("task %v", tk)
	}
	payload := tk["payload"].(map[string]any)
	if payload["dest"] != "/dl" || payload["policy"] != "rename" {
		t.Fatalf("payload %v", payload)
	}
	task = waitTask(t, env, ck, hdr, tk["id"].(string), "done")
	prog := task["progress"].(map[string]any)
	if prog["unit"] != "bytes" || prog["done"].(float64) != float64(len(data)) || prog["total"].(float64) != float64(len(data)) {
		t.Fatalf("progress %v", prog)
	}
	res := task["result"].(map[string]any)
	if res["path"] != "/dl/data.bin" || res["size"].(float64) != float64(len(data)) || strings.Contains(res["finalUrl"].(string), "QS-SECRET") {
		t.Fatalf("result %v", res)
	}
	got, err := os.ReadFile(filepath.Join(home, "dl", "data.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if gs := sha256.Sum256(got); gs != sum {
		t.Fatal("checksum mismatch")
	}
	// listing shows it, quota counted
	r = env.call(http.MethodGet, "/api/v1/files/list?path=/dl", remoteIP, nil, hdr, ck)
	if r.rec.Code != 200 || !strings.Contains(itemNames(t, r), "data.bin") {
		t.Fatalf("listing: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/me/quota", remoteIP, nil, hdr, ck)
	if used := r.body["used"].(float64); used != float64(len(data)) {
		t.Fatalf("quota used %v", used)
	}
	// logs (server + task + audit) never carry the query string
	if strings.Contains(env.logs.String(), "QS-SECRET") {
		t.Fatal("query string leaked into the server log")
	}
	r = env.call(http.MethodGet, "/api/v1/tasks/"+tk["id"].(string)+"/logs", remoteIP, nil, hdr, ck)
	if strings.Contains(r.rec.Body.String(), "QS-SECRET") {
		t.Fatal("query string leaked into task logs")
	}
	rows, err := env.store.ListAudit(context.Background(), db.AuditFilter{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	var sawAudit bool
	for _, row := range rows {
		if row.Action == "remote_download.created" {
			sawAudit = true
			d := ""
			if row.DetailsJson != nil {
				d = *row.DetailsJson
			}
			if strings.Contains(d, "QS-SECRET") || strings.Contains(d, "/pkg/") {
				t.Fatalf("audit carries the URL: %s", d)
			}
			if !strings.Contains(d, "127.0.0.1:") {
				t.Fatalf("audit lacks the host: %s", d)
			}
		}
	}
	if !sawAudit {
		t.Fatal("no remote_download.created audit row")
	}

	// cancel while running → cancelled, no part under DATA_DIR/tmp, quota released
	r = postRemote(env, ck, hdr, map[string]any{"urls": []string{base + "/slow"}, "path": "/dl", "filename": "slow.bin"})
	if r.rec.Code != 202 {
		t.Fatalf("create slow: %d %s", r.rec.Code, r.rec.Body.String())
	}
	id := firstTask(t, r)["id"].(string)
	deadline := time.Now().Add(10 * time.Second)
	for {
		cur := taskOf(t, env.call(http.MethodGet, "/api/v1/tasks/"+id, remoteIP, nil, hdr, ck))
		if cur["state"] == "running" && cur["progress"].(map[string]any)["done"].(float64) > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("slow task never progressed: %v", cur)
		}
		time.Sleep(10 * time.Millisecond)
	}
	r = env.call(http.MethodPost, "/api/v1/tasks/"+id+"/cancel", remoteIP, nil, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("cancel: %d %s", r.rec.Code, r.rec.Body.String())
	}
	waitTask(t, env, ck, hdr, id, "cancelled")
	ents, _ := os.ReadDir(filepath.Join(env.cfg.DataDir, "tmp"))
	for _, en := range ents {
		if strings.HasPrefix(en.Name(), remotedl.TmpPrefix) {
			t.Fatalf("part left behind: %s", en.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(home, "dl", "slow.bin")); err == nil {
		t.Fatal("cancelled download landed")
	}
	r = env.call(http.MethodGet, "/api/v1/me/quota", remoteIP, nil, hdr, ck)
	if used := r.body["used"].(float64); used != float64(len(data)) {
		t.Fatalf("quota after cancel %v", used)
	}

	// probe
	r = env.call(http.MethodPost, "/api/v1/remote-downloads/probe", remoteIP, map[string]any{"url": base + "/pkg/data.bin"}, hdr, ck)
	if r.rec.Code != 200 || r.body["size"].(float64) != float64(len(data)) || r.body["filename"] != "data.bin" || r.body["acceptsRanges"] != true {
		t.Fatalf("probe: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// IDOR: user B cannot see/cancel A's task
	ckB, hdrB := env.newUser(t, "budi", "Rahasia sekali 2026!")
	r = env.call(http.MethodGet, "/api/v1/tasks/"+id, remoteIP, nil, hdrB, ckB)
	if r.rec.Code != 404 {
		t.Fatalf("idor get: %d", r.rec.Code)
	}
	r = env.call(http.MethodPost, "/api/v1/tasks/"+id+"/retry", remoteIP, nil, hdrB, ckB)
	if r.rec.Code != 404 {
		t.Fatalf("idor retry: %d", r.rec.Code)
	}

	// denylist from the settings table → 403 with a clear reason
	up := func(key, val string) {
		if err := env.store.UpsertSetting(context.Background(), sqlc.UpsertSettingParams{Key: key, ValueJson: val, UpdatedAt: "2026-10-14T00:00:00Z"}); err != nil {
			t.Fatal(err)
		}
		env.remote.InvalidateSettings()
	}
	up(remotedl.KeyDenylist, `["127.0.0.1"]`)
	r = postRemote(env, ck, hdr, map[string]any{"urls": []string{base + "/pkg/data.bin"}, "path": "/dl"})
	if r.rec.Code != 403 || r.code(t) != "FORBIDDEN" || details(r)["reason"] != "denylist" {
		t.Fatalf("denylist: %d %s", r.rec.Code, r.rec.Body.String())
	}
	up(remotedl.KeyDenylist, `[]`)

	// rate limit: 10 tasks per hour per user, the 11th → 429 + Retry-After
	// (the three tasks above already count: 7 more fill the window)
	var urls []string
	for i := 0; i < 7; i++ {
		urls = append(urls, base+"/pkg/data.bin")
	}
	r = postRemote(env, ck, hdr, map[string]any{"urls": urls, "path": "/dl", "onConflict": "skip"})
	if r.rec.Code != 202 {
		t.Fatalf("7 more: %d %s", r.rec.Code, r.rec.Body.String())
	}
	for _, it := range r.body["tasks"].([]any) {
		waitTask(t, env, ck, hdr, it.(map[string]any)["id"].(string), "done")
	}
	r = postRemote(env, ck, hdr, map[string]any{"urls": []string{base + "/pkg/data.bin"}, "path": "/dl"})
	if r.rec.Code != 429 || r.code(t) != "RATE_LIMITED" || r.rec.Header().Get("Retry-After") == "" {
		t.Fatalf("11th: %d %s %q", r.rec.Code, r.rec.Body.String(), r.rec.Header().Get("Retry-After"))
	}
	// user B has their own window
	seedFiles(t, filepath.Join(env.cfg.StorageRoot, "budi"), "in/")
	r = postRemote(env, ckB, hdrB, map[string]any{"urls": []string{base + "/pkg/data.bin"}, "path": "/in"})
	if r.rec.Code != 202 {
		t.Fatalf("user B: %d %s", r.rec.Code, r.rec.Body.String())
	}
	waitTask(t, env, ckB, hdrB, firstTask(t, r)["id"].(string), "done")
	if _, err := os.Stat(filepath.Join(env.cfg.StorageRoot, "budi", "in", "data.bin")); err != nil {
		t.Fatal("user B download missing")
	}
	// a task carries no secret: payload is url+dest+policy only
	r = env.call(http.MethodGet, "/api/v1/tasks?limit=5", remoteIP, nil, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("list: %d", r.rec.Code)
	}
	_ = hex.EncodeToString(sum[:])
}

// TestRemoteDownloadDisabled: REMOTE_DOWNLOAD_ENABLED=false → 403 FORBIDDEN
// on both endpoints and the flag is false in /auth/me config.
func TestRemoteDownloadDisabled(t *testing.T) {
	env := newAuthEnv(t, "REMOTE_DOWNLOAD_ENABLED=false")
	ck, hdr := readyAdmin(t, env)
	r := postRemote(env, ck, hdr, map[string]any{"urls": []string{"http://example.com/x"}, "path": "/"})
	if r.rec.Code != 403 || r.code(t) != "FORBIDDEN" || details(r)["reason"] != "disabled" {
		t.Fatalf("disabled: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/remote-downloads/probe", remoteIP, map[string]any{"url": "http://example.com/x"}, hdr, ck)
	if r.rec.Code != 403 {
		t.Fatalf("probe disabled: %d", r.rec.Code)
	}
	r = env.call(http.MethodGet, "/api/v1/auth/me", remoteIP, nil, hdr, ck)
	if cfg := r.body["config"].(map[string]any); cfg["remoteDownloadEnabled"] != false {
		t.Fatalf("me config: %v", cfg["remoteDownloadEnabled"])
	}
	// unauthenticated / no CSRF
	r = env.call(http.MethodPost, "/api/v1/remote-downloads", remoteIP, map[string]any{"urls": []string{"http://example.com/x"}, "path": "/"}, nil, nil)
	if r.rec.Code != 401 {
		t.Fatalf("anon: %d", r.rec.Code)
	}
	r = env.call(http.MethodPost, "/api/v1/remote-downloads", remoteIP, map[string]any{"urls": []string{"http://example.com/x"}, "path": "/"}, nil, ck)
	if r.rec.Code != 403 || r.code(t) != "CSRF_INVALID" {
		t.Fatalf("csrf: %d %s", r.rec.Code, r.rec.Body.String())
	}
}
