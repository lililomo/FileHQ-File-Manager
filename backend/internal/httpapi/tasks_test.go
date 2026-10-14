package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/sse"
	"kppbwi.app/filemanager/internal/tasks"
)

func taskOf(t *testing.T, r resp) map[string]any {
	t.Helper()
	task, _ := r.body["task"].(map[string]any)
	if task == nil {
		t.Fatalf("no task in body: %s", r.rec.Body.String())
	}
	return task
}

func waitTask(t *testing.T, env *authEnv, ck *http.Cookie, hdr map[string]string, id string, want string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		r := env.call(http.MethodGet, "/api/v1/tasks/"+id, "203.0.113.5:1", nil, hdr, ck)
		if r.rec.Code != 200 {
			t.Fatalf("get task: %d %s", r.rec.Code, r.rec.Body.String())
		}
		task := taskOf(t, r)
		if task["state"] == want {
			return task
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("task %s did not reach %s", id, want)
	return nil
}

// TestTasksAC covers the HTTP side of PROMPT Step 11: large copy → 202
// task with progress, cancel cleans the destination and returns the quota,
// retry, IDOR, list/logs/delete, notifications.
func TestTasksAC(t *testing.T) {
	env := newAuthEnv(t, "TEST_SYNC_MAX_ENTRIES=5", "TEST_TASK_ITEM_DELAY_MS=40")
	ck, hdr := readyAdmin(t, env)
	ip := "203.0.113.5:1"
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	names := []string{"big/"}
	for i := 0; i < 40; i++ {
		names = append(names, fmt.Sprintf("big/f%02d.txt", i))
	}
	names = append(names, "dest/", "small.txt")
	seedFiles(t, home, names...)
	ctx := context.Background()
	admin, _ := env.store.GetUserByUsername(ctx, "admin")
	usedBefore, _ := env.quota.Usage(ctx, admin.ID)

	// small copies stay synchronous (200)
	r := env.call(http.MethodPost, "/api/v1/files/copy", ip, map[string]any{"paths": []string{"/small.txt"}, "to": "/dest"}, hdr, ck)
	if r.rec.Code != 200 || r.body["task"] != nil {
		t.Fatalf("small copy: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// a folder beyond the synchronous limit → 202 {task}
	r = env.call(http.MethodPost, "/api/v1/files/copy", ip, map[string]any{"paths": []string{"/big"}, "to": "/dest"}, hdr, ck)
	if r.rec.Code != 202 {
		t.Fatalf("large copy: %d %s", r.rec.Code, r.rec.Body.String())
	}
	task := taskOf(t, r)
	id, _ := task["id"].(string)
	if task["type"] != "copy" || task["state"] != "queued" || id == "" || task["cancelable"] != true {
		t.Fatalf("task view: %v", task)
	}
	if _, has := task["userId"]; has {
		t.Fatal("userId leaked")
	}
	progress := task["progress"].(map[string]any)
	if progress["unit"] != "bytes" {
		t.Fatalf("unit: %v", progress)
	}
	// AC: cancel while running → cancelled, no partial destination, quota back
	waitTask(t, env, ck, hdr, id, "running")
	time.Sleep(120 * time.Millisecond) // a few items in
	r = env.call(http.MethodPost, "/api/v1/tasks/"+id+"/cancel", ip, nil, hdr, ck)
	if r.rec.Code != 200 || taskOf(t, r)["cancelRequested"] != true {
		t.Fatalf("cancel: %d %s", r.rec.Code, r.rec.Body.String())
	}
	cancelled := waitTask(t, env, ck, hdr, id, "cancelled")
	if cancelled["retryable"] != true || cancelled["cancelable"] != false {
		t.Fatalf("cancelled view: %v", cancelled)
	}
	if _, err := os.Stat(filepath.Join(home, "dest", "big")); !os.IsNotExist(err) {
		t.Fatalf("partial destination left behind: %v", err)
	}
	usedAfter, _ := env.quota.Usage(ctx, admin.ID)
	if usedAfter.Reserved != 0 || usedAfter.Used != usedBefore.Used+int64(len("small.txt")) {
		t.Fatalf("quota after cancel: before=%+v after=%+v", usedBefore, usedAfter)
	}
	r = env.call(http.MethodPost, "/api/v1/tasks/"+id+"/cancel", ip, nil, hdr, ck)
	if r.rec.Code != 409 || details(r)["reason"] != "task_final" {
		t.Fatalf("cancel final: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// AC: retry → new task runs to completion; destination complete
	r = env.call(http.MethodPost, "/api/v1/tasks/"+id+"/retry", ip, nil, hdr, ck)
	if r.rec.Code != 202 {
		t.Fatalf("retry: %d %s", r.rec.Code, r.rec.Body.String())
	}
	retry := taskOf(t, r)
	rid, _ := retry["id"].(string)
	if rid == "" || rid == id || retry["retryOf"] != id {
		t.Fatalf("retry view: %v", retry)
	}
	done := waitTask(t, env, ck, hdr, rid, "done")
	if p := done["progress"].(map[string]any); p["percent"] != float64(100) || p["total"].(float64) <= 0 {
		t.Fatalf("done progress: %v", p)
	}
	if res := done["result"].(map[string]any); res["copied"] != float64(1) || res["to"] != "/dest" {
		t.Fatalf("result: %v", res)
	}
	if n := strings.Count(listNames(t, env, ck, hdr, "/dest/big"), ","); n != 39 {
		t.Fatalf("copied entries: %d", n+1)
	}
	r = env.call(http.MethodGet, "/api/v1/tasks/"+rid+"/logs", ip, nil, hdr, ck)
	if r.rec.Code != 200 || len(r.body["items"].([]any)) == 0 {
		t.Fatalf("logs: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/tasks/"+rid+"/retry", ip, nil, hdr, ck)
	if r.rec.Code != 409 {
		t.Fatalf("retry done: %d", r.rec.Code)
	}
	// list newest first + state filter + validation
	r = env.call(http.MethodGet, "/api/v1/tasks", ip, nil, hdr, ck)
	items := r.body["items"].([]any)
	if r.rec.Code != 200 || len(items) != 2 || items[0].(map[string]any)["id"] != rid {
		t.Fatalf("list: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/tasks?state=cancelled", ip, nil, hdr, ck)
	if len(r.body["items"].([]any)) != 1 {
		t.Fatalf("filter: %s", r.rec.Body.String())
	}
	if r = env.call(http.MethodGet, "/api/v1/tasks?state=bogus", ip, nil, hdr, ck); r.rec.Code != 400 {
		t.Fatalf("bad state: %d", r.rec.Code)
	}

	// notifications: the two finished tasks (cancelled → none, done → one)
	r = env.call(http.MethodGet, "/api/v1/notifications?unread=1", ip, nil, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("notifications: %d %s", r.rec.Code, r.rec.Body.String())
	}
	notes := r.body["items"].([]any)
	if len(notes) != 1 || r.body["unread"] != float64(1) {
		t.Fatalf("notifications body: %s", r.rec.Body.String())
	}
	note := notes[0].(map[string]any)
	if note["type"] != "task_done" || note["link"] != "/files/dest" || note["readAt"] != nil {
		t.Fatalf("note: %v", note)
	}
	r = env.call(http.MethodPost, "/api/v1/notifications/read", ip, map[string]any{"ids": []string{note["id"].(string)}}, hdr, ck)
	if r.rec.Code != 200 || r.body["read"] != float64(1) || r.body["unread"] != float64(0) {
		t.Fatalf("mark read: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r = env.call(http.MethodPost, "/api/v1/notifications/read", ip, map[string]any{}, hdr, ck); r.rec.Code != 400 {
		t.Fatalf("read validation: %d", r.rec.Code)
	}

	// AC IDOR: user B sees nothing of A's tasks or notifications
	ckB, hdrB := env.newUser(t, "budi", "Budi-Passw0rd-Long")
	for _, probe := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/tasks/" + rid}, {http.MethodGet, "/api/v1/tasks/" + rid + "/logs"},
		{http.MethodPost, "/api/v1/tasks/" + rid + "/cancel"}, {http.MethodPost, "/api/v1/tasks/" + id + "/retry"},
		{http.MethodDelete, "/api/v1/tasks/" + rid}, {http.MethodDelete, "/api/v1/notifications/" + note["id"].(string)},
	} {
		r = env.call(probe.method, probe.path, "203.0.113.9:1", nil, hdrB, ckB)
		if r.rec.Code != 404 {
			t.Fatalf("IDOR %s %s: %d %s", probe.method, probe.path, r.rec.Code, r.rec.Body.String())
		}
	}
	r = env.call(http.MethodGet, "/api/v1/tasks", "203.0.113.9:1", nil, hdrB, ckB)
	if len(r.body["items"].([]any)) != 0 {
		t.Fatal("user B lists A's tasks")
	}
	r = env.call(http.MethodPost, "/api/v1/notifications/read", "203.0.113.9:1", map[string]any{"ids": []string{note["id"].(string)}}, hdrB, ckB)
	if r.rec.Code != 200 || r.body["read"] != float64(0) {
		t.Fatalf("cross-user read: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// delete: running → 409, final → 204
	r = env.call(http.MethodDelete, "/api/v1/tasks/"+rid, ip, nil, hdr, ck)
	if r.rec.Code != 204 {
		t.Fatalf("delete: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodDelete, "/api/v1/notifications/"+note["id"].(string), ip, nil, hdr, ck)
	if r.rec.Code != 204 {
		t.Fatalf("delete notification: %d", r.rec.Code)
	}
	// unauthenticated
	if r = env.call(http.MethodGet, "/api/v1/tasks", ip, nil, nil, nil); r.rec.Code != 401 {
		t.Fatalf("anon: %d", r.rec.Code)
	}
}

// TestChecksumTask: files above the synchronous limit are hashed as a task.
func TestChecksumTask(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	big := filepath.Join(home, "big.bin")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(101 << 20); err != nil { // sparse: no disk cost
		t.Fatal(err)
	}
	_ = f.Close()
	r := env.call(http.MethodPost, "/api/v1/files/checksum", "203.0.113.5:1", map[string]any{"path": "/big.bin", "algos": []string{"sha256"}}, hdr, ck)
	if r.rec.Code != 202 {
		t.Fatalf("checksum: %d %s", r.rec.Code, r.rec.Body.String())
	}
	task := taskOf(t, r)
	done := waitTask(t, env, ck, hdr, task["id"].(string), "done")
	res := done["result"].(map[string]any)
	// sha256 of 101 MiB of zeros
	if res["sha256"] == nil || len(res["sha256"].(string)) != 64 {
		t.Fatalf("result: %v", res)
	}
	if p := done["progress"].(map[string]any); p["total"] != float64(101<<20) || p["unit"] != "bytes" {
		t.Fatalf("progress: %v", p)
	}
	// the digest is now cached: a second request answers synchronously
	r = env.call(http.MethodPost, "/api/v1/files/checksum", "203.0.113.5:1", map[string]any{"path": "/big.bin", "algos": []string{"sha256"}}, hdr, ck)
	if r.rec.Code != 200 || r.body["sha256"] != res["sha256"] {
		t.Fatalf("cached checksum: %d %s", r.rec.Code, r.rec.Body.String())
	}
}

// readSSE collects frames until n events named in want arrived or timeout.
func readSSE(t *testing.T, rd *bufio.Reader, timeout time.Duration, stop func(name string, data map[string]any) bool) (frames []string, comments int) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var name, data, id string
	for time.Now().Before(deadline) {
		line, err := rd.ReadString('\n')
		if err != nil {
			t.Fatalf("read: %v (frames %v)", err, frames)
		}
		line = strings.TrimRight(line, "\n")
		switch {
		case line == "":
			if name != "" || data != "" {
				frames = append(frames, id+" "+name+" "+data)
				var obj map[string]any
				_ = json.Unmarshal([]byte(data), &obj)
				if stop(name, obj) {
					return frames, comments
				}
			}
			name, data, id = "", "", ""
		case strings.HasPrefix(line, ":"):
			comments++
			if stop(":", nil) {
				return frames, comments
			}
		case strings.HasPrefix(line, "event: "):
			name = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data += strings.TrimPrefix(line, "data: ")
		case strings.HasPrefix(line, "id: "):
			id = strings.TrimPrefix(line, "id: ")
		case strings.HasPrefix(line, "retry: "):
			frames = append(frames, line)
		}
	}
	t.Fatalf("timeout; frames %v", frames)
	return nil, 0
}

// TestEventsStream: AC heartbeat + task.progress, Last-Event-ID replay,
// resync, headers, authentication and the API timeout exemption.
func TestEventsStream(t *testing.T) {
	old := sse.Heartbeat
	sse.Heartbeat = 150 * time.Millisecond
	t.Cleanup(func() { sse.Heartbeat = old })
	env := newAuthEnv(t, "TEST_SYNC_MAX_ENTRIES=3", "TEST_TASK_ITEM_DELAY_MS=30")
	ck, hdr := readyAdmin(t, env)
	srv := httptest.NewServer(env.handler)
	defer srv.Close()
	client := &http.Client{Timeout: 20 * time.Second}
	open := func(lastID string) (*http.Response, *bufio.Reader) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/v1/events", nil)
		req.AddCookie(ck)
		if lastID != "" {
			req.Header.Set("Last-Event-ID", lastID)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res, bufio.NewReader(res.Body)
	}
	// anonymous → 401 JSON, not a stream
	anon, err := client.Get(srv.URL + "/api/v1/events")
	if err != nil {
		t.Fatal(err)
	}
	_ = anon.Body.Close()
	if anon.StatusCode != 401 {
		t.Fatalf("anon events: %d", anon.StatusCode)
	}
	res, rd := open("")
	defer res.Body.Close()
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") ||
		res.Header.Get("Cache-Control") != "no-cache" || res.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("headers: %d %v", res.StatusCode, res.Header)
	}
	// retry hint, then a heartbeat comment within the (shortened) interval
	frames, comments := readSSE(t, rd, 3*time.Second, func(name string, _ map[string]any) bool { return name == ":" })
	if comments != 1 || len(frames) == 0 || frames[0] != "retry: 3000" {
		t.Fatalf("initial frames: %v comments=%d", frames, comments)
	}
	// a large copy publishes task.created, task.progress and task.done
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	seedFiles(t, home, "src/", "src/a.txt", "src/b.txt", "src/c.txt", "src/d.txt", "dst/")
	r := env.call(http.MethodPost, "/api/v1/files/copy", "203.0.113.5:1", map[string]any{"paths": []string{"/src"}, "to": "/dst"}, hdr, ck)
	if r.rec.Code != 202 {
		t.Fatalf("copy: %d %s", r.rec.Code, r.rec.Body.String())
	}
	id := taskOf(t, r)["id"].(string)
	seen := map[string]int{}
	var lastID string
	frames, _ = readSSE(t, rd, 10*time.Second, func(name string, data map[string]any) bool {
		if name == ":" {
			return false
		}
		seen[name]++
		if name == "task.done" && data["id"] == id {
			return true
		}
		return false
	})
	if seen["task.created"] != 1 || seen["task.progress"] < 1 || seen["task.done"] != 1 || seen["file.changed"] < 1 || seen["quota.updated"] != 1 {
		t.Fatalf("events: %v (%v)", seen, frames)
	}
	for _, f := range frames {
		if strings.Contains(f, "task.created") {
			lastID = strings.SplitN(f, " ", 2)[0]
		}
	}
	// reconnect with the id of task.created → replay of everything after it
	res2, rd2 := open(lastID)
	defer res2.Body.Close()
	replayed := map[string]int{}
	readSSE(t, rd2, 3*time.Second, func(name string, _ map[string]any) bool {
		if name == ":" {
			return true
		}
		replayed[name]++
		return false
	})
	if replayed["task.created"] != 0 || replayed["task.done"] != 1 || replayed["resync"] != 0 || replayed["task.progress"] < 1 {
		t.Fatalf("replay: %v", replayed)
	}
	// unknown id → resync
	res3, rd3 := open("0-0")
	defer res3.Body.Close()
	got, _ := readSSE(t, rd3, 3*time.Second, func(name string, _ map[string]any) bool { return name == "resync" })
	if len(got) == 0 {
		t.Fatal("no resync")
	}
	// the broker holds three connections of this user
	if env.events.Clients() != 3 {
		t.Fatalf("clients: %d", env.events.Clients())
	}
	// an API request with the task engine's types registered
	if types := env.tasks.Types(); len(types) < 4 {
		t.Fatalf("types: %v", types)
	}
	_ = tasks.EventDone
}
