package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestSearchHTTP covers the API side of PROMPT Step 15: name search with
// location, traversal → 400, NDJSON content stream, replace preview/apply
// tasks, versions list/restore, history, IDOR (user B sees nothing of A),
// scope parameters refused, name rate limit.
func TestSearchHTTP(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	ip := "203.0.113.5:1"
	writeHome(t, env, "admin", "proj/a.txt", "TODO alpha\nplain\n")
	writeHome(t, env, "admin", "proj/b.md", "todo beta\n")
	writeHome(t, env, "admin", "proj/bin.dat", "TODO\x00bin\n")
	writeHome(t, env, "admin", "proj/sub/c.txt", "nothing\n")

	r := env.call(http.MethodGet, "/api/v1/search/names?q=.txt&path=/proj", ip, nil, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("names: %d %s", r.rec.Code, r.rec.Body.String())
	}
	items, _ := r.body["items"].([]any)
	if len(items) != 2 || r.body["truncated"] != false {
		t.Fatalf("names body %s", r.rec.Body.String())
	}
	first, _ := items[0].(map[string]any)
	if first["name"] != "a.txt" || first["location"] != "/proj" || first["category"] != "text" {
		t.Fatalf("first item %v", first)
	}
	for _, q := range []string{"q=a&path=../", "q=a&path=/..", "q=a&path=/.system"} {
		r = env.call(http.MethodGet, "/api/v1/search/names?"+q, ip, nil, hdr, ck)
		if r.rec.Code != 400 || r.code(t) != "PATH_INVALID" {
			t.Fatalf("%s: %d %s", q, r.rec.Code, r.rec.Body.String())
		}
	}
	r = env.call(http.MethodGet, "/api/v1/search/names?q=a&types=nope", ip, nil, hdr, ck)
	if r.rec.Code != 400 || r.code(t) != "VALIDATION_FAILED" {
		t.Fatalf("bad types: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/search/names?q=a&share=x", ip, nil, hdr, ck)
	if r.rec.Code != 400 {
		t.Fatalf("scope param: %d", r.rec.Code)
	}

	// content search: NDJSON stream, binary skipped
	r = env.call(http.MethodPost, "/api/v1/search/content", ip, map[string]any{"q": "todo", "path": "/proj", "recursive": true}, hdr, ck)
	if r.rec.Code != 200 || !strings.HasPrefix(r.rec.Header().Get("Content-Type"), "application/x-ndjson") {
		t.Fatalf("content: %d %q %s", r.rec.Code, r.rec.Header().Get("Content-Type"), r.rec.Body.String())
	}
	lines := strings.Split(strings.TrimSpace(r.rec.Body.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("ndjson lines: %v", lines)
	}
	// the Go engine emits files in completion order: check the set
	found := false
	for _, line := range lines[:2] {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("match line %s: %v", line, err)
		}
		if m["file"] == "/proj/a.txt" {
			found = true
			if m["line"] != float64(1) || m["col"] != float64(1) || m["after"] != "plain" || m["text"] != "TODO alpha" {
				t.Fatalf("a.txt match %s", line)
			}
		}
	}
	if !found {
		t.Fatalf("a.txt not in %v", lines)
	}
	var sum map[string]any
	if err := json.Unmarshal([]byte(lines[2]), &sum); err != nil || sum["done"] != true || sum["matches"] != float64(2) || sum["files"] != float64(2) || sum["engine"] != "go" {
		t.Fatalf("summary %s", lines[2])
	}
	r = env.call(http.MethodPost, "/api/v1/search/content", ip, map[string]any{"q": "(", "regex": true, "path": "/proj"}, hdr, ck)
	if r.rec.Code != 400 || r.code(t) != "VALIDATION_FAILED" {
		t.Fatalf("bad regex: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/search/content", ip, map[string]any{"q": "x", "path": "../"}, hdr, ck)
	if r.rec.Code != 400 || r.code(t) != "PATH_INVALID" {
		t.Fatalf("content traversal: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// replace preview → apply → versions → restore
	r = env.call(http.MethodPost, "/api/v1/search/replace/preview", ip, map[string]any{"q": "todo", "path": "/proj", "recursive": true, "replace": "DONE"}, hdr, ck)
	if r.rec.Code != 202 {
		t.Fatalf("preview: %d %s", r.rec.Code, r.rec.Body.String())
	}
	pv := waitTask(t, env, ck, hdr, taskID(t, r), "done")
	res, _ := pv["result"].(map[string]any)
	if res["previewId"] != pv["id"] || res["totalFiles"] != float64(2) || res["totalReplacements"] != float64(2) {
		t.Fatalf("preview result %v", res)
	}
	r = env.call(http.MethodPost, "/api/v1/search/replace", ip, map[string]any{"previewId": pv["id"], "files": []string{"/proj/a.txt"}}, hdr, ck)
	if r.rec.Code != 202 {
		t.Fatalf("apply: %d %s", r.rec.Code, r.rec.Body.String())
	}
	ap := waitTask(t, env, ck, hdr, taskID(t, r), "done")
	ares, _ := ap["result"].(map[string]any)
	vids, _ := ares["versionIds"].([]any)
	if ares["replacements"] != float64(1) || len(vids) != 1 {
		t.Fatalf("apply result %v", ares)
	}
	r = env.call(http.MethodGet, "/api/v1/search/names?q=a.txt&path=/proj", ip, nil, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("names after apply: %d", r.rec.Code)
	}
	r = env.call(http.MethodGet, "/api/v1/versions?path=/proj/a.txt", ip, nil, hdr, ck)
	vitems, _ := r.body["items"].([]any)
	if r.rec.Code != 200 || len(vitems) != 1 {
		t.Fatalf("versions: %d %s", r.rec.Code, r.rec.Body.String())
	}
	vid, _ := vids[0].(string)
	// user B: nothing of A is visible
	bck, bhdr := env.newUser(t, "bob", readyPassword)
	r = env.call(http.MethodGet, "/api/v1/versions?path=/proj/a.txt", ip, nil, bhdr, bck)
	if bitems, _ := r.body["items"].([]any); r.rec.Code != 200 || len(bitems) != 0 {
		t.Fatalf("bob versions: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/versions/"+vid+"/restore", ip, nil, bhdr, bck)
	if r.rec.Code != 404 {
		t.Fatalf("bob restore: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/search/replace", ip, map[string]any{"previewId": pv["id"]}, bhdr, bck)
	if r.rec.Code != 404 {
		t.Fatalf("bob apply A's preview: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/search/names?q=a&path=/proj", ip, nil, bhdr, bck)
	if r.rec.Code != 404 {
		t.Fatalf("bob names in A's folder: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodDelete, "/api/v1/versions/"+vid, ip, nil, bhdr, bck)
	if r.rec.Code != 404 {
		t.Fatalf("bob delete version: %d", r.rec.Code)
	}
	// admin restores: content back, pre-image version created
	r = env.call(http.MethodPost, "/api/v1/versions/"+vid+"/restore", ip, nil, hdr, ck)
	if r.rec.Code != 200 || r.body["path"] != "/proj/a.txt" {
		t.Fatalf("restore: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/search/content", ip, map[string]any{"q": "TODO alpha", "path": "/proj"}, hdr, ck)
	if r.rec.Code != 200 || !strings.Contains(r.rec.Body.String(), `"file":"/proj/a.txt"`) {
		t.Fatalf("content after restore: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/versions?path=/proj/a.txt", ip, nil, hdr, ck)
	if vitems, _ = r.body["items"].([]any); len(vitems) != 2 {
		t.Fatalf("versions after restore: %s", r.rec.Body.String())
	}

	// history
	r = env.call(http.MethodGet, "/api/v1/search/history", ip, nil, hdr, ck)
	if h, _ := r.body["items"].([]any); r.rec.Code != 200 || len(h) < 3 {
		t.Fatalf("history: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/search/history?kind=name", ip, nil, hdr, ck)
	for _, it := range r.body["items"].([]any) {
		if it.(map[string]any)["kind"] != "name" {
			t.Fatalf("kind filter: %v", it)
		}
	}
	r = env.call(http.MethodDelete, "/api/v1/search/history", ip, nil, hdr, ck)
	if r.rec.Code != 204 {
		t.Fatalf("clear history: %d", r.rec.Code)
	}
	r = env.call(http.MethodGet, "/api/v1/search/history", ip, nil, hdr, ck)
	if h, _ := r.body["items"].([]any); len(h) != 0 {
		t.Fatalf("history not cleared: %s", r.rec.Body.String())
	}

	// name rate limit: 30 fresh searches per minute, then 429 + Retry-After
	var last resp
	for i := 0; i < 40; i++ {
		last = env.call(http.MethodGet, "/api/v1/search/names?q=zz"+strings.Repeat("a", i%7)+string(rune('a'+i%26))+"&path=/proj&recursive="+[]string{"1", "0"}[i%2], ip, nil, hdr, ck)
		if last.rec.Code == 429 {
			break
		}
	}
	if last.rec.Code != 429 || last.code(t) != "RATE_LIMITED" || last.rec.Header().Get("Retry-After") == "" {
		t.Fatalf("rate limit: %d %s %q", last.rec.Code, last.rec.Body.String(), last.rec.Header().Get("Retry-After"))
	}

	// gates
	r = env.call(http.MethodGet, "/api/v1/search/names?q=a", ip, nil, nil, nil)
	if r.rec.Code != 401 {
		t.Fatalf("anonymous: %d", r.rec.Code)
	}
	r = env.call(http.MethodPost, "/api/v1/search/content", ip, map[string]any{"q": "a"}, nil, ck)
	if r.rec.Code != 403 {
		t.Fatalf("no csrf: %d", r.rec.Code)
	}
}
