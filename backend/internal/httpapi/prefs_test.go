package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

// Step 7b: preference registry and folder view prefs (SPEC §8.18, §6.3 "me").

func TestPreferencesRegistry(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	put := func(prefs map[string]any) resp {
		return env.call(http.MethodPut, "/api/v1/me/preferences", "203.0.113.5:1", map[string]any{"prefs": prefs}, hdr, ck)
	}
	// valid keys of several shapes
	r := put(map[string]any{
		"view.columns":              map[string]any{"visible": []string{"name", "size"}, "order": []string{"size", "name"}, "widths": map[string]int{"size": 120}},
		"view.sort":                 map[string]any{"field": "mtime", "dir": "desc", "field2": "name", "dir2": "asc"},
		"view.folders_first":        false,
		"ui.preview_panel":          true,
		"tabs.open":                 []map[string]string{{"path": "/"}, {"path": "/a"}},
		"editor.fontSize":           14,
		"viewer.slideshow_interval": 5,
	})
	if r.rec.Code != 200 {
		t.Fatalf("valid prefs: %d %s", r.rec.Code, r.rec.Body.String())
	}
	prefs := r.body["prefs"].(map[string]any)
	if prefs["view.folders_first"] != false || prefs["ui.preview_panel"] != true {
		t.Fatalf("prefs not stored: %v", prefs)
	}
	// unknown key
	r = put(map[string]any{"foo.bar": 1})
	if r.rec.Code != 400 {
		t.Fatalf("unknown key: %d", r.rec.Code)
	}
	fields := r.body["error"].(map[string]any)["details"].(map[string]any)["fields"].(map[string]any)
	if fields["foo.bar"] != "unknown_key" {
		t.Fatalf("fields = %v", fields)
	}
	// invalid values
	bad := []map[string]any{
		{"ui.theme": "blue"},
		{"view.columns": map[string]any{"visible": []string{"size"}}}, // name mandatory
		{"view.columns": map[string]any{"visible": []string{"name", "nope"}}},
		{"view.columns": map[string]any{"widths": map[string]int{"name": 10}}},
		{"view.sort": map[string]any{"field": "weird", "dir": "asc"}},
		{"view.sort": map[string]any{"field": "name", "dir": "up"}},
		{"tabs.open": make([]map[string]string, 11)},
		{"tabs.open": []map[string]string{{"path": "relative"}}},
		{"audio.volume": 2},
		{"viewer.slideshow_interval": 3},
		{"ui.timezone": "Mars/Olympus"},
		{"video.positions": map[string]float64{"/a": -1}},
	}
	for _, b := range bad {
		r = put(b)
		if r.rec.Code != 400 {
			t.Errorf("expected 400 for %v, got %d %s", b, r.rec.Code, r.rec.Body.String())
		}
	}
	// null removes a registered key; null on unknown key is rejected
	r = put(map[string]any{"ui.preview_panel": nil})
	if r.rec.Code != 200 {
		t.Fatalf("null delete: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if _, ok := r.body["prefs"].(map[string]any)["ui.preview_panel"]; ok {
		t.Fatal("key not removed")
	}
	r = put(map[string]any{"nope.key": nil})
	if r.rec.Code != 400 {
		t.Fatalf("null unknown key: %d", r.rec.Code)
	}
	// timezone IANA ok
	r = put(map[string]any{"ui.timezone": "Asia/Jakarta"})
	if r.rec.Code != 200 {
		t.Fatalf("timezone: %d %s", r.rec.Code, r.rec.Body.String())
	}
}

func TestFolderViewPrefs(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	ip := "203.0.113.5:1"
	get := func(path string) resp {
		return env.call(http.MethodGet, "/api/v1/me/folder-prefs?path="+path, ip, nil, hdr, ck)
	}
	r := get("/proyek")
	if r.rec.Code != 200 || r.body["pref"] != nil {
		t.Fatalf("absent pref: %d %v", r.rec.Code, r.body)
	}
	r = env.call(http.MethodPut, "/api/v1/me/folder-prefs", ip, map[string]any{
		"path": "/proyek", "view": "large", "sort": "size", "dir": "desc", "sort2": "name", "dir2": "asc",
		"columns": map[string]any{"visible": []string{"name", "mtime"}},
	}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("put: %d %s", r.rec.Code, r.rec.Body.String())
	}
	pref := r.body["pref"].(map[string]any)
	if pref["view"] != "large" || pref["path"] != "/proyek" {
		t.Fatalf("pref = %v", pref)
	}
	sort := pref["sort"].(map[string]any)
	if sort["field"] != "size" || sort["dir"] != "desc" || sort["field2"] != "name" {
		t.Fatalf("sort = %v", sort)
	}
	cols := pref["columns"].(map[string]any)
	if vis, _ := cols["visible"].([]any); len(vis) != 2 {
		t.Fatalf("columns = %v", cols)
	}
	r = get("/proyek")
	if r.rec.Code != 200 || r.body["pref"] == nil {
		t.Fatalf("get after put: %d %v", r.rec.Code, r.body)
	}
	// defaults: view details, sort name asc, no columns
	r = env.call(http.MethodPut, "/api/v1/me/folder-prefs", ip, map[string]any{"path": "/lain"}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("put defaults: %d %s", r.rec.Code, r.rec.Body.String())
	}
	pref = r.body["pref"].(map[string]any)
	if pref["view"] != "details" || pref["columns"] != nil || pref["sort"].(map[string]any)["field"] != "name" {
		t.Fatalf("defaults = %v", pref)
	}
	// validation
	for _, body := range []map[string]any{
		{"path": "/x", "view": "huge"},
		{"path": "/x", "sort": "nope"},
		{"path": "/x", "dir": "sideways"},
		{"path": "/x", "columns": map[string]any{"visible": []string{"bogus"}}},
		{"path": "../x"},
	} {
		r = env.call(http.MethodPut, "/api/v1/me/folder-prefs", ip, body, hdr, ck)
		if r.rec.Code != 400 {
			t.Errorf("expected 400 for %v: %d %s", body, r.rec.Code, r.rec.Body.String())
		}
	}
	r = get("/.system")
	if r.rec.Code != 404 {
		t.Fatalf(".system: %d", r.rec.Code)
	}
	// isolation: another user does not see it
	ck2, hdr2 := env.newUser(t, "budi", "Password budi 2026 kuat")
	r = env.call(http.MethodGet, "/api/v1/me/folder-prefs?path=/proyek", "203.0.113.9:1", nil, hdr2, ck2)
	if r.rec.Code != 200 || r.body["pref"] != nil {
		t.Fatalf("isolation: %d %v", r.rec.Code, r.body)
	}
	// delete
	r = env.call(http.MethodDelete, "/api/v1/me/folder-prefs?path=/proyek", ip, nil, hdr, ck)
	if r.rec.Code != 204 {
		t.Fatalf("delete: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = get("/proyek")
	if r.body["pref"] != nil {
		t.Fatal("pref still present after delete")
	}
	// CSRF required on PUT/DELETE
	r = env.call(http.MethodPut, "/api/v1/me/folder-prefs", ip, map[string]any{"path": "/a"}, nil, ck)
	if r.rec.Code != 403 {
		t.Fatalf("csrf: %d", r.rec.Code)
	}
}

func TestFolderViewPrefsLRU(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	ip := "203.0.113.5:1"
	for i := 0; i < 502; i++ {
		r := env.call(http.MethodPut, "/api/v1/me/folder-prefs", ip, map[string]any{"path": "/d" + itoa(i)}, hdr, ck)
		if r.rec.Code != 200 {
			t.Fatalf("put %d: %d %s", i, r.rec.Code, r.rec.Body.String())
		}
	}
	r := env.call(http.MethodGet, "/api/v1/me/folder-prefs?path=/d0", ip, nil, hdr, ck)
	if r.body["pref"] != nil {
		t.Fatal("oldest row should have been evicted at 500")
	}
	r = env.call(http.MethodGet, "/api/v1/me/folder-prefs?path=/d501", ip, nil, hdr, ck)
	if r.body["pref"] == nil {
		t.Fatal("newest row missing")
	}
}

func itoa(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{digits[i%10]}, b...)
		i /= 10
	}
	return string(b)
}

// Step 23b: dual-pane preferences (SPEC §8.18).
func TestDualPanePrefValidator(t *testing.T) {
	ok := []string{
		`{"tabs":[{"path":"/"}]}`,
		`{"tabs":[{"path":"/"},{"path":"/Proyek/web"}],"active":1}`,
		`{"tabs":[{"path":"/a"},{"path":"/a"}],"active":0}`, // duplicates allowed like tabs.open
	}
	for _, v := range ok {
		if r := validatePref("dualpane.right", []byte(v)); r != "" {
			t.Errorf("%s: %q, want valid", v, r)
		}
	}
	tooMany := `{"tabs":[` + strings.Repeat(`{"path":"/"},`, 10) + `{"path":"/"}]}`
	bad := []string{
		`{}`,                                    // no tabs
		`{"tabs":[]}`,                           // empty
		tooMany,                                 // > 10
		`{"tabs":[{"path":"relative"}]}`,        // not absolute
		`{"tabs":[{"path":"/"}],"active":1}`,    // index out of range
		`{"tabs":[{"path":"/"}],"active":-1}`,   // negative
		`{"tabs":[{"path":"/"}],"active":"0"}`,  // wrong type
		`{"tabs":[{"path":"/"}],"sort":"name"}`, // unknown field
		`{"tabs":[{"path":"/","x":1}]}`,         // unknown nested field
		`{"tabs":[{"path":"/` + strings.Repeat("a", 4100) + `"}]}`,
		`[{"path":"/"}]`, // tabs.open shape is not accepted here
		`true`,
	}
	for _, v := range bad {
		if r := validatePref("dualpane.right", []byte(v)); r != "invalid_value" {
			t.Errorf("%.60s: %q, want invalid_value", v, r)
		}
	}
	if r := validatePref("dualpane.enabled", []byte(`true`)); r != "" {
		t.Errorf("dualpane.enabled true: %q", r)
	}
	if r := validatePref("dualpane.enabled", []byte(`"yes"`)); r != "invalid_value" {
		t.Errorf("dualpane.enabled string: %q", r)
	}
	if r := validatePref("dualpane.left", []byte(`true`)); r != "unknown_key" {
		t.Errorf("dualpane.left: %q", r)
	}

	// end to end through PUT /me/preferences (merge patch, null removes)
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	put := func(prefs map[string]any) resp {
		return env.call(http.MethodPut, "/api/v1/me/preferences", "203.0.113.5:1", map[string]any{"prefs": prefs}, hdr, ck)
	}
	r := put(map[string]any{
		"dualpane.enabled": true,
		"dualpane.right":   map[string]any{"tabs": []map[string]string{{"path": "/"}, {"path": "/b"}}, "active": 1},
	})
	if r.rec.Code != 200 {
		t.Fatalf("valid dual-pane prefs: %d %s", r.rec.Code, r.rec.Body.String())
	}
	prefs := r.body["prefs"].(map[string]any)
	if prefs["dualpane.enabled"] != true {
		t.Fatalf("dualpane.enabled not stored: %v", prefs)
	}
	right := prefs["dualpane.right"].(map[string]any)
	if right["active"] != float64(1) || len(right["tabs"].([]any)) != 2 {
		t.Fatalf("dualpane.right = %v", right)
	}
	r = put(map[string]any{"dualpane.right": map[string]any{"tabs": []map[string]string{{"path": "x"}}}})
	if r.rec.Code != 400 {
		t.Fatalf("invalid dualpane.right: %d", r.rec.Code)
	}
	fields := r.body["error"].(map[string]any)["details"].(map[string]any)["fields"].(map[string]any)
	if fields["dualpane.right"] != "invalid_value" {
		t.Fatalf("fields = %v", fields)
	}
	r = put(map[string]any{"dualpane.enabled": nil, "dualpane.right": nil})
	if r.rec.Code != 200 {
		t.Fatalf("null removes: %d", r.rec.Code)
	}
	prefs = r.body["prefs"].(map[string]any)
	if _, has := prefs["dualpane.enabled"]; has {
		t.Fatalf("dualpane.enabled not removed: %v", prefs)
	}
}
