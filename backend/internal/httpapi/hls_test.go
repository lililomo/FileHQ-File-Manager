package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/download"
	fmexec "kppbwi.app/filemanager/internal/exec"
	"kppbwi.app/filemanager/internal/media"
)

// Step 23d: POST /files/hls and the signed playlist/segment routes against
// stub ffmpeg/ffprobe scripts (never the real ffmpeg).

const hlsStubFFmpeg = `#!/bin/sh
LOG=@DIR@
out=""; for a in "$@"; do out="$a"; done
dir=$(dirname "$out")
n=3; [ -f "$LOG/segments" ] && n=$(cat "$LOG/segments")
delay=0; [ -f "$LOG/delay" ] && delay=$(cat "$LOG/delay")
printf '#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:4\n#EXT-X-MEDIA-SEQUENCE:0\n#EXT-X-PLAYLIST-TYPE:EVENT\n' > "$dir/pl"
i=0
while [ $i -lt $n ]; do
  f=$(printf "$dir/seg_%05d.ts" $i)
  head -c 2048 /dev/zero > "$f.tmp"; mv "$f.tmp" "$f"
  printf '#EXTINF:4.000000,\n%s\n' "$(basename "$f")" >> "$dir/pl"
  cp "$dir/pl" "$out.tmp"; mv "$out.tmp" "$out"
  i=$((i+1))
  printf 'out_time_us=%d\nprogress=continue\n' $((i*4000000))
  [ "$delay" != "0" ] && sleep "$delay"
done
printf '#EXT-X-ENDLIST\n' >> "$dir/pl"; cp "$dir/pl" "$out.tmp"; mv "$out.tmp" "$out"
printf 'progress=end\n'
`

const hlsStubFFprobe = `#!/bin/sh
cat @DIR@/probe.json
`

const hlsProbeHEVC = `{"streams":[{"codec_type":"video","codec_name":"hevc","width":640,"height":360},{"codec_type":"audio","codec_name":"aac"}],"format":{"duration":"12.0"}}`

// writeHLSStubs writes the stub tools into a temp dir and returns it.
func writeHLSStubs(t testing.TB) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range map[string]string{"ffmpeg": hlsStubFFmpeg, "ffprobe": hlsStubFFprobe} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.ReplaceAll(body, "@DIR@", dir)), 0o755); err != nil { //nolint:gosec // executable stub
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "probe.json"), []byte(hlsProbeHEVC), 0o640); err != nil {
		t.Fatal(err)
	}
	return dir
}

func (e *authEnv) hlsMarker(t *testing.T, name, content string) {
	t.Helper()
	p := filepath.Join(e.hlsStub, name)
	if content == "" {
		_ = os.Remove(p)
		return
	}
	if err := os.WriteFile(p, []byte(content), 0o640); err != nil {
		t.Fatal(err)
	}
}

// rawGet issues a cookie-less GET (signed routes) and returns the recorder.
func (e *authEnv) rawGet(target string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.RemoteAddr = "203.0.113.30:1"
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	e.handler.ServeHTTP(rec, req)
	return rec
}

func errCode(rec *httptest.ResponseRecorder) (string, string) {
	var body struct {
		Error struct {
			Code    string         `json:"code"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	reason, _ := body.Error.Details["reason"].(string)
	return body.Error.Code, reason
}

func hlsTask(t *testing.T, r resp) (string, string) {
	t.Helper()
	task, _ := r.body["task"].(map[string]any)
	id, _ := task["id"].(string)
	u, _ := r.body["playlistUrl"].(string)
	if id == "" || !strings.HasPrefix(u, HLSPrefix+id+"/playlist.m3u8?") {
		t.Fatalf("bad start response: %d %s", r.rec.Code, r.rec.Body.String())
	}
	return id, u
}

func (e *authEnv) waitHLSState(t *testing.T, userID, id string, state string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if x, err := e.tasks.Get(context.Background(), userID, id); err == nil && string(x.State) == state {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	x, _ := e.tasks.Get(context.Background(), userID, id)
	t.Fatalf("task %s: %s, want %s (%+v)", id, x.State, state, x.Error)
}

func TestHLSHTTP(t *testing.T) {
	env := newAuthEnv(t, "TEST_HLS_STUB=1")
	ck, hdr := readyAdmin(t, env)
	ip := "203.0.113.5:1"
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	for _, f := range []string{"film.mkv", "other.mkv", "long.mkv"} {
		dupWrite(t, home, f, "fake video "+f)
	}
	dupWrite(t, home, "dir/x.txt", "x")
	me := env.call(http.MethodGet, "/api/v1/auth/me", ip, nil, hdr, ck)
	adminID := me.body["user"].(map[string]any)["id"].(string)
	if cfgMap := me.body["config"].(map[string]any); cfgMap["transcodeEnabled"] != false {
		t.Fatalf("transcodeEnabled before enabling: %v", cfgMap["transcodeEnabled"])
	}
	// disabled by default → 403 transcode_disabled
	r := env.call(http.MethodPost, "/api/v1/files/hls", ip, map[string]any{"path": "/film.mkv"}, hdr, ck)
	if c, reason := errCode(r.rec); r.rec.Code != 403 || c != "FORBIDDEN" || reason != media.ReasonDisabled {
		t.Fatalf("disabled: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := env.call(http.MethodPut, "/api/v1/admin/settings", ip, map[string]any{"transcode.enabled": true}, hdr, ck); r.rec.Code != 200 {
		t.Fatalf("enable: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if me := env.call(http.MethodGet, "/api/v1/auth/me", ip, nil, hdr, ck); me.body["config"].(map[string]any)["transcodeEnabled"] != true {
		t.Fatal("transcodeEnabled not exposed")
	}
	// validation
	for _, c := range []struct {
		target string
		body   any
		code   int
	}{
		{"/api/v1/files/hls", map[string]any{}, 400},
		{"/api/v1/files/hls", map[string]any{"path": "/dir"}, 400},
		{"/api/v1/files/hls", map[string]any{"path": "/missing.mkv"}, 404},
		{"/api/v1/files/hls?share=abc", map[string]any{"path": "/film.mkv"}, 400},
		{"/api/v1/files/hls", map[string]any{"path": "/film.mkv", "x": 1}, 400},
	} {
		if r := env.call(http.MethodPost, c.target, ip, c.body, hdr, ck); r.rec.Code != c.code {
			t.Errorf("%s %v: %d %s", c.target, c.body, r.rec.Code, r.rec.Body.String())
		}
	}
	// duration cap → 422 VALIDATION_FAILED too_long; audio → 415
	env.hlsMarker(t, "probe.json", `{"streams":[{"codec_type":"video","codec_name":"hevc","width":10,"height":10}],"format":{"duration":"7200"}}`)
	if r := env.call(http.MethodPut, "/api/v1/admin/settings", ip, map[string]any{"transcode.max_duration_minutes": 60}, hdr, ck); r.rec.Code != 200 {
		t.Fatalf("max duration: %d", r.rec.Code)
	}
	r = env.call(http.MethodPost, "/api/v1/files/hls", ip, map[string]any{"path": "/long.mkv"}, hdr, ck)
	if c, reason := errCode(r.rec); r.rec.Code != 422 || c != "VALIDATION_FAILED" || reason != media.ReasonTooLong || r.body["error"].(map[string]any)["details"].(map[string]any)["maxMinutes"] != float64(60) {
		t.Fatalf("too long: %d %s", r.rec.Code, r.rec.Body.String())
	}
	env.hlsMarker(t, "probe.json", `{"streams":[{"codec_type":"audio","codec_name":"mp3"}],"format":{"duration":"10"}}`)
	r = env.call(http.MethodPost, "/api/v1/files/hls", ip, map[string]any{"path": "/long.mkv"}, hdr, ck)
	if c, reason := errCode(r.rec); r.rec.Code != 415 || c != "UNSUPPORTED_TYPE" || reason != media.ReasonNotVideo {
		t.Fatalf("audio: %d %s", r.rec.Code, r.rec.Body.String())
	}
	env.hlsMarker(t, "probe.json", hlsProbeHEVC)

	// 202 new task; same file again while running → 200 same task; another file → 409
	env.hlsMarker(t, "delay", "0.4")
	r = env.call(http.MethodPost, "/api/v1/files/hls", ip, map[string]any{"path": "/film.mkv"}, hdr, ck)
	if r.rec.Code != 202 || r.body["ready"] != false {
		t.Fatalf("start: %d %s", r.rec.Code, r.rec.Body.String())
	}
	id, playlist := hlsTask(t, r)
	r = env.call(http.MethodPost, "/api/v1/files/hls", ip, map[string]any{"path": "/film.mkv"}, hdr, ck)
	if again, _ := hlsTask(t, r); r.rec.Code != 200 || again != id {
		t.Fatalf("same file: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodPost, "/api/v1/files/hls", ip, map[string]any{"path": "/other.mkv"}, hdr, ck)
	if c, reason := errCode(r.rec); r.rec.Code != 409 || c != "CONFLICT" || reason != "already_running" || r.body["error"].(map[string]any)["details"].(map[string]any)["taskId"] != id {
		t.Fatalf("per-user limit: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// while running: playlist with signed segment URIs, not cacheable
	var rec *httptest.ResponseRecorder
	deadline := time.Now().Add(5 * time.Second)
	for {
		rec = env.rawGet(playlist, nil)
		if rec.Code == 200 || time.Now().After(deadline) {
			break
		}
		if rec.Code != 202 || rec.Header().Get("Retry-After") != "2" {
			t.Fatalf("playlist warm-up: %d %s", rec.Code, rec.Body.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "private, no-store" || strings.Contains(rec.Body.String(), "#EXT-X-ENDLIST") {
		t.Fatalf("running playlist: %d %q %s", rec.Code, rec.Header().Get("Cache-Control"), rec.Body.String())
	}
	env.waitHLSState(t, adminID, id, "done")
	rec = env.rawGet(playlist, nil)
	pu, _ := url.Parse(playlist)
	q := pu.Query()
	body := rec.Body.String()
	h := rec.Header()
	if rec.Code != 200 || h.Get("Content-Type") != "application/vnd.apple.mpegurl" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Content-Security-Policy") != RawCSP || h.Get("Cache-Control") != "private, max-age=3600" || h.Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("finished playlist headers: %d %v", rec.Code, h)
	}
	if !strings.HasPrefix(body, "#EXTM3U\n#EXT-X-START:TIME-OFFSET=0\n") || !strings.Contains(body, "#EXT-X-ENDLIST") ||
		!strings.Contains(body, "\nseg_00000.ts?"+pu.RawQuery+"\n") || strings.Count(body, "?"+pu.RawQuery) != 3 {
		t.Fatalf("playlist body:\n%s", body)
	}
	// finished: POST returns the same task, ready, with a fresh URL
	r = env.call(http.MethodPost, "/api/v1/files/hls", ip, map[string]any{"path": "/film.mkv"}, hdr, ck)
	if again, _ := hlsTask(t, r); r.rec.Code != 200 || again != id || r.body["ready"] != true {
		t.Fatalf("finished reuse: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// segment: type, headers, Range
	seg := HLSPrefix + id + "/seg_00001.ts?" + pu.RawQuery
	rec = env.rawGet(seg, nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "video/mp2t" || rec.Body.Len() != 2048 || rec.Header().Get("Content-Security-Policy") != RawCSP ||
		rec.Header().Get("X-Content-Type-Options") != "nosniff" || rec.Header().Get("Cache-Control") != "private, max-age=3600" {
		t.Fatalf("segment: %d %v", rec.Code, rec.Header())
	}
	rec = env.rawGet(seg, map[string]string{"Range": "bytes=100-199"})
	if rec.Code != 206 || rec.Body.Len() != 100 || rec.Header().Get("Content-Range") != "bytes 100-199/2048" {
		t.Fatalf("range: %d %v", rec.Code, rec.Header())
	}
	// strict names, no directory listing
	for _, target := range []string{
		HLSPrefix + id + "/seg_1.ts?" + pu.RawQuery,
		HLSPrefix + id + "/seg_00009.ts?" + pu.RawQuery,
		HLSPrefix + id + "/seg_00000.ts.tmp?" + pu.RawQuery,
		HLSPrefix + id + "/pl?" + pu.RawQuery,
		HLSPrefix + id + "/?" + pu.RawQuery,
		HLSPrefix + id + "?" + pu.RawQuery,
	} {
		if rec := env.rawGet(target, nil); rec.Code != 404 {
			t.Errorf("%s: %d %s", target, rec.Code, rec.Body.String())
		}
	}
	// signatures: tampered, expired (like /files/raw), other user's scope
	signer := download.NewSigner(env.cfg.AppSecret, nil)
	sign := func(scope, path string, exp int64) string {
		v := url.Values{}
		v.Set("scope", scope)
		v.Set("exp", strconv.FormatInt(exp, 10))
		v.Set("sig", signer.Sign(download.Params{Scope: scope, Path: path, Purpose: download.PurposeHLS, Exp: exp}))
		return v.Encode()
	}
	bad := url.Values{"scope": {q.Get("scope")}, "exp": {q.Get("exp")}, "sig": {strings.Repeat("A", 32)}}
	if rec := env.rawGet(HLSPrefix+id+"/playlist.m3u8?"+bad.Encode(), nil); rec.Code != 403 {
		t.Fatalf("tampered: %d", rec.Code)
	} else if c, _ := errCode(rec); c != "SIGNATURE_INVALID" {
		t.Fatalf("tampered code %s", c)
	}
	expired := sign(q.Get("scope"), id, time.Now().Add(-time.Minute).Unix())
	for _, target := range []string{HLSPrefix + id + "/playlist.m3u8?" + expired, HLSPrefix + id + "/seg_00000.ts?" + expired} {
		rec := env.rawGet(target, nil)
		if c, _ := errCode(rec); rec.Code != 403 || c != "SIGNATURE_EXPIRED" {
			t.Fatalf("expired %s: %d %s", target, rec.Code, rec.Body.String())
		}
	}
	// a raw media signature does not open the HLS folder (purpose bound)
	mediaSig := signer.Query(download.Params{Scope: q.Get("scope"), Path: id, Purpose: download.PurposeMedia, Exp: time.Now().Add(time.Hour).Unix()})
	mediaSig.Del("path")
	mediaSig.Del("purpose")
	if rec := env.rawGet(HLSPrefix+id+"/playlist.m3u8?"+mediaSig.Encode(), nil); rec.Code != 403 {
		t.Fatalf("media signature accepted: %d", rec.Code)
	}
	// IDOR: user B with a valid signature of her own scope for A's task → 404
	ckB, hdrB := env.newUser(t, "budi", readyPassword)
	meB := env.call(http.MethodGet, "/api/v1/auth/me", ip, nil, hdrB, ckB)
	bID := meB.body["user"].(map[string]any)["id"].(string)
	foreign := sign(download.UserScope(bID), id, time.Now().Add(time.Hour).Unix())
	for _, name := range []string{"playlist.m3u8", "seg_00000.ts"} {
		if rec := env.rawGet(HLSPrefix+id+"/"+name+"?"+foreign, nil); rec.Code != 404 {
			t.Fatalf("foreign %s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	// B's own POST for A's path name resolves in B's jail (no such file)
	if r := env.call(http.MethodPost, "/api/v1/files/hls", ip, map[string]any{"path": "/film.mkv"}, hdrB, ckB); r.rec.Code != 404 {
		t.Fatalf("B POST: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// a non-HLS task id of the owner is not a folder
	cr := env.call(http.MethodPost, "/api/v1/archives/compress", ip, map[string]any{"paths": []string{"/dir"}, "format": "zip"}, hdr, ck)
	if cr.rec.Code == 202 {
		other := cr.body["task"].(map[string]any)["id"].(string)
		if rec := env.rawGet(HLSPrefix+other+"/playlist.m3u8?"+sign(q.Get("scope"), other, time.Now().Add(time.Hour).Unix()), nil); rec.Code != 404 {
			t.Fatalf("compress task as hls: %d", rec.Code)
		}
	}

	// cancel a running conversion → 404 afterwards, folder removed
	env.hlsMarker(t, "delay", "2")
	env.hlsMarker(t, "segments", "30")
	r = env.call(http.MethodPost, "/api/v1/files/hls", ip, map[string]any{"path": "/other.mkv"}, hdr, ck)
	cid, cplaylist := hlsTask(t, r)
	env.waitHLSState(t, adminID, cid, "running")
	if r := env.call(http.MethodPost, "/api/v1/tasks/"+cid+"/cancel", ip, map[string]any{}, hdr, ck); r.rec.Code != 200 {
		t.Fatalf("cancel: %d %s", r.rec.Code, r.rec.Body.String())
	}
	env.waitHLSState(t, adminID, cid, "cancelled")
	if rec := env.rawGet(cplaylist, nil); rec.Code != 404 {
		t.Fatalf("cancelled playlist: %d", rec.Code)
	}
	if _, err := os.Stat(filepath.Join(env.cfg.DataDir, "tmp", "hls", cid)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled folder: %v", err)
	}
	// switched off: signed routes answer 403 transcode_disabled, POST too
	if r := env.call(http.MethodPut, "/api/v1/admin/settings", ip, map[string]any{"transcode.enabled": false}, hdr, ck); r.rec.Code != 200 {
		t.Fatal("disable")
	}
	if rec := env.rawGet(playlist, nil); rec.Code != 403 {
		t.Fatalf("playlist while disabled: %d", rec.Code)
	} else if _, reason := errCode(rec); reason != media.ReasonDisabled {
		t.Fatalf("reason %s", reason)
	}
}

// TestHLSErrorMapping: every refusal reason → SPEC §6.2 code and status.
func TestHLSErrorMapping(t *testing.T) {
	s := &Server{}
	for _, c := range []struct {
		err    error
		status int
		code   string
	}{
		{&media.TranscodeError{Reason: media.ReasonDisabled}, 403, "FORBIDDEN"},
		{&media.TranscodeError{Reason: media.ReasonNotVideo}, 415, "UNSUPPORTED_TYPE"},
		{&media.TranscodeError{Reason: media.ReasonTooLong, DurationMs: 1, MaxMinutes: 180}, 422, "VALIDATION_FAILED"},
		{&media.TranscodeError{Reason: media.ReasonUnknownDuration}, 422, "VALIDATION_FAILED"},
		{&media.TranscodeError{Reason: media.ReasonDiskBudget}, 409, "CONFLICT"},
		{&media.TranscodeError{Reason: media.ReasonDiskFull}, 413, "QUOTA_EXCEEDED"},
		{&fmexec.MissingError{Bin: "ffprobe"}, 501, "DEPENDENCY_MISSING"},
		{&fmexec.ExitError{Bin: "ffprobe", Code: 1}, 415, "UNSUPPORTED_TYPE"},
	} {
		rec := httptest.NewRecorder()
		s.writeTranscodeError(rec, httptest.NewRequest(http.MethodPost, "/api/v1/files/hls", nil), c.err)
		if code, _ := errCode(rec); rec.Code != c.status || code != c.code {
			t.Errorf("%v: %d %s", c.err, rec.Code, code)
		}
	}
}

// TestHLSWithoutTools: no ffmpeg → 501 DEPENDENCY_MISSING, auth/me false.
func TestHLSWithoutTools(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	ip := "203.0.113.5:1"
	if r := env.call(http.MethodPut, "/api/v1/admin/settings", ip, map[string]any{"transcode.enabled": true}, hdr, ck); r.rec.Code != 200 {
		t.Fatal("enable")
	}
	dupWrite(t, filepath.Join(env.cfg.StorageRoot, "admin"), "a.mkv", "x")
	r := env.call(http.MethodPost, "/api/v1/files/hls", ip, map[string]any{"path": "/a.mkv"}, hdr, ck)
	if c, _ := errCode(r.rec); r.rec.Code != 501 || c != "DEPENDENCY_MISSING" {
		t.Fatalf("no tools: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if me := env.call(http.MethodGet, "/api/v1/auth/me", ip, nil, hdr, ck); me.body["config"].(map[string]any)["transcodeEnabled"] != false {
		t.Fatal("transcodeEnabled without tools")
	}
}
