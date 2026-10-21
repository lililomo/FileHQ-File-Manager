package httpapi

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kppbwi.app/filemanager/internal/files"
)

// Review D probes (PROMPT Step R-D checklist b, c, f, g): encoding round
// trips through the editor API, If-Match handling, version isolation
// between users, signed media URLs on the download/zip routes and the
// subtitle sanitiser as served.

// TestReviewDMediaURLCannotDownloadOrZip: a `purpose=media` signature is
// bound to the raw route; replaying it on /files/download or /files/zip, or
// rewriting the purpose, is refused (checklist g).
func TestReviewDMediaURLCannotDownloadOrZip(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := env.newUser(t, "mia", readyPassword)
	ip := "203.0.113.70:1"
	writeHome(t, env, "mia", "clip.mp4", strings.Repeat("x", 64))
	writeHome(t, env, "mia", "page.html", "<script>alert(1)</script>")
	r := env.call(http.MethodPost, "/api/v1/files/sign", ip, map[string]any{"path": "/clip.mp4", "purpose": "media"}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("sign: %d %s", r.rec.Code, r.rec.Body.String())
	}
	signed, _ := r.body["url"].(string)
	u, err := url.Parse(signed)
	if err != nil || !strings.HasPrefix(u.Path, "/api/v1/files/raw") {
		t.Fatalf("signed url %q", signed)
	}
	q := u.RawQuery
	// the media URL works where it was issued
	if rr := env.call(http.MethodGet, signed, ip, nil, nil, nil); rr.rec.Code != 200 {
		t.Fatalf("raw media: %d %s", rr.rec.Code, rr.rec.Body.String())
	}
	for _, route := range []string{"/api/v1/files/download", "/api/v1/files/zip", "/api/v1/files/thumb", "/api/v1/archives/entry-content"} {
		rr := env.call(http.MethodGet, route+"?"+q, ip, nil, nil, nil)
		if rr.rec.Code != 403 || rr.code(t) != "SIGNATURE_INVALID" {
			t.Fatalf("%s with media signature: %d %s", route, rr.rec.Code, rr.rec.Body.String())
		}
	}
	// rewriting the purpose keeps the old signature → invalid
	for _, p := range []string{"download", "inline", "zip"} {
		vals := u.Query()
		vals.Set("purpose", p)
		rr := env.call(http.MethodGet, u.Path+"?"+vals.Encode(), ip, nil, nil, nil)
		if rr.rec.Code != 403 {
			t.Fatalf("purpose %s rewritten: %d %s", p, rr.rec.Code, rr.rec.Body.String())
		}
	}
	// the subtitle route accepts media signatures but only for .srt/.vtt
	if rr := env.call(http.MethodGet, "/api/v1/files/subtitle?"+q, ip, nil, nil, nil); rr.rec.Code != 400 {
		t.Fatalf("subtitle with a video path: %d %s", rr.rec.Code, rr.rec.Body.String())
	}
	// an HTML file through the media route is never inline (ADR-06)
	r = env.call(http.MethodPost, "/api/v1/files/sign", ip, map[string]any{"path": "/page.html", "purpose": "media"}, hdr, ck)
	html, _ := r.body["url"].(string)
	rr := env.call(http.MethodGet, html, ip, nil, nil, nil)
	if rr.rec.Code != 200 || rr.rec.Header().Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(rr.rec.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("html via media: %d %q %q", rr.rec.Code, rr.rec.Header().Get("Content-Type"), rr.rec.Header().Get("Content-Disposition"))
	}
	if csp := rr.rec.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "sandbox") {
		t.Fatalf("raw CSP %q", csp)
	}
}

// TestReviewDContentPutIfMatchMandatory: a save without If-Match is a
// conflict (the client must prove what it saw), `*` and a matching etag
// pass, force skips the check and is recorded as force_save (checklist f).
func TestReviewDContentPutIfMatchMandatory(t *testing.T) {
	env := newAuthEnv(t, "FILE_VERSION_LIMIT=5")
	ck, hdr := env.newUser(t, "ifm", readyPassword)
	ip := "203.0.113.71:1"
	writeHome(t, env, "ifm", "a.txt", "one\n")
	csrf := map[string]string{"X-CSRF-Token": hdr["X-CSRF-Token"]}
	body := map[string]any{"path": "/a.txt", "content": "two\n"}
	r := env.call(http.MethodPut, "/api/v1/files/content", ip, body, csrf, ck)
	if r.rec.Code != 409 || r.code(t) != "CONFLICT" {
		t.Fatalf("no If-Match: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if got, _ := os.ReadFile(filepath.Join(env.cfg.StorageRoot, "ifm", "a.txt")); string(got) != "one\n" {
		t.Fatalf("file changed without If-Match: %q", got)
	}
	r = env.call(http.MethodPut, "/api/v1/files/content", ip, body, map[string]string{"X-CSRF-Token": hdr["X-CSRF-Token"], "If-Match": `"1-2"`}, ck)
	if r.rec.Code != 409 {
		t.Fatalf("wrong If-Match: %d", r.rec.Code)
	}
	r = env.call(http.MethodPut, "/api/v1/files/content", ip, body, map[string]string{"X-CSRF-Token": hdr["X-CSRF-Token"], "If-Match": "*"}, ck)
	if r.rec.Code != 200 || r.body["noop"] != false {
		t.Fatalf("If-Match *: %d %s", r.rec.Code, r.rec.Body.String())
	}
	etag := r.rec.Header().Get("ETag")
	r = env.call(http.MethodPut, "/api/v1/files/content", ip, map[string]any{"path": "/a.txt", "content": "three\n"}, map[string]string{"X-CSRF-Token": hdr["X-CSRF-Token"], "If-Match": "W/" + etag}, ck)
	if r.rec.Code != 200 {
		t.Fatalf("weak etag: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPut, "/api/v1/files/content", ip, map[string]any{"path": "/a.txt", "content": "four\n", "force": true}, csrf, ck)
	if r.rec.Code != 200 {
		t.Fatalf("force: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/versions?path=/a.txt", ip, nil, nil, ck)
	items, _ := r.body["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("versions: %v", r.body)
	}
	newest, _ := items[0].(map[string]any)
	if newest["reason"] != "force_save" {
		t.Fatalf("newest reason %v", newest["reason"])
	}
}

// TestReviewDEncodingRoundTripViaAPI: files in eight encodings (with and
// without BOM) and a mixed-EOL file are opened and saved back unchanged —
// the server reports noop because the re-encoded bytes are identical
// (checklist b).
func TestReviewDEncodingRoundTripViaAPI(t *testing.T) {
	env := newAuthEnv(t, "FILE_VERSION_LIMIT=5")
	ck, hdr := env.newUser(t, "enc", readyPassword)
	ip := "203.0.113.72:1"
	cases := []struct {
		name, enc, text string
		bom             bool
	}{
		{"utf8.txt", "utf-8", "héllo — wörld\nbaris dua\n", false},
		{"utf8bom.txt", "utf-8", "héllo\r\nBOM tetap\r\n", true},
		{"utf16le.txt", "utf-16le", "UTF-16 LE ✓\r\nbaris\r\n", true},
		{"utf16be.txt", "utf-16be", "UTF-16 BE ✓\nbaris\n", true},
		{"gbk.txt", "gbk", "中文内容 第二行\n第三行 第四行内容\n", false},
		{"sjis.txt", "shift_jis", "日本語のテキスト\r\nカタカナ\r\n", false},
		{"euckr.txt", "euc-kr", "한국어 텍스트\n둘째 줄 셋째 줄\n", false},
		{"big5.txt", "big5", "繁體中文測試\n第二行 第三行\n", false},
		{"cp1252.txt", "windows-1252", "café — naïve €\r\n", false},
		{"iso2.txt", "iso-8859-2", "zażółć gęślą jaźń\n", false}, // detection falls back to windows-1252; opened with ?encoding=
		{"koi8.txt", "koi8-r", "Привет мир\n", false},
		{"mixed.txt", "utf-8", "a\r\nb\nc\r\n\nd", false},
		{"cronly.txt", "utf-8", "a\rb\rc", false},
	}
	for _, c := range cases {
		raw, err := files.EncodeText(c.text, c.enc, c.bom)
		if err != nil {
			t.Fatalf("%s encode: %v", c.name, err)
		}
		writeHome(t, env, "enc", c.name, string(raw))
	}
	for _, c := range cases {
		target := "/api/v1/files/content?path=/" + c.name
		if c.enc == "iso-8859-2" {
			target += "&encoding=" + c.enc
		}
		r := env.call(http.MethodGet, target, ip, nil, nil, ck)
		if r.rec.Code != 200 {
			t.Fatalf("%s open: %d %s", c.name, r.rec.Code, r.rec.Body.String())
		}
		if r.body["encoding"] != c.enc || r.body["bom"] != c.bom || r.body["content"] != c.text {
			t.Fatalf("%s open body: enc=%v bom=%v content=%q", c.name, r.body["encoding"], r.body["bom"], r.body["content"])
		}
		h := map[string]string{"X-CSRF-Token": hdr["X-CSRF-Token"], "If-Match": r.rec.Header().Get("ETag")}
		put := map[string]any{"path": "/" + c.name, "content": r.body["content"], "encoding": r.body["encoding"], "bom": r.body["bom"], "eol": "keep"}
		rr := env.call(http.MethodPut, "/api/v1/files/content", ip, put, h, ck)
		if rr.rec.Code != 200 || rr.body["noop"] != true {
			t.Fatalf("%s save: %d %s", c.name, rr.rec.Code, rr.rec.Body.String())
		}
		before, _ := files.EncodeText(c.text, c.enc, c.bom)
		after, _ := os.ReadFile(filepath.Join(env.cfg.StorageRoot, "enc", c.name))
		if string(before) != string(after) {
			t.Fatalf("%s bytes changed: %q → %q", c.name, before, after)
		}
	}
	// a real edit in a legacy encoding stays in that encoding and keeps CRLF
	r := env.call(http.MethodGet, "/api/v1/files/content?path=/sjis.txt", ip, nil, nil, ck)
	h := map[string]string{"X-CSRF-Token": hdr["X-CSRF-Token"], "If-Match": r.rec.Header().Get("ETag")}
	rr := env.call(http.MethodPut, "/api/v1/files/content", ip, map[string]any{"path": "/sjis.txt", "content": "日本語のテキスト\r\nカタカナ\r\n追加\r\n", "encoding": "shift_jis", "eol": "keep"}, h, ck)
	if rr.rec.Code != 200 || rr.body["noop"] != false {
		t.Fatalf("sjis edit: %d %s", rr.rec.Code, rr.rec.Body.String())
	}
	want, _ := files.EncodeText("日本語のテキスト\r\nカタカナ\r\n追加\r\n", "shift_jis", false)
	got, _ := os.ReadFile(filepath.Join(env.cfg.StorageRoot, "enc", "sjis.txt"))
	if string(got) != string(want) {
		t.Fatalf("sjis bytes: %q", got)
	}
	// a character outside the target encoding is refused with its position
	rr = env.call(http.MethodPut, "/api/v1/files/content", ip, map[string]any{"path": "/iso2.txt", "content": "ok\n中\n", "encoding": "iso-8859-2", "force": true}, map[string]string{"X-CSRF-Token": hdr["X-CSRF-Token"]}, ck)
	det, _ := rr.body["error"].(map[string]any)
	details, _ := det["details"].(map[string]any)
	if rr.rec.Code != 400 || details["line"] != float64(2) || details["col"] != float64(1) {
		t.Fatalf("unencodable: %d %s", rr.rec.Code, rr.rec.Body.String())
	}
}

// TestReviewDVersionsNoCrossUserLeak: versions are keyed by (user, path);
// another user with the same virtual path neither sees nor can touch them,
// and a delete-all by that user leaves the owner's versions alone
// (checklist c).
func TestReviewDVersionsNoCrossUserLeak(t *testing.T) {
	env := newAuthEnv(t, "FILE_VERSION_LIMIT=5")
	ckA, hdrA := env.newUser(t, "ana", readyPassword)
	ckB, hdrB := env.newUser(t, "bob", readyPassword)
	ip := "203.0.113.73:1"
	writeHome(t, env, "ana", "same.txt", "ana v1\n")
	writeHome(t, env, "bob", "same.txt", "bob v1\n")
	save := func(ck *http.Cookie, hdr map[string]string, content string) {
		t.Helper()
		r := env.call(http.MethodGet, "/api/v1/files/content?path=/same.txt", ip, nil, nil, ck)
		h := map[string]string{"X-CSRF-Token": hdr["X-CSRF-Token"], "If-Match": r.rec.Header().Get("ETag")}
		if rr := env.call(http.MethodPut, "/api/v1/files/content", ip, map[string]any{"path": "/same.txt", "content": content}, h, ck); rr.rec.Code != 200 {
			t.Fatalf("save: %d %s", rr.rec.Code, rr.rec.Body.String())
		}
	}
	save(ckA, hdrA, "ana v2\n")
	save(ckA, hdrA, "ana v3\n")
	r := env.call(http.MethodGet, "/api/v1/versions?path=/same.txt", ip, nil, nil, ckA)
	itemsA, _ := r.body["items"].([]any)
	if len(itemsA) != 2 {
		t.Fatalf("ana versions: %v", r.body)
	}
	idA, _ := itemsA[0].(map[string]any)["id"].(string)
	// bob: same path, no versions, cannot reach ana's ids
	r = env.call(http.MethodGet, "/api/v1/versions?path=/same.txt", ip, nil, nil, ckB)
	if items, _ := r.body["items"].([]any); r.rec.Code != 200 || len(items) != 0 {
		t.Fatalf("bob sees versions: %v", r.body)
	}
	hB := map[string]string{"X-CSRF-Token": hdrB["X-CSRF-Token"]}
	for _, c := range []struct{ method, target string }{
		{http.MethodGet, "/api/v1/versions/" + idA + "/content"},
		{http.MethodGet, "/api/v1/versions/" + idA + "/download"},
		{http.MethodPost, "/api/v1/versions/" + idA + "/restore"},
		{http.MethodDelete, "/api/v1/versions/" + idA},
	} {
		rr := env.call(c.method, c.target, ip, nil, hB, ckB)
		if rr.rec.Code != 404 {
			t.Fatalf("bob %s %s: %d %s", c.method, c.target, rr.rec.Code, rr.rec.Body.String())
		}
	}
	if rr := env.call(http.MethodDelete, "/api/v1/versions?path=/same.txt", ip, nil, hB, ckB); rr.rec.Code != 204 {
		t.Fatalf("bob delete-all: %d", rr.rec.Code)
	}
	r = env.call(http.MethodGet, "/api/v1/versions?path=/same.txt", ip, nil, nil, ckA)
	if items, _ := r.body["items"].([]any); len(items) != 2 {
		t.Fatalf("ana lost versions after bob's delete-all: %v", r.body)
	}
	// ana's blob still opens; bob's file untouched
	if rr := env.call(http.MethodGet, "/api/v1/versions/"+idA+"/content", ip, nil, nil, ckA); rr.rec.Code != 200 || rr.body["content"] != "ana v2\n" {
		t.Fatalf("ana content: %d %v", rr.rec.Code, rr.body)
	}
	if got, _ := os.ReadFile(filepath.Join(env.cfg.StorageRoot, "bob", "same.txt")); string(got) != "bob v1\n" {
		t.Fatalf("bob file: %q", got)
	}
}

// TestReviewDSubtitleHostilePayloads: what /files/subtitle serves for
// hostile SRT/VTT input (checklist a, g).
func TestReviewDSubtitleHostilePayloads(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := env.newUser(t, "sub", readyPassword)
	ip := "203.0.113.74:1"
	srt := "1\r\n00:00:01,000 --> 00:00:02,000 X1:0 Y1:0\r\n<font color=red><b>bold</b></font><script>alert(1)</script>\r\n<c.red>c</c> <v Bob>v</v> <00:00:01.500>karaoke\r\n\r\n" +
		"2\n00:00:03,000 --> 00:00:04,000\nline one\n\nline two after blank\n\n" +
		"STYLE\n::cue { background: url(http://evil/x) }\n\n" +
		"3\n00:00:05,000 --> 00:00:06,000\ntext --> 00:00:07.000 injected\n&lt;ok&gt; &bogus; a < b > c & d\n\n" +
		"4\n99:99:99,000 --> 00:00:01,000\nbad time\n\n" +
		"5\n00:00:08,000 --> 00:00:09,000 line:-1 position:50% align:start vertical:rl size:50% onclick:x\n<i>it</i><u>u</u><b.cls attr=1>b</b><img src=x onerror=1>\n"
	writeHome(t, env, "sub", "film.srt", srt)
	writeHome(t, env, "sub", "film.vtt", "WEBVTT\n\nSTYLE\n::cue { color: red }\n\nREGION\nid:r1\n\nNOTE hi\n\n00:01.000 --> 00:02.000\n<ruby>r<rt>t</rt></ruby><lang en>x</lang>\n")
	for _, name := range []string{"film.srt", "film.vtt"} {
		r := env.call(http.MethodPost, "/api/v1/files/sign", ip, map[string]any{"path": "/" + name, "purpose": "media"}, hdr, ck)
		u, _ := url.Parse(r.body["url"].(string))
		rr := env.call(http.MethodGet, "/api/v1/files/subtitle?"+u.RawQuery, ip, nil, nil, nil)
		if rr.rec.Code != 200 || rr.rec.Header().Get("Content-Type") != "text/vtt; charset=utf-8" {
			t.Fatalf("%s: %d %q", name, rr.rec.Code, rr.rec.Header().Get("Content-Type"))
		}
		out := rr.rec.Body.String()
		if !strings.HasPrefix(out, "WEBVTT\n\n") {
			t.Fatalf("%s: not vtt: %q", name, out)
		}
		for _, bad := range []string{"<script", "<font", "<c.", "<v ", "<00:", "STYLE", "REGION", "NOTE", "::cue", "onclick", "<img", "<ruby", "<lang", "X1:", "99:99", "&bogus;"} {
			if strings.Contains(out, bad) {
				t.Fatalf("%s: %q survived:\n%s", name, bad, out)
			}
		}
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, "-->") && !strings.HasPrefix(line, "00:") {
				t.Fatalf("%s: timing arrow inside cue text: %q", name, line)
			}
			if strings.HasPrefix(line, "<") && !strings.HasPrefix(line, "<b>") && !strings.HasPrefix(line, "<i>") && !strings.HasPrefix(line, "<u>") && !strings.HasPrefix(line, "</") {
				t.Fatalf("%s: unexpected tag line %q", name, line)
			}
		}
		if name == "film.srt" {
			for _, want := range []string{"<b>bold</b>", "&lt;ok&gt; &amp;bogus; a &lt; b &gt; c &amp; d", "text --&gt; 00:00:07.000 injected", "line:-1 position:50% align:start vertical:rl size:50%\n", "<i>it</i><u>u</u><b>b</b>"} {
				if !strings.Contains(out, want) {
					t.Fatalf("srt: %q missing:\n%s", want, out)
				}
			}
			if strings.Contains(out, "line one\n\nline two") || !strings.Contains(out, "line one\n\n") {
				t.Fatalf("srt: blank line handling:\n%s", out)
			}
		}
	}
}
