package httpapi

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/webp"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/media"
)

// exifJPEG encodes a w×h JPEG and splices an APP1 segment after SOI with
// Make/Model/Orientation and a GPS IFD (little-endian TIFF, IFD0 at 8).
func exifJPEG(t *testing.T, w, h int, orientation uint16, withGPS bool) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 120, 255})
		}
	}
	var pic bytes.Buffer
	if err := jpeg.Encode(&pic, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatal(err)
	}
	bo := binary.LittleEndian
	// IFD0: Make(ASCII), Model(ASCII), Orientation(SHORT), [GPSIFD(LONG)]
	type entry struct {
		tag, typ uint16
		count    uint32
		inline   []byte
		out      []byte
	}
	mk := []byte("Canon\x00")
	md := []byte("EOS R6\x00")
	entries := []entry{
		{tag: 0x010F, typ: 2, count: uint32(len(mk)), out: mk},
		{tag: 0x0110, typ: 2, count: uint32(len(md)), out: md},
		{tag: 0x0112, typ: 3, count: 1, inline: []byte{byte(orientation), byte(orientation >> 8), 0, 0}},
	}
	ifd0Size := 2 + 12*(len(entries)+1) + 4
	gpsOff := 8 + ifd0Size
	gpsEntries := 4
	gpsSize := 2 + 12*gpsEntries + 4
	valuesOff := gpsOff
	if withGPS {
		valuesOff += gpsSize
		entries = append(entries, entry{tag: 0x8825, typ: 4, count: 1, inline: bo.AppendUint32(nil, uint32(gpsOff))})
	}
	tiff := make([]byte, valuesOff)
	copy(tiff, "II")
	bo.PutUint16(tiff[2:], 42)
	bo.PutUint32(tiff[4:], 8)
	var values []byte
	writeEntry := func(p int, e entry) {
		bo.PutUint16(tiff[p:], e.tag)
		bo.PutUint16(tiff[p+2:], e.typ)
		bo.PutUint32(tiff[p+4:], e.count)
		if e.out != nil {
			bo.PutUint32(tiff[p+8:], uint32(valuesOff+len(values)))
			values = append(values, e.out...)
		} else {
			copy(tiff[p+8:], e.inline)
		}
	}
	bo.PutUint16(tiff[8:], uint16(len(entries)))
	for i, e := range entries {
		writeEntry(10+i*12, e)
	}
	if withGPS {
		rat := func(pairs ...[2]uint32) []byte {
			b := make([]byte, 8*len(pairs))
			for i, p := range pairs {
				bo.PutUint32(b[i*8:], p[0])
				bo.PutUint32(b[i*8+4:], p[1])
			}
			return b
		}
		gps := []entry{
			{tag: 1, typ: 2, count: 2, inline: []byte{'S', 0, 0, 0}},
			{tag: 2, typ: 5, count: 3, out: rat([2]uint32{8, 1}, [2]uint32{13, 1}, [2]uint32{12, 1})},
			{tag: 3, typ: 2, count: 2, inline: []byte{'E', 0, 0, 0}},
			{tag: 4, typ: 5, count: 3, out: rat([2]uint32{114, 1}, [2]uint32{22, 1}, [2]uint32{30, 1})},
		}
		bo.PutUint16(tiff[gpsOff:], uint16(len(gps)))
		for i, e := range gps {
			writeEntry(gpsOff+2+i*12, e)
		}
	}
	tiff = append(tiff, values...)
	payload := append([]byte("Exif\x00\x00"), tiff...)
	var out bytes.Buffer
	out.Write(pic.Bytes()[:2]) // SOI
	out.Write([]byte{0xFF, 0xE1, byte((len(payload) + 2) >> 8), byte(len(payload) + 2)})
	out.Write(payload)
	out.Write(pic.Bytes()[2:])
	return out.Bytes()
}

func TestMediaInfoExifAndGPS(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	withThumbs(t, env, testFFmpeg)
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	if err := os.MkdirAll(filepath.Join(home, "foto"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "foto", "kamera.jpg"), exifJPEG(t, 300, 200, 6, true), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "foto", "polos.jpg"), exifJPEG(t, 40, 30, 1, false), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "catatan.txt"), []byte("teks"), 0o640); err != nil {
		t.Fatal(err)
	}

	// default: EXIF without the position, hasGps announces it
	r := env.call(http.MethodGet, "/api/v1/files/media-info?path=/foto/kamera.jpg", "203.0.113.5:1", nil, nil, ck)
	if r.rec.Code != 200 {
		t.Fatalf("media-info: %d %s", r.rec.Code, r.rec.Body.String())
	}
	if r.body["kind"] != "image" || r.body["mime"] != "image/jpeg" || r.body["width"] != float64(300) || r.body["height"] != float64(200) {
		t.Fatalf("basic fields: %v", r.body)
	}
	ex, _ := r.body["exif"].(map[string]any)
	if ex == nil || ex["make"] != "Canon" || ex["model"] != "EOS R6" || ex["orientation"] != float64(6) || ex["hasGps"] != true {
		t.Fatalf("exif: %v", ex)
	}
	if _, has := r.body["gps"]; has {
		t.Fatalf("gps must not be sent by default: %v", r.body)
	}
	if bytes.Contains(r.rec.Body.Bytes(), []byte(`"lat"`)) {
		t.Fatalf("latitude leaked: %s", r.rec.Body.String())
	}
	// include_gps=1 → position
	r = env.call(http.MethodGet, "/api/v1/files/media-info?path=/foto/kamera.jpg&include_gps=1", "203.0.113.5:1", nil, nil, ck)
	gps, _ := r.body["gps"].(map[string]any)
	if r.rec.Code != 200 || gps == nil || gps["lat"] != float64(-8.22) || gps["lon"] != float64(114.375) {
		t.Fatalf("gps: %d %v", r.rec.Code, r.body)
	}
	if r = env.call(http.MethodGet, "/api/v1/files/media-info?path=/foto/kamera.jpg&include_gps=maybe", "203.0.113.5:1", nil, nil, ck); r.rec.Code != 400 {
		t.Fatalf("bad include_gps: %d", r.rec.Code)
	}

	// EXIF without GPS → hasGps false; image without camera data keeps exif null
	r = env.call(http.MethodGet, "/api/v1/files/media-info?path=/foto/polos.jpg", "203.0.113.5:1", nil, nil, ck)
	ex, _ = r.body["exif"].(map[string]any)
	if r.rec.Code != 200 || ex == nil || ex["hasGps"] != false || ex["make"] != "Canon" {
		t.Fatalf("polos: %d %v", r.rec.Code, r.body)
	}
	if err := os.WriteFile(filepath.Join(home, "foto", "tanpa.png"), testPNG(t, 20, 20), 0o640); err != nil {
		t.Fatal(err)
	}
	r = env.call(http.MethodGet, "/api/v1/files/media-info?path=/foto/tanpa.png", "203.0.113.5:1", nil, nil, ck)
	if r.rec.Code != 200 || r.body["kind"] != "image" || r.body["exif"] != nil {
		t.Fatalf("png without exif: %d %v", r.rec.Code, r.body)
	}

	// non-media → kind other, exif null; folder → not_a_file; missing → 404
	r = env.call(http.MethodGet, "/api/v1/files/media-info?path=/catatan.txt", "203.0.113.5:1", nil, nil, ck)
	if r.rec.Code != 200 || r.body["kind"] != "other" || r.body["exif"] != nil || r.body["mime"] != "text/plain" {
		t.Fatalf("txt: %d %v", r.rec.Code, r.body)
	}
	r = env.call(http.MethodGet, "/api/v1/files/media-info?path=/foto", "203.0.113.5:1", nil, nil, ck)
	if r.rec.Code != 400 || r.code(t) != "VALIDATION_FAILED" {
		t.Fatalf("folder: %d %s", r.rec.Code, r.rec.Body.String())
	}
	r = env.call(http.MethodGet, "/api/v1/files/media-info?path=/foto/tidak-ada.jpg", "203.0.113.5:1", nil, nil, ck)
	if r.rec.Code != 404 {
		t.Fatalf("missing: %d", r.rec.Code)
	}
	r = env.call(http.MethodGet, "/api/v1/files/media-info?path=/../etc/passwd", "203.0.113.5:1", nil, nil, ck)
	if r.rec.Code != 400 || r.code(t) != "PATH_INVALID" {
		t.Fatalf("escape: %d %s", r.rec.Code, r.rec.Body.String())
	}
	// no cookie → 401
	if r = env.call(http.MethodGet, "/api/v1/files/media-info?path=/foto/kamera.jpg", "203.0.113.5:1", nil, nil, nil); r.rec.Code != 401 {
		t.Fatalf("anonymous: %d", r.rec.Code)
	}
	_ = hdr
}

// TestMediaInfoFillsLegacyRow: a media_metadata row written before Step 17a
// (exif_json NULL) gets its EXIF read from the file once and stored.
func TestMediaInfoFillsLegacyRow(t *testing.T) {
	env := newAuthEnv(t)
	ck, _ := readyAdmin(t, env)
	withThumbs(t, env, testFFmpeg)
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	path := filepath.Join(home, "lama.jpg")
	if err := os.WriteFile(path, exifJPEG(t, 50, 40, 1, false), 0o640); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	u, err := env.store.GetUserByUsername(context.Background(), "admin")
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.UpsertMedia(context.Background(), sqlc.UpsertMediaMetadataParams{
		UserID: u.ID, Path: "/lama.jpg", Size: st.Size(), MtimeUnix: st.ModTime().UnixNano(), Mime: "image/jpeg",
		Width: db.Ptr(int64(50)), Height: db.Ptr(int64(40)), ThumbState: media.ThumbReady, ComputedAt: "2026-01-01T00:00:00.000Z",
	}); err != nil {
		t.Fatal(err)
	}
	r := env.call(http.MethodGet, "/api/v1/files/media-info?path=/lama.jpg", "203.0.113.5:1", nil, nil, ck)
	ex, _ := r.body["exif"].(map[string]any)
	if r.rec.Code != 200 || ex == nil || ex["make"] != "Canon" || ex["hasGps"] != false {
		t.Fatalf("legacy row: %d %v", r.rec.Code, r.body)
	}
	row, err := env.store.GetMedia(context.Background(), u.ID, "/lama.jpg")
	if err != nil || row.ExifJson == nil || !bytes.Contains([]byte(*row.ExifJson), []byte(`"make":"Canon"`)) || row.ThumbState != media.ThumbReady {
		t.Fatalf("exif must be stored without touching the rest of the row: %v %+v", err, row)
	}
}

// TestPreviewAppliesExifOrientation: vipsthumbnail auto-rotates, so the
// 2048 px preview of a 300×200 photo tagged orientation 6 is 200×300.
func TestPreviewAppliesExifOrientation(t *testing.T) {
	env := newAuthEnv(t)
	ck, hdr := readyAdmin(t, env)
	withThumbs(t, env, testFFmpeg)
	home := filepath.Join(env.cfg.StorageRoot, "admin")
	if err := os.WriteFile(filepath.Join(home, "putar.jpg"), exifJPEG(t, 300, 200, 6, false), 0o640); err != nil {
		t.Fatal(err)
	}
	r := env.call(http.MethodPost, "/api/v1/files/sign", "203.0.113.5:1", map[string]any{"path": "/putar.jpg", "purpose": "media"}, hdr, ck)
	if r.rec.Code != 200 {
		t.Fatalf("sign: %d %s", r.rec.Code, r.rec.Body.String())
	}
	u, _ := url.Parse(r.body["url"].(string))
	q := u.Query()
	q.Set("variant", "preview")
	u.RawQuery = q.Encode()
	pr := env.call(http.MethodGet, u.String(), "198.51.100.7:1", nil, nil, nil)
	if pr.rec.Code != 200 || pr.rec.Header().Get("Content-Type") != "image/webp" {
		t.Fatalf("preview: %d %s", pr.rec.Code, pr.rec.Body.String())
	}
	cfg, err := webp.DecodeConfig(bytes.NewReader(pr.rec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Width != 200 || cfg.Height != 300 {
		t.Fatalf("preview must be auto-rotated: got %dx%d", cfg.Width, cfg.Height)
	}
	// the stored dimensions stay as encoded (viewer shows the rotated preview)
	mi := env.call(http.MethodGet, "/api/v1/files/media-info?path=/putar.jpg", "203.0.113.5:1", nil, nil, ck)
	if mi.body["width"] != float64(300) || mi.body["height"] != float64(200) {
		t.Fatalf("media-info dims: %v", mi.body)
	}
}
