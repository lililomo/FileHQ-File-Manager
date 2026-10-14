package httpapi

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/config"
	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/download"
	"kppbwi.app/filemanager/internal/exec"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/listing"
	"kppbwi.app/filemanager/internal/media"
	"kppbwi.app/filemanager/internal/thumbs"
)

const (
	testVips     = "/usr/bin/vipsthumbnail"
	testFFmpeg   = "/usr/bin/ffmpeg"
	testFFprobe  = "/usr/bin/ffprobe"
	testPdftoppm = "/usr/bin/pdftoppm"
)

// withThumbs rebuilds the env handler with the media/thumbnail pipeline.
// ffmpeg="" simulates a host without ffmpeg.
func withThumbs(t *testing.T, env *authEnv, ffmpeg string) *thumbs.Service {
	t.Helper()
	if !exec.Executable(testVips) {
		t.Skip("vipsthumbnail not installed")
	}
	runner := exec.New(filepath.Join(env.cfg.DataDir, "tmp"))
	prober := &media.Prober{Runner: runner, FFprobe: testFFprobe, Vipsheader: media.VipsheaderPath(testVips)}
	mediaSvc := media.New(env.store, env.store, prober, nil)
	svc, err := thumbs.New(thumbs.Config{Workers: 1, Vips: testVips, FFmpeg: ffmpeg, FFprobe: testFFprobe, Pdftoppm: testPdftoppm,
		StorageRoot: env.cfg.StorageRoot, DataDir: env.cfg.DataDir}, runner, env.store, mediaSvc, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	svc.Start(ctx)
	t.Cleanup(func() {
		cancel()
		svc.Wait()
		_ = svc.Close()
	})
	env.listing = listing.New(listing.Options{Secret: env.cfg.AppSecret, PageSize: env.cfg.ListingPageSize, Meta: mediaSvc})
	env.files = files.New(files.Options{Repo: env.store, Locks: env.locks, Trash: env.trash,
		OnChange: func(ctx context.Context, userID string, p fsjail.VPath, isDir bool) {
			_ = svc.Invalidate(ctx, userID, p, isDir)
		},
		Invalidate: func(userID string, dir fsjail.VPath) { env.listing.Invalidate(userID, "", dir) }})
	env.handler = New(Options{Cfg: env.cfg, Logger: config.NewLogger(env.logs, env.cfg.AppEnv, slog.LevelDebug), Version: "test", Store: env.store, Auth: env.auth,
		Listing: env.listing, Locks: env.locks, Files: env.files,
		Media: mediaSvc, Thumbs: svc, Signer: download.NewSigner(env.cfg.AppSecret, nil),
		Warnings: func() []string {
			var out []string
			for _, m := range svc.MissingTools() {
				out = append(out, "dependency missing: "+m)
			}
			return out
		},
		Assets: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("spa")) })})
	return svc
}

func testPNG(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x * 3), uint8(y * 5), 90, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func signBatch(t *testing.T, env *authEnv, ck *http.Cookie, hdr map[string]string, size int, paths ...string) map[string]string {
	t.Helper()
	r := env.call(http.MethodPost, "/api/v1/files/sign", "203.0.113.5:1", map[string]any{"paths": paths, "purpose": "thumb", "size": size}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("sign: %d %s", r.rec.Code, r.rec.Body.String())
	}
	out := map[string]string{}
	for _, e := range r.body["urls"].([]any) {
		m := e.(map[string]any)
		out[m["path"].(string)] = m["url"].(string)
	}
	return out
}

func waitThumb(t *testing.T, env *authEnv, u string) resp {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		r := env.call(http.MethodGet, u, "198.51.100.7:1", nil, nil, nil) // no cookie: signature only
		if r.rec.Code != http.StatusAccepted {
			return r
		}
		if r.rec.Header().Get("Retry-After") != "2" {
			t.Fatalf("202 without Retry-After: %v", r.rec.Header())
		}
		if time.Now().After(deadline) {
			t.Fatal("thumbnail never became ready")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestThumbEndpointLifecycle(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	withThumbs(t, env, testFFmpeg)
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	if err := os.MkdirAll(filepath.Join(home, "foto"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "foto", "a.png"), testPNG(t, 300, 200), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "catatan.txt"), []byte("teks"), 0o640); err != nil {
		t.Fatal(err)
	}

	urls := signBatch(t, env, ck, hdr, 256, "/foto/a.png", "/catatan.txt", "/../x")
	if _, ok := urls["/foto/a.png"]; !ok || len(urls) != 2 {
		t.Fatalf("urls = %v", urls)
	}
	// first request queues the job → 202, then 200 image/webp with validators
	r := waitThumb(t, env, urls["/foto/a.png"])
	if r.rec.Code != 200 {
		t.Fatalf("thumb: %d %s", r.rec.Code, r.rec.Body.String())
	}
	h := r.rec.Header()
	if h.Get("Content-Type") != "image/webp" || h.Get("Cache-Control") != "private, max-age=86400" ||
		h.Get("Content-Security-Policy") != RawCSP || h.Get("ETag") == "" || h.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("headers: %v", h)
	}
	body := r.rec.Body.Bytes()
	if len(body) < 12 || string(body[:4]) != "RIFF" || string(body[8:12]) != "WEBP" {
		t.Fatalf("body is not webp: %q", body[:min(12, len(body))])
	}
	// conditional request → 304
	r2 := env.call(http.MethodGet, urls["/foto/a.png"], "198.51.100.7:1", nil, map[string]string{"If-None-Match": h.Get("ETag")}, nil)
	if r2.rec.Code != http.StatusNotModified || r2.rec.Body.Len() != 0 {
		t.Fatalf("304 expected, got %d %d bytes", r2.rec.Code, r2.rec.Body.Len())
	}
	// other sizes were rendered by the same job
	for _, size := range []int{128, 512} {
		u := signBatch(t, env, ck, hdr, size, "/foto/a.png")["/foto/a.png"]
		if r := env.call(http.MethodGet, u, "198.51.100.7:1", nil, nil, nil); r.rec.Code != 200 {
			t.Fatalf("size %d: %d", size, r.rec.Code)
		}
	}
	// unsupported type → 404 (client shows the icon)
	if r := env.call(http.MethodGet, urls["/catatan.txt"], "198.51.100.7:1", nil, nil, nil); r.rec.Code != 404 || r.code(t) != "NOT_FOUND" {
		t.Fatalf("txt: %d %s", r.rec.Code, r.rec.Body.String())
	}

	// file replaced (new mtime) → old URL still signed for the path, new content rendered
	time.Sleep(20 * time.Millisecond)
	if err := os.WriteFile(filepath.Join(home, "foto", "a.png"), testPNG(t, 40, 40), 0o640); err != nil {
		t.Fatal(err)
	}
	r3 := waitThumb(t, env, urls["/foto/a.png"])
	if r3.rec.Code != 200 || r3.rec.Header().Get("ETag") == h.Get("ETag") {
		t.Fatalf("changed file must get a new thumbnail/ETag: %d %s", r3.rec.Code, r3.rec.Header().Get("ETag"))
	}

	// listing with media=1 fills dimensions and keeps thumb=true
	l := env.call(http.MethodGet, "/api/v1/files/list?path=/foto&media=1", "203.0.113.5:1", nil, nil, ck)
	if l.rec.Code != 200 {
		t.Fatalf("list: %d %s", l.rec.Code, l.rec.Body.String())
	}
	it := l.body["items"].([]any)[0].(map[string]any)
	m, _ := it["media"].(map[string]any)
	if m == nil || m["width"] != float64(40) || m["height"] != float64(40) || it["thumb"] != true {
		t.Fatalf("media annotation: %v", it)
	}
}

func TestThumbSignatureChecks(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	withThumbs(t, env, testFFmpeg)
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	if err := os.WriteFile(filepath.Join(home, "b.png"), testPNG(t, 20, 20), 0o640); err != nil {
		t.Fatal(err)
	}
	good := signBatch(t, env, ck, hdr, 128, "/b.png")["/b.png"]
	parsed, _ := url.Parse(good)
	q := parsed.Query()

	mut := func(change func(url.Values)) string {
		c := url.Values{}
		for k, v := range q {
			c[k] = v
		}
		change(c)
		return parsed.Path + "?" + c.Encode()
	}
	cases := map[string]struct {
		u    string
		code string
	}{
		"tampered sig":  {mut(func(v url.Values) { v.Set("sig", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA") }), "SIGNATURE_INVALID"},
		"other path":    {mut(func(v url.Values) { v.Set("path", "/c.png") }), "SIGNATURE_INVALID"},
		"other size":    {mut(func(v url.Values) { v.Set("size", "512") }), "SIGNATURE_INVALID"},
		"other scope":   {mut(func(v url.Values) { v.Set("scope", "u:someone-else") }), "SIGNATURE_INVALID"},
		"purpose swap":  {mut(func(v url.Values) { v.Set("purpose", "download") }), "SIGNATURE_INVALID"},
		"missing sig":   {mut(func(v url.Values) { v.Del("sig") }), "SIGNATURE_INVALID"},
		"expired (exp)": {mut(func(v url.Values) { v.Set("exp", "1") }), "SIGNATURE_INVALID"},
	}
	for name, c := range cases {
		r := env.call(http.MethodGet, c.u, "198.51.100.7:1", nil, nil, nil)
		if r.rec.Code != 403 || r.code(t) != c.code {
			t.Errorf("%s: %d %s", name, r.rec.Code, r.rec.Body.String())
		}
	}
	// a genuinely expired signature (signed with exp in the past)
	signer := download.NewSigner(env.cfg.AppSecret, nil)
	u, _ := env.store.GetUserByUsername(context.Background(), "admin")
	expired := "/api/v1/files/thumb?" + signer.Query(download.Params{Scope: download.UserScope(u.ID), Path: "/b.png", Purpose: download.PurposeThumb, Extra: "128", Exp: time.Now().Add(-time.Minute).Unix()}).Encode()
	if r := env.call(http.MethodGet, expired, "198.51.100.7:1", nil, nil, nil); r.rec.Code != 403 || r.code(t) != "SIGNATURE_EXPIRED" {
		t.Fatalf("expired: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// invalid size signed is impossible via API; a hand-made one is rejected
	bad := "/api/v1/files/thumb?" + signer.Query(download.Params{Scope: download.UserScope(u.ID), Path: "/b.png", Purpose: download.PurposeThumb, Extra: "64", Exp: time.Now().Add(time.Minute).Unix()}).Encode()
	if r := env.call(http.MethodGet, bad, "198.51.100.7:1", nil, nil, nil); r.rec.Code != 400 {
		t.Fatalf("bad size: %d", r.rec.Code)
	}
	// sign endpoint requires CSRF and only supports thumb for now
	if r := env.call(http.MethodPost, "/api/v1/files/sign", "203.0.113.5:1", map[string]any{"path": "/b.png", "purpose": "thumb"}, nil, ck); r.rec.Code != 403 {
		t.Fatalf("sign without csrf: %d", r.rec.Code)
	}
	// Step 12b: other purposes are signed too, each on its own route
	if r := env.call(http.MethodPost, "/api/v1/files/sign", "203.0.113.5:1", map[string]any{"path": "/b.png", "purpose": "download"}, hdr, ck); r.rec.Code != 200 || !strings.Contains(r.rec.Body.String(), "/api/v1/files/download?") {
		t.Fatalf("sign download: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := env.call(http.MethodPost, "/api/v1/files/sign", "203.0.113.5:1", map[string]any{"path": "/b.png", "purpose": "thumb", "size": 999}, hdr, ck); r.rec.Code != 400 {
		t.Fatalf("sign bad size: %d", r.rec.Code)
	}
	// disabled user's signed URLs stop working
	if err := env.store.UpdateUserStatus(context.Background(), sqlc.UpdateUserStatusParams{Status: "disabled", UpdatedAt: db.Now(), ID: u.ID}); err != nil {
		t.Fatal(err)
	}
	if r := env.call(http.MethodGet, good, "198.51.100.7:1", nil, nil, nil); r.rec.Code != 404 {
		t.Fatalf("disabled user: %d", r.rec.Code)
	}
}

func TestThumbWithoutFFmpegDegrades(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	withThumbs(t, env, "") // no ffmpeg on this "host"
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	// an mp4 by extension (content irrelevant: the tool is missing anyway)
	if err := os.WriteFile(filepath.Join(home, "film.mp4"), []byte("not really a video"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "gambar.png"), testPNG(t, 10, 10), 0o640); err != nil {
		t.Fatal(err)
	}
	urls := signBatch(t, env, ck, hdr, 128, "/film.mp4", "/gambar.png")
	if r := env.call(http.MethodGet, urls["/film.mp4"], "198.51.100.7:1", nil, nil, nil); r.rec.Code != 404 {
		t.Fatalf("video without ffmpeg must be 404, got %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r := waitThumb(t, env, urls["/gambar.png"]); r.rec.Code != 200 {
		t.Fatalf("image thumbnails must still work: %d", r.rec.Code)
	}
	r := env.call(http.MethodGet, "/readyz", "127.0.0.1:1", nil, nil, nil)
	if r.rec.Code != 200 {
		t.Fatalf("readyz: %d %s", r.rec.Code, r.rec.Body.String())
	}
	w, _ := r.body["warnings"].([]any)
	if len(w) != 1 || !strings.Contains(w[0].(string), "ffmpeg") {
		t.Fatalf("warnings: %v", r.body["warnings"])
	}
	if strings.Contains(env.logs.String(), "panic") {
		t.Fatal("panic logged")
	}
}

func TestThumbLimiter(t *testing.T) {
	var l thumbLimiter
	for range thumbMaxParallelPerUser {
		if !l.acquire("u") {
			t.Fatal("acquire below limit failed")
		}
	}
	if l.acquire("u") {
		t.Fatal("limit not enforced")
	}
	if !l.acquire("v") {
		t.Fatal("limit must be per user")
	}
	l.release("u")
	if !l.acquire("u") {
		t.Fatal("release did not free a slot")
	}
	if !etagMatches(`W/"abc", "def"`, `"def"`) || etagMatches(`"abc"`, `"def"`) || !etagMatches("*", `"x"`) {
		t.Fatal("etagMatches")
	}
}
