package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strings"
	"testing"

	"kppbwi.app/filemanager/internal/db/sqlc"
)

// testVideo encodes a 1 s H.264 clip (skips without ffmpeg).
func testVideo(t *testing.T) []byte {
	t.Helper()
	if _, err := os.Stat(testFFmpeg); err != nil {
		t.Skip("ffmpeg not installed")
	}
	out := filepath.Join(t.TempDir(), "v.mp4")
	cmd := osexec.Command(testFFmpeg, "-nostdin", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc=size=64x48:rate=5", "-t", "1",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestSubtitleSidecarLookupAndServe: media-info of a video lists its
// sidecars with signed /files/subtitle URLs; SRT comes back as sanitised
// WebVTT (also from Windows-1252), VTT is re-emitted, other files and
// unsigned/foreign requests are refused.
func TestSubtitleSidecarLookupAndServe(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	withThumbs(t, env, testFFmpeg)
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	dir := filepath.Join(home, "film")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	write := func(name string, b []byte) {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	write("klip.mp4", testVideo(t))
	write("klip.srt", []byte("1\r\n00:00:00,500 --> 00:00:01,000\r\n<font color=red>Halo</font> <i>dunia</i><script>alert(1)</script>\r\n\r\n"))
	// "café" in Windows-1252 plus enough text for detection
	write("klip.fr.srt", append([]byte("1\n00:00:00,000 --> 00:00:01,000\nUn caf"), append([]byte{0xE9}, []byte(" tr\xe8s chaud, d\xe9j\xe0 servi \xe0 la cr\xe8me\n\n")...)...))
	write("klip.en.vtt", []byte("WEBVTT\n\nSTYLE\n::cue { color: red }\n\n00:00.000 --> 00:01.000 align:start\n<v Narator>Hello</v>\n"))
	write("lain.srt", []byte("1\n00:00:00,000 --> 00:00:01,000\nbukan milik klip\n"))
	write("klip.txt", []byte("bukan subtitle"))

	ip := "203.0.113.7:1"
	r := env.call(http.MethodGet, "/api/v1/files/media-info?path=/film/klip.mp4", ip, nil, nil, ck)
	if r.rec.Code != 200 || r.body["kind"] != "video" {
		t.Fatalf("media-info: %d %s", r.rec.Code, r.rec.Body.String())
	}
	subs, _ := r.body["subtitles"].([]any)
	if len(subs) != 3 {
		t.Fatalf("subtitles: %v", r.body["subtitles"])
	}
	byName := map[string]map[string]any{}
	for _, s := range subs {
		m := s.(map[string]any)
		byName[m["name"].(string)] = m
	}
	if byName["klip.srt"] == nil || byName["klip.en.vtt"]["lang"] != "en" || byName["klip.fr.srt"]["format"] != "srt" || byName["lain.srt"] != nil {
		t.Fatalf("sidecars: %v", byName)
	}
	get := func(u string) (int, string, http.Header) {
		res := env.call(http.MethodGet, u, ip, nil, nil, nil) // signature only, no cookie
		return res.rec.Code, res.rec.Body.String(), res.rec.Header()
	}
	code, body, h := get(byName["klip.srt"]["url"].(string))
	if code != 200 || h.Get("Content-Type") != "text/vtt; charset=utf-8" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("srt: %d %v %s", code, h, body)
	}
	if body != "WEBVTT\n\n00:00:00.500 --> 00:00:01.000\nHalo <i>dunia</i>alert(1)\n\n" {
		t.Fatalf("srt body: %q", body)
	}
	if code, body, _ = get(byName["klip.fr.srt"]["url"].(string)); code != 200 || !strings.Contains(body, "Un café très chaud") {
		t.Fatalf("cp1252 srt: %d %q", code, body)
	}
	if code, body, _ = get(byName["klip.en.vtt"]["url"].(string)); code != 200 || body != "WEBVTT\n\n00:00:00.000 --> 00:00:01.000 align:start\nHello\n\n" {
		t.Fatalf("vtt: %d %q", code, body)
	}

	// a media signature of a non-subtitle file is refused by this route
	vidURL := signURL(t, env, ck, hdr, "/film/klip.txt", "media")
	q, _ := url.Parse(vidURL)
	if code, _, _ = get(SubtitlePath + "?" + q.RawQuery); code != 400 {
		t.Fatalf("txt through subtitle route: %d", code)
	}
	// unsigned / tampered
	if code, _, _ = get(SubtitlePath + "?path=/film/klip.srt&purpose=media"); code != 403 {
		t.Fatalf("unsigned: %d", code)
	}
	tampered := strings.Replace(byName["klip.srt"]["url"].(string), "klip.srt", "lain.srt", 1)
	if code, _, _ = get(tampered); code != 403 {
		t.Fatalf("tampered path: %d", code)
	}

	// IDOR: user B signs the same relative path → resolved in B's home → 404;
	// B cannot point the path at A's home either
	ckB, hdrB := env.newUser(t, "budi", readyPassword)
	r = env.call(http.MethodPost, "/api/v1/files/sign", ip, map[string]any{"path": "/film/klip.srt", "purpose": "media"}, hdrB, ckB)
	if r.rec.Code != 200 {
		t.Fatalf("sign B: %d", r.rec.Code)
	}
	q, _ = url.Parse(r.body["url"].(string))
	if code, _, _ = get(SubtitlePath + "?" + q.RawQuery); code != 404 {
		t.Fatalf("user B reads A's subtitle: %d", code)
	}
	r = env.call(http.MethodPost, "/api/v1/files/sign", ip, map[string]any{"path": "/../admin/film/klip.srt", "purpose": "media"}, hdrB, ckB)
	if r.rec.Code != 400 {
		t.Fatalf("escape sign: %d", r.rec.Code)
	}
	// B's media-info cannot see A's video at all
	if r = env.call(http.MethodGet, "/api/v1/files/media-info?path=/film/klip.mp4", ip, nil, nil, ckB); r.rec.Code != 404 {
		t.Fatalf("B media-info: %d", r.rec.Code)
	}

	// over the size limit → 413
	big := []byte("1\n00:00:00,000 --> 00:00:01,000\n" + strings.Repeat("x", 2<<20) + "\n")
	write("besar.srt", big)
	bigURL := signURL(t, env, ck, hdr, "/film/besar.srt", "media")
	q, _ = url.Parse(bigURL)
	if code, _, _ = get(SubtitlePath + "?" + q.RawQuery); code != 413 {
		t.Fatalf("big: %d", code)
	}
	// a video without sidecars has no subtitles key
	write("sepi.mp4", testVideo(t))
	r = env.call(http.MethodGet, "/api/v1/files/media-info?path=/film/sepi.mp4", ip, nil, nil, ck)
	if _, has := r.body["subtitles"]; has || r.rec.Code != 200 {
		t.Fatalf("no sidecars: %d %v", r.rec.Code, r.body)
	}
}

// TestMediaPosition: round trip, zero forgets, validation, privacy per
// user and the 500-row LRU cap.
func TestMediaPosition(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := env.newUser(t, "vina", readyPassword)
	ip := "203.0.113.8:1"
	r := env.call(http.MethodGet, "/api/v1/me/media-position?path=/film/a.mp4", ip, nil, nil, ck)
	if r.rec.Code != 200 || r.body["seconds"] != float64(0) || r.body["updatedAt"] != nil {
		t.Fatalf("empty: %d %v", r.rec.Code, r.body)
	}
	r = env.call(http.MethodPut, "/api/v1/me/media-position", ip, map[string]any{"path": "/film/a.mp4", "seconds": 754.25, "duration": 3600}, hdr, ck)
	if r.rec.Code != 200 || r.body["seconds"] != 754.25 {
		t.Fatalf("put: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/me/media-position?path=/film/a.mp4", ip, nil, nil, ck)
	if r.body["seconds"] != 754.25 || r.body["duration"] != float64(3600) || r.body["updatedAt"] == nil {
		t.Fatalf("get: %v", r.body)
	}
	for _, bad := range []map[string]any{
		{"path": "/film/a.mp4"}, {"path": "/film/a.mp4", "seconds": -1}, {"path": "/", "seconds": 1},
		{"path": "../x", "seconds": 1}, {"path": "/a", "seconds": 1e9}, {"path": "/a", "seconds": 1, "duration": -3},
	} {
		if r = env.call(http.MethodPut, "/api/v1/me/media-position", ip, bad, hdr, ck); r.rec.Code != 400 || r.code(t) != "VALIDATION_FAILED" {
			t.Fatalf("%v: %d %s", bad, r.rec.Code, r.rec.Body.String())
		}
	}
	// another user does not see it
	ck2, _ := env.newUser(t, "wawan", readyPassword)
	if r = env.call(http.MethodGet, "/api/v1/me/media-position?path=/film/a.mp4", ip, nil, nil, ck2); r.body["seconds"] != float64(0) {
		t.Fatalf("other user: %v", r.body)
	}
	// seconds 0 forgets
	r = env.call(http.MethodPut, "/api/v1/me/media-position", ip, map[string]any{"path": "/film/a.mp4", "seconds": 0}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("reset: %d", r.rec.Code)
	}
	if r = env.call(http.MethodGet, "/api/v1/me/media-position?path=/film/a.mp4", ip, nil, nil, ck); r.body["updatedAt"] != nil {
		t.Fatalf("after reset: %v", r.body)
	}
	// CSRF/cookie gates
	if r = env.call(http.MethodPut, "/api/v1/me/media-position", ip, map[string]any{"path": "/a", "seconds": 1}, nil, ck); r.rec.Code != 403 {
		t.Fatalf("no csrf: %d", r.rec.Code)
	}
	if r = env.call(http.MethodGet, "/api/v1/me/media-position?path=/a", ip, nil, nil, nil); r.rec.Code != 401 {
		t.Fatalf("anonymous: %d", r.rec.Code)
	}

	// LRU: 500 old rows + one new PUT → the oldest row is gone, 500 remain
	u, err := env.store.GetUserByUsername(context.Background(), "vina")
	if err != nil {
		t.Fatal(err)
	}
	for i := range MaxMediaPositions {
		if _, err := env.store.UpsertMediaPosition(context.Background(), sqlc.UpsertMediaPositionParams{
			UserID: u.ID, Path: fmt.Sprintf("/lama/%03d.mp4", i), Seconds: 10,
			UpdatedAt: fmt.Sprintf("2026-01-01T00:%02d:%02d.000Z", i/60, i%60),
		}); err != nil {
			t.Fatal(err)
		}
	}
	if r = env.call(http.MethodPut, "/api/v1/me/media-position", ip, map[string]any{"path": "/baru.mp4", "seconds": 5}, hdr, ck); r.rec.Code != 200 {
		t.Fatalf("put new: %d", r.rec.Code)
	}
	if r = env.call(http.MethodGet, "/api/v1/me/media-position?path=/lama/000.mp4", ip, nil, nil, ck); r.body["updatedAt"] != nil {
		t.Fatalf("oldest row survived: %v", r.body)
	}
	if r = env.call(http.MethodGet, "/api/v1/me/media-position?path=/lama/001.mp4", ip, nil, nil, ck); r.body["seconds"] != float64(10) {
		t.Fatalf("second oldest pruned: %v", r.body)
	}
	if r = env.call(http.MethodGet, "/api/v1/me/media-position?path=/baru.mp4", ip, nil, nil, ck); r.body["seconds"] != float64(5) {
		t.Fatalf("new row: %v", r.body)
	}
}
