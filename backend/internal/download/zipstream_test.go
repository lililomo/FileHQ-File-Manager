package download

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/fsjail"
)

func memSource(entries ...Entry) Source {
	return func(ctx context.Context, yield func(Entry) bool) error {
		for _, e := range entries {
			if !yield(e) {
				return nil
			}
		}
		return nil
	}
}

func strEntry(name, content string) Entry {
	return Entry{Name: name, Size: int64(len(content)), ModTime: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), Open: func() (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(content)), nil
	}}
}

func TestWriteZipEntriesUTF8AndErrors(t *testing.T) {
	var buf bytes.Buffer
	src := memSource(
		strEntry("résumé ☕.txt", "hello"),
		Entry{Name: "dir", IsDir: true, ModTime: time.Now()},
		strEntry("dir/b.bin", strings.Repeat("x", 70000)),
		Entry{Name: "broken.txt", Open: func() (io.ReadCloser, error) { return nil, errors.New("permission denied") }},
		strEntry("/leading/slash.txt", "ok"),
	)
	st, err := WriteZip(context.Background(), &buf, src, MethodStore)
	if err != nil {
		t.Fatal(err)
	}
	if st.Files != 3 || st.Dirs != 1 || len(st.Skipped) != 1 {
		t.Fatalf("stats %+v", st)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]*zip.File{}
	for _, f := range zr.File {
		names[f.Name] = f
	}
	for _, n := range []string{"résumé ☕.txt", "dir/", "dir/b.bin", "leading/slash.txt", ErrorsEntry} {
		if names[n] == nil {
			t.Fatalf("missing entry %q in %v", n, keys(names))
		}
	}
	if names["résumé ☕.txt"].Flags&0x800 == 0 {
		t.Fatal("UTF-8 flag not set")
	}
	if names["dir/b.bin"].Method != zip.Store {
		t.Fatal("store expected")
	}
	rc, _ := names[ErrorsEntry].Open()
	errTxt, _ := io.ReadAll(rc)
	_ = rc.Close()
	if !strings.Contains(string(errTxt), "broken.txt: permission denied") {
		t.Fatalf("_ERRORS.txt: %s", errTxt)
	}
	rc, _ = names["dir/b.bin"].Open()
	data, _ := io.ReadAll(rc)
	_ = rc.Close()
	if len(data) != 70000 {
		t.Fatalf("content length %d", len(data))
	}

	// deflate variant
	buf.Reset()
	if _, err := WriteZip(context.Background(), &buf, memSource(strEntry("a.txt", strings.Repeat("abc", 10000))), MethodDeflate); err != nil {
		t.Fatal(err)
	}
	zr, _ = zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if zr.File[0].Method != zip.Deflate || zr.File[0].CompressedSize64 >= zr.File[0].UncompressedSize64 {
		t.Fatalf("deflate not applied: %+v", zr.File[0].FileHeader)
	}
}

func keys(m map[string]*zip.File) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// failWriter breaks after n bytes like a client that went away.
type failWriter struct{ left int }

func (f *failWriter) Write(p []byte) (int, error) {
	if f.left <= 0 {
		return 0, errors.New("broken pipe")
	}
	n := len(p)
	if n > f.left {
		n = f.left
	}
	f.left -= n
	return n, nil
}

func TestWriteZipStopsOnClientError(t *testing.T) {
	big := strings.Repeat("z", 4<<20)
	calls := 0
	src := func(ctx context.Context, yield func(Entry) bool) error {
		for i := 0; i < 100; i++ {
			calls++
			if !yield(strEntry("f"+string(rune('a'+i%26))+".txt", big)) {
				return nil
			}
		}
		return nil
	}
	_, err := WriteZip(context.Background(), &failWriter{left: 3 << 20}, src, MethodStore)
	if err == nil || !strings.Contains(err.Error(), "broken pipe") {
		t.Fatalf("err %v", err)
	}
	if calls > 5 {
		t.Fatalf("walk continued after the client left: %d entries", calls)
	}
	// context cancellation stops the walk too
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := WriteZip(ctx, io.Discard, src, MethodStore); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}

// tailWriter keeps the last n bytes of a stream (the central directory and
// the end-of-central-directory records live there).
type tailWriter struct {
	n    int64
	buf  []byte
	size int64
}

func (w *tailWriter) Write(p []byte) (int, error) {
	w.size += int64(len(p))
	w.buf = append(w.buf, p...)
	if int64(len(w.buf)) > w.n {
		w.buf = w.buf[int64(len(w.buf))-w.n:]
	}
	return len(p), nil
}

// zeroReader yields n zero bytes without allocating them.
type zeroReader struct{ left int64 }

func (z *zeroReader) Read(p []byte) (int, error) {
	if z.left <= 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > z.left {
		p = p[:z.left]
	}
	for i := range p {
		p[i] = 0
	}
	z.left -= int64(len(p))
	return len(p), nil
}

func TestWriteZip64ForLargeEntry(t *testing.T) {
	if testing.Short() || raceEnabled {
		t.Skip("streams 4 GiB through crc32; skipped with -short and under -race (make test-backend); run: go test -run TestWriteZip64 ./internal/download/")
	}
	const size = 4<<30 + 1
	src := memSource(Entry{Name: "huge.bin", Size: size, ModTime: time.Now(), Open: func() (io.ReadCloser, error) {
		return io.NopCloser(&zeroReader{left: size}), nil
	}})
	tw := &tailWriter{n: 1 << 16}
	st, err := WriteZip(context.Background(), tw, src, MethodStore)
	if err != nil {
		t.Fatal(err)
	}
	if st.Bytes != size || tw.size <= size {
		t.Fatalf("stats %+v total %d", st, tw.size)
	}
	// zip64 end of central directory record signature 0x06064b50
	sig := []byte{0x50, 0x4b, 0x06, 0x06}
	i := bytes.LastIndex(tw.buf, sig)
	if i < 0 {
		t.Fatal("no zip64 EOCD record: archive beyond 4 GiB is not zip64")
	}
	// central directory header: uncompressed size 0xFFFFFFFF with a zip64 extra field
	cd := bytes.Index(tw.buf, []byte{0x50, 0x4b, 0x01, 0x02})
	if cd < 0 {
		t.Fatal("no central directory in tail")
	}
	uncompressed := binary.LittleEndian.Uint32(tw.buf[cd+24:])
	if uncompressed != 0xFFFFFFFF {
		t.Fatalf("central directory uncompressed size %#x, want zip64 marker", uncompressed)
	}
}

func TestJailSourceSkipsSymlinksAndNamesRelativeToParent(t *testing.T) {
	root := t.TempDir()
	mk := func(rel, content string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	mk("proyek/a.txt", "A")
	mk("proyek/sub/deep/b.txt", "B")
	mk("proyek/.hidden", "H")
	mk("lain.txt", "L")
	if err := os.Symlink("a.txt", filepath.Join(root, "proyek", "link.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc", filepath.Join(root, "proyek", "escape")); err != nil {
		t.Fatal(err)
	}
	j, err := fsjail.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()

	var buf bytes.Buffer
	st, err := WriteZip(context.Background(), &buf, JailSource(j, []fsjail.VPath{"/proyek", "/lain.txt", "/missing.txt"}, j.Redact), MethodStore)
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range zr.File {
		got[f.Name] = true
	}
	for _, want := range []string{"proyek/", "proyek/a.txt", "proyek/sub/", "proyek/sub/deep/", "proyek/sub/deep/b.txt", "proyek/.hidden", "lain.txt", ErrorsEntry} {
		if !got[want] {
			t.Fatalf("missing %q in %v", want, got)
		}
	}
	for n := range got {
		if strings.Contains(n, "link") || strings.Contains(n, "escape") || strings.Contains(n, root) {
			t.Fatalf("unexpected entry %q", n)
		}
	}
	if st.Files != 4 {
		t.Fatalf("files %d (%+v)", st.Files, st)
	}
	joined := strings.Join(st.Skipped, "\n")
	if !strings.Contains(joined, "proyek/link.txt: symlink skipped") || !strings.Contains(joined, "proyek/escape: symlink skipped") || !strings.Contains(joined, "missing.txt") {
		t.Fatalf("skipped: %q", joined)
	}
	if strings.Contains(joined, root) {
		t.Fatalf("host path leaked: %q", joined)
	}

	// single folder: entries start with the folder name; single file: just the name
	buf.Reset()
	_, _ = WriteZip(context.Background(), &buf, JailSource(j, []fsjail.VPath{"/proyek/sub"}, j.Redact), MethodStore)
	zr, _ = zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	if zr.File[0].Name != "sub/" {
		t.Fatalf("single folder first entry %q", zr.File[0].Name)
	}
}

func TestCommonParentAndZipFilename(t *testing.T) {
	if commonParent([]fsjail.VPath{"/a/b/x.txt", "/a/b/c/y.txt"}) != "/a/b" {
		t.Fatal("common parent")
	}
	if commonParent([]fsjail.VPath{"/a/x.txt", "/b/y.txt"}) != "/" {
		t.Fatal("root parent")
	}
	if commonParent([]fsjail.VPath{"/x.txt"}) != "/" {
		t.Fatal("top-level parent")
	}
	now := time.Date(2026, 10, 14, 15, 4, 0, 0, time.UTC)
	if ZipFilename("File Manager", []fsjail.VPath{"/proyek"}, "", now) != "proyek.zip" {
		t.Fatal("folder name")
	}
	if ZipFilename("File Manager", []fsjail.VPath{"/a.txt", "/b.txt"}, "", now) != "file-manager-20261014-1504.zip" {
		t.Fatalf("app name: %s", ZipFilename("File Manager", []fsjail.VPath{"/a.txt", "/b.txt"}, "", now))
	}
	if ZipFilename("x", []fsjail.VPath{"/a.txt", "/b.txt"}, "pilihan.zip", now) != "pilihan.zip" {
		t.Fatal("explicit")
	}
	if ParseMethodMust("") != MethodStore || ParseMethodMust("DEFLATE") != MethodDeflate {
		t.Fatal("method parse")
	}
	if _, ok := ParseMethod("lzma"); ok {
		t.Fatal("lzma accepted")
	}
}

func ParseMethodMust(s string) Method {
	m, _ := ParseMethod(s)
	return m
}

func TestZipStoreRoundTripAndIsolation(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "tmp"), 0o750); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 14, 9, 0, 0, 0, time.UTC)
	st, err := NewZipStore(dir, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	id, err := st.Put(ZipRequest{UserID: "u1", Paths: []string{"/a", "/b"}, Method: MethodStore})
	if err != nil {
		t.Fatal(err)
	}
	if len(id) != 32 {
		t.Fatalf("id %q", id)
	}
	req, err := st.Get(id, "u1")
	if err != nil || len(req.Paths) != 2 || req.UserID != "u1" {
		t.Fatalf("get: %+v %v", req, err)
	}
	if _, err := st.Get(id, "u2"); !errors.Is(err, ErrZipRequestNotFound) {
		t.Fatalf("foreign user: %v", err)
	}
	if _, err := st.Get("../../etc/passwd", "u1"); !errors.Is(err, ErrZipRequestNotFound) {
		t.Fatalf("traversal id: %v", err)
	}
	if _, err := st.Get(strings.Repeat("0", 32), "u1"); !errors.Is(err, ErrZipRequestNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	now = now.Add(ZipPayloadTTL + time.Second)
	if _, err := st.Get(id, "u1"); !errors.Is(err, ErrZipRequestNotFound) {
		t.Fatalf("expired: %v", err)
	}
	// the sweep (at most once a minute) removes files whose mtime is older than the TTL on the next Put
	st.lastSweep = now.Add(-2 * time.Minute)
	old := time.Now().Add(-ZipPayloadTTL - time.Minute)
	if err := os.Chtimes(filepath.Join(dir, "tmp", "zip-"+id+".json"), old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Put(ZipRequest{UserID: "u1", Paths: []string{"/c"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tmp", "zip-"+id+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale payload not swept: %v", err)
	}
}
