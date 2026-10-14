package thumbs

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	osexec "os/exec"
	"path/filepath"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/exec"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/media"
)

// Integration test against the real tools on this host (vipsthumbnail,
// ffmpeg, ffprobe, pdftoppm). Skipped when a tool is missing.
const (
	vipsBin     = "/usr/bin/vipsthumbnail"
	ffmpegBin   = "/usr/bin/ffmpeg"
	ffprobeBin  = "/usr/bin/ffprobe"
	pdftoppmBin = "/usr/bin/pdftoppm"
)

func requireTools(t *testing.T) {
	t.Helper()
	for _, b := range []string{vipsBin, ffmpegBin, ffprobeBin, pdftoppmBin} {
		if !exec.Executable(b) {
			t.Skipf("%s not installed", b)
		}
	}
}

type env struct {
	store   *db.Store
	svc     *Service
	jail    *fsjail.Jail
	userID  string
	user    string
	storage string
	data    string
	ctx     context.Context
}

func newEnv(t *testing.T) *env {
	t.Helper()
	requireTools(t)
	root := t.TempDir()
	storage := filepath.Join(root, "storage")
	data := filepath.Join(storage, ".system")
	for _, d := range []string{filepath.Join(data, "db"), filepath.Join(data, "tmp"), filepath.Join(data, "thumbs"), filepath.Join(storage, "budi")} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	database, err := db.Open(filepath.Join(data, "db", "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	ctx := context.Background()
	if _, err := database.MigrateUp(ctx); err != nil {
		t.Fatal(err)
	}
	store := db.NewStore(database)
	now := db.Now()
	u, err := store.CreateUser(ctx, sqlc.CreateUserParams{
		ID: db.NewID(), Username: "budi", DisplayName: "Budi", Role: "user", Status: "active",
		PasswordHash: "x", QuotaBytes: 1 << 30, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := exec.New(filepath.Join(data, "tmp"))
	prober := &media.Prober{Runner: runner, FFprobe: ffprobeBin, Vipsheader: media.VipsheaderPath(vipsBin)}
	mediaSvc := media.New(store, store, prober, nil)
	svc, err := New(Config{Workers: 1, Vips: vipsBin, FFmpeg: ffmpegBin, FFprobe: ffprobeBin, Pdftoppm: pdftoppmBin,
		StorageRoot: storage, DataDir: data}, runner, store, mediaSvc, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.q.close(); svc.Wait(); _ = svc.Close() })
	j, err := fsjail.OpenUser(storage, "budi", u.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	return &env{store: store, svc: svc, jail: j, userID: u.ID, user: "budi", storage: storage, data: data, ctx: ctx}
}

func (e *env) write(t *testing.T, name string, data []byte) fsjail.Info {
	t.Helper()
	if err := os.WriteFile(filepath.Join(e.storage, e.user, name), data, 0o640); err != nil {
		t.Fatal(err)
	}
	info, err := e.jail.Lstat(fsjail.VPath("/" + name))
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 128, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// ffmpegMake generates small media fixtures at test time (nothing binary is
// committed): a 2 s H.264 mp4 and an mp3 with an attached cover.
func ffmpegMake(t *testing.T, dir string) (mp4, mp3 []byte) {
	t.Helper()
	cover := filepath.Join(dir, "cover.png")
	if err := os.WriteFile(cover, pngBytes(t, 200, 120), 0o640); err != nil {
		t.Fatal(err)
	}
	mp4Path, mp3Path := filepath.Join(dir, "v.mp4"), filepath.Join(dir, "a.mp3")
	run := func(args ...string) {
		cmd := osexec.Command(ffmpegBin, append([]string{"-nostdin", "-v", "error", "-y"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
		}
	}
	run("-f", "lavfi", "-i", "testsrc=size=320x240:rate=10", "-t", "2", "-c:v", "libx264", "-pix_fmt", "yuv420p", mp4Path)
	run("-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-i", cover, "-map", "0:a", "-map", "1:v",
		"-c:a", "libmp3lame", "-c:v", "mjpeg", "-id3v2_version", "3", "-metadata", "title=Lagu Uji", "-metadata", "artist=Tester",
		"-disposition:v", "attached_pic", mp3Path)
	mp4, _ = os.ReadFile(mp4Path)
	mp3, _ = os.ReadFile(mp3Path)
	return mp4, mp3
}

const minimalPDF = "%PDF-1.1\n1 0 obj<</Type/Catalog/Pages 2 0 R>>endobj\n2 0 obj<</Type/Pages/Kids[3 0 R]/Count 1>>endobj\n3 0 obj<</Type/Page/Parent 2 0 R/MediaBox[0 0 200 100]>>endobj\ntrailer<</Root 1 0 R>>"
const svgDoc = `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="50"><rect width="100" height="50" fill="red"/><script>alert(1)</script></svg>`

func (e *env) waitReady(t *testing.T, info fsjail.Info) string {
	t.Helper()
	key := Key(string(info.Path), info.Size, info.ModTime.UnixNano())
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := e.svc.Lookup(e.userID, key, Size512); ok {
			if row, ok := e.svc.media.Get(e.ctx, e.userID, info); ok && row.ThumbState == media.ThumbReady {
				return key
			}
		}
		if row, ok := e.svc.media.Get(e.ctx, e.userID, info); ok && (row.ThumbState == media.ThumbFailed || row.ThumbState == media.ThumbUnsupported) {
			t.Fatalf("%s: state %s error %v", info.Name, row.ThumbState, deref(row.ThumbError))
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("%s: thumbnail not ready in time", info.Name)
	return ""
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func isWebP(t *testing.T, path string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 12 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		t.Fatalf("%s is not WebP (%q)", path, b[:min(12, len(b))])
	}
}

func TestPipelineRendersEveryKind(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	e.svc.Start(ctx)

	mp4, mp3 := ffmpegMake(t, t.TempDir())
	fixtures := map[string][]byte{
		"foto.png": pngBytes(t, 640, 480),
		"logo.svg": []byte(svgDoc),
		"doc.pdf":  []byte(minimalPDF),
		"film.mp4": mp4,
		"lagu.mp3": mp3,
	}
	infos := map[string]fsjail.Info{}
	for name, data := range fixtures {
		infos[name] = e.write(t, name, data)
	}
	for name, info := range infos {
		st := e.svc.Request(ctx, Job{UserID: e.userID, Username: e.user, Path: info.Path}, info, Size256)
		if st != StatusPending {
			t.Fatalf("%s: first request = %s, want pending", name, st)
		}
	}
	for name, info := range infos {
		key := e.waitReady(t, info)
		for _, size := range Sizes {
			fi, ok := e.svc.Lookup(e.userID, key, size)
			if !ok {
				t.Fatalf("%s: size %d missing", name, size)
			}
			isWebP(t, filepath.Join(e.data, "thumbs", CacheRel(e.userID, key, size)))
			if fi.Size() == 0 {
				t.Fatalf("%s: empty thumbnail", name)
			}
		}
		if st := e.svc.Request(ctx, Job{UserID: e.userID, Username: e.user, Path: info.Path}, info, Size128); st != StatusReady {
			t.Fatalf("%s: cached request = %s", name, st)
		}
	}
	// metadata rows
	row, ok := e.svc.media.Get(ctx, e.userID, infos["foto.png"])
	if !ok || row.Width == nil || *row.Width != 640 || *row.Height != 480 || row.Mime != "image/png" {
		t.Fatalf("png row = %+v", row)
	}
	row, _ = e.svc.media.Get(ctx, e.userID, infos["film.mp4"])
	if row.DurationMs == nil || *row.DurationMs < 1500 || *row.DurationMs > 2500 || row.Width == nil || *row.Width != 320 || deref(row.CodecVideo) != "h264" {
		t.Fatalf("mp4 row = %+v", row)
	}
	row, _ = e.svc.media.Get(ctx, e.userID, infos["lagu.mp3"])
	if row.HasCover != 1 || row.TagsJson == nil || media.TagsOf(row).Title != "Lagu Uji" {
		t.Fatalf("mp3 row = %+v", row)
	}
	row, _ = e.svc.media.Get(ctx, e.userID, infos["logo.svg"])
	if row.Mime != "image/svg+xml" {
		t.Fatalf("svg mime = %s", row.Mime)
	}
	// the SVG thumbnail is a raster: no "<script" inside the WebP
	svgKey := Key("/logo.svg", infos["logo.svg"].Size, infos["logo.svg"].ModTime.UnixNano())
	b, _ := os.ReadFile(filepath.Join(e.data, "thumbs", CacheRel(e.userID, svgKey, Size128)))
	if bytes.Contains(b, []byte("<script")) {
		t.Fatal("svg source leaked into thumbnail")
	}

	// changed file (new mtime) → new key, old thumbnails removed by Invalidate
	oldKey := Key("/foto.png", infos["foto.png"].Size, infos["foto.png"].ModTime.UnixNano())
	if err := e.svc.Invalidate(ctx, e.userID, "/foto.png", false); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.svc.Lookup(e.userID, oldKey, Size128); ok {
		t.Fatal("invalidate left cache files")
	}
	if _, ok := e.svc.media.Get(ctx, e.userID, infos["foto.png"]); ok {
		t.Fatal("invalidate left the media row")
	}
	time.Sleep(10 * time.Millisecond) // mtime resolution
	info2 := e.write(t, "foto.png", pngBytes(t, 32, 32))
	newKey := Key("/foto.png", info2.Size, info2.ModTime.UnixNano())
	if newKey == oldKey {
		t.Fatal("key must change with mtime/size")
	}
	if st := e.svc.Request(ctx, Job{UserID: e.userID, Username: e.user, Path: info2.Path}, info2, Size128); st != StatusPending {
		t.Fatalf("after change: %s", st)
	}
	e.waitReady(t, info2)
	row, _ = e.svc.media.Get(ctx, e.userID, info2)
	if row.Width == nil || *row.Width != 32 {
		t.Fatalf("recomputed row = %+v", row)
	}

	// directory invalidation drops rows and files below
	if err := e.svc.Invalidate(ctx, e.userID, "/", true); err != nil {
		t.Fatal(err)
	}
	if n, _ := e.store.CountMedia(ctx, e.userID); n != 0 {
		t.Fatalf("rows left after dir invalidate: %d", n)
	}
}

func TestUnsupportedAndBrokenFiles(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(e.ctx)
	defer cancel()
	e.svc.Start(ctx)
	txt := e.write(t, "catatan.txt", []byte("hello"))
	if st := e.svc.Request(ctx, Job{UserID: e.userID, Username: e.user, Path: txt.Path}, txt, Size128); st != StatusUnsupported {
		t.Fatalf("txt = %s", st)
	}
	broken := e.write(t, "rusak.jpg", []byte("this is not a jpeg at all"))
	if st := e.svc.Request(ctx, Job{UserID: e.userID, Username: e.user, Path: broken.Path}, broken, Size128); st != StatusPending {
		t.Fatalf("broken first = %s", st)
	}
	deadline := time.Now().Add(60 * time.Second)
	for {
		row, ok := e.svc.media.Get(ctx, e.userID, broken)
		if ok && row.ThumbState != media.ThumbPending {
			if row.ThumbState != media.ThumbFailed && row.ThumbState != media.ThumbUnsupported {
				t.Fatalf("broken state = %s", row.ThumbState)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("broken file never settled")
		}
		time.Sleep(50 * time.Millisecond)
	}
	// persisted failure: not re-queued
	if st := e.svc.Request(ctx, Job{UserID: e.userID, Username: e.user, Path: broken.Path}, broken, Size128); st == StatusPending {
		t.Fatal("failed file was queued again")
	}
	// audio without cover → unsupported
	noCover := filepath.Join(t.TempDir(), "n.mp3")
	cmd := osexec.Command(ffmpegBin, "-nostdin", "-v", "error", "-y", "-f", "lavfi", "-i", "sine=frequency=440:duration=1", "-c:a", "libmp3lame", noCover)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	raw, _ := os.ReadFile(noCover)
	nc := e.write(t, "nocover.mp3", raw)
	e.svc.Request(ctx, Job{UserID: e.userID, Username: e.user, Path: nc.Path}, nc, Size128)
	deadline = time.Now().Add(60 * time.Second)
	for {
		row, ok := e.svc.media.Get(ctx, e.userID, nc)
		if ok && row.ThumbState == media.ThumbUnsupported {
			if row.DurationMs == nil || *row.DurationMs < 900 {
				t.Fatalf("duration missing for audio: %+v", row)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no-cover audio state: %+v ok=%v", row, ok)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestMissingToolsReported(t *testing.T) {
	svc, err := New(Config{Vips: "/nonexistent/vips", FFmpeg: "", FFprobe: "/nonexistent/ffprobe", Pdftoppm: "/nonexistent/pdftoppm", DataDir: t.TempDir(), StorageRoot: t.TempDir()},
		exec.New(t.TempDir()), nil, media.New(nil, nil, &media.Prober{}, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = svc.Close() }()
	missing := svc.MissingTools()
	if len(missing) != 4 {
		t.Fatalf("missing = %v", missing)
	}
}
