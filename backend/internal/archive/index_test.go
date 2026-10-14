package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/fsjail"
)

func (e *env) jail(t *testing.T) *fsjail.Jail {
	t.Helper()
	j, err := fsjail.OpenUser(e.cfg.StorageRoot, e.user.Username, e.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	return j
}

// TestIndexTree: implicit parents, explicit folder arriving after its
// children, folders-first natural order, Lookup/Members/Page and the
// NormalizeInner rules (backslashes, "./", ".." refused, unsafe counted).
func TestIndexTree(t *testing.T) {
	z := buildZip(t, []zipEntry{
		{name: "docs/b10.txt", content: []byte("B10")},
		{name: "docs/b2.txt", content: []byte("B2")},
		{name: "docs/", mode: 0o755 | os.ModeDir},
		{name: "docs/sub/deep.txt", content: []byte("D")},
		{name: "Zeta.txt", content: []byte("Z")},
		{name: "alpha.txt", content: []byte("a")},
		{name: `win\style\x.txt`, content: []byte("w")},
		{name: "../escape.txt", content: []byte("e")},
		{name: "./dot/y.txt", content: []byte("y")},
	})
	ix := indexFromZip(t, z)
	if ix.Unsafe != 1 {
		t.Fatalf("unsafe = %d, want 1 (../escape.txt)", ix.Unsafe)
	}
	root, ok := ix.Children("")
	if !ok {
		t.Fatal("root is a folder")
	}
	var names []string
	for _, e := range root {
		names = append(names, e.Base)
	}
	if got := strings.Join(names, ","); got != "docs,dot,win,alpha.txt,Zeta.txt" {
		t.Fatalf("root order: %s", got)
	}
	docs, _ := ix.Children("docs")
	names = names[:0]
	for _, e := range docs {
		names = append(names, e.Base)
	}
	if got := strings.Join(names, ","); got != "sub,b2.txt,b10.txt" {
		t.Fatalf("natural order in docs: %s", got)
	}
	d, _ := ix.Lookup("docs")
	if d.Implicit || !d.IsDir || d.Children != 3 {
		t.Fatalf("explicit docs folder: %+v", d)
	}
	sub, _ := ix.Lookup("docs/sub")
	if !sub.Implicit || sub.Children != 1 {
		t.Fatalf("implicit sub folder: %+v", sub)
	}
	if _, ok := ix.Children("docs/b2.txt"); ok {
		t.Fatal("a file is not a folder")
	}
	if m := ix.Members("docs"); len(m) != 5 {
		t.Fatalf("members of docs = %d, want 5 (docs, 2 files, sub, deep)", len(m))
	}
	if w, ok := ix.Lookup("win/style/x.txt"); !ok || w.Size != 1 {
		t.Fatalf("backslash name normalised: %+v %v", w, ok)
	}
	if _, ok := ix.Lookup("dot/y.txt"); !ok {
		t.Fatal("./ prefix stripped")
	}
	page, next, err := Page(root, "", 2)
	if err != nil || len(page) != 2 || next != "o2" {
		t.Fatalf("page 1: %d %q %v", len(page), next, err)
	}
	page, next, err = Page(root, next, 2)
	if err != nil || len(page) != 2 || next != "o4" {
		t.Fatalf("page 2: %d %q %v", len(page), next, err)
	}
	page, next, err = Page(root, next, 2)
	if err != nil || len(page) != 1 || next != "" {
		t.Fatalf("page 3: %d %q %v", len(page), next, err)
	}
	if _, _, err := Page(root, "x9", 2); err == nil {
		t.Fatal("bad cursor accepted")
	}
	for raw, want := range map[string]string{"/a/b/": "a/b", `a\b`: "a/b", "./a/./b": "a/b", "": ""} {
		if got, ok := NormalizeInner(raw); !ok || got != want {
			t.Fatalf("NormalizeInner(%q) = %q %v", raw, got, ok)
		}
	}
	for _, raw := range []string{"../x", "a/../../b", "a\x00b"} {
		if _, ok := NormalizeInner(raw); ok {
			t.Fatalf("NormalizeInner(%q) accepted", raw)
		}
	}
}

func indexFromZip(t *testing.T, z []byte) *Index {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "ix-*.zip")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	if _, err := f.Write(z); err != nil {
		t.Fatal(err)
	}
	src, err := Open(f, int64(len(z)), FormatZip, OpenOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ix, err := BuildIndex(context.Background(), src, 0)
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

// TestEntriesCacheAndBigZip: AC "zip 50.000 entri: halaman pertama < 1 s,
// navigasi antar folder tanpa membaca ulang". The first call builds the
// index from the central directory, later calls (other folders, next
// pages) are served from the cache without touching the file.
var raceEnabled = false // set by race_test.go

func TestEntriesCacheAndBigZip(t *testing.T) {
	e := newEnv(t)
	j := e.jail(t)
	ctx := context.Background()
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	for i := 0; i < 50_000; i++ {
		name := "dir" + itoa(i%50) + "/f" + itoa(i) + ".txt"
		w, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("x"))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	e.write(t, "big.zip", buf.Bytes())
	start := time.Now()
	resp, err := e.svc.Entries(ctx, j, EntriesRequest{UserID: e.user.ID, Archive: "/big.zip"})
	first := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if resp.TotalEntries != 50_050 || resp.Total != 50 || len(resp.Items) != 50 || resp.Format != FormatZip || !resp.SupportsModify {
		t.Fatalf("root page: total=%d entries=%d items=%d %+v", resp.Total, resp.TotalEntries, len(resp.Items), resp)
	}
	t.Logf("50k entries: first page (index build) %v, race=%v", first, raceEnabled)
	if !raceEnabled && first > time.Second {
		t.Fatalf("first page took %s (> 1 s)", first)
	}
	// navigation: same Index object, no second read of the file
	var cached *Index
	for _, ix := range e.svc.index.m {
		cached = ix
	}
	start = time.Now()
	in, err := e.svc.Entries(ctx, j, EntriesRequest{UserID: e.user.ID, Archive: "/big.zip", Inner: "dir7"})
	nav := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	var after *Index
	for _, ix := range e.svc.index.m {
		after = ix
	}
	if cached == nil || after != cached {
		t.Fatal("navigation rebuilt the index")
	}
	if in.Total != 1000 || len(in.Items) != 1000 || in.Inner != "dir7" {
		t.Fatalf("dir7: total=%d items=%d inner=%q", in.Total, len(in.Items), in.Inner)
	}
	t.Logf("50k entries: navigation from cache %v", nav)
	if !raceEnabled && nav > 200*time.Millisecond {
		t.Fatalf("navigation took %s (cache miss?)", nav)
	}
	if len(e.svc.index.m) != 1 {
		t.Fatalf("index cache holds %d entries, want 1", len(e.svc.index.m))
	}
	// paging with a small limit
	p1, err := e.svc.Entries(ctx, j, EntriesRequest{UserID: e.user.ID, Archive: "/big.zip", Inner: "dir7", Limit: 300})
	if err != nil || len(p1.Items) != 300 || p1.NextCursor != "o300" {
		t.Fatalf("page 1: %d %q %v", len(p1.Items), p1.NextCursor, err)
	}
	p4, err := e.svc.Entries(ctx, j, EntriesRequest{UserID: e.user.ID, Archive: "/big.zip", Inner: "dir7", Limit: 300, Cursor: "o900"})
	if err != nil || len(p4.Items) != 100 || p4.NextCursor != "" {
		t.Fatalf("page 4: %d %q %v", len(p4.Items), p4.NextCursor, err)
	}
	if _, err := e.svc.Entries(ctx, j, EntriesRequest{UserID: e.user.ID, Archive: "/big.zip", Inner: "nope"}); !IsCode(err, CodeNotFound) {
		t.Fatalf("unknown folder: %v", err)
	}
	// a stricter entry limit refuses the archive with ARCHIVE_LIMIT
	e.svc.o.MaxEntries = 100
	e.svc.index.invalidate(e.user.ID, "/big.zip")
	if _, err := e.svc.Entries(ctx, j, EntriesRequest{UserID: e.user.ID, Archive: "/big.zip"}); !IsCode(err, CodeLimit) {
		t.Fatalf("limit: %v", err)
	}
	t.Logf("50k zip: first page %s, cached navigation %s", first, nav)
}

// TestEntryContentStreamsWithoutTmp: AC "preview gambar/teks dalam arsip
// tanpa ekstraksi penuh (tidak ada file di DATA_DIR/tmp)". Covers an
// indexed container (zip) and a stream container (tar.gz), the entry size
// limit, folders/symlinks/unknown names, and the encoding option.
func TestEntryContentStreamsWithoutTmp(t *testing.T) {
	e := newEnv(t)
	j := e.jail(t)
	ctx := context.Background()
	big := bytes.Repeat([]byte("0123456789abcdef"), 64*1024) // 1 MiB
	z := buildZip(t, []zipEntry{{name: "readme.txt", content: []byte("hello archive")}, {name: "img/pic.png", content: big}, {name: "link", symlink: "readme.txt"}, {name: "dir/"}})
	e.write(t, "pack.zip", z)
	read := func(archive, inner string) ([]byte, error) {
		es, err := e.svc.OpenEntry(ctx, j, e.user.ID, archive, inner, "", "auto")
		if err != nil {
			return nil, err
		}
		defer func() { _ = es.Body.Close() }()
		return io.ReadAll(es.Body)
	}
	if b, err := read("/pack.zip", "readme.txt"); err != nil || string(b) != "hello archive" {
		t.Fatalf("zip entry: %q %v", b, err)
	}
	if b, err := read("/pack.zip", "/img/pic.png"); err != nil || !bytes.Equal(b, big) {
		t.Fatalf("zip 1 MiB entry: %d %v", len(b), err)
	}
	for _, inner := range []string{"dir", "link", "missing.txt", "img"} {
		if _, err := read("/pack.zip", inner); !IsCode(err, CodeNotFound) {
			t.Fatalf("%s: %v", inner, err)
		}
	}
	// early close while streaming must not leak the walker goroutine
	es, err := e.svc.OpenEntry(ctx, j, e.user.ID, "/pack.zip", "img/pic.png", "", "auto")
	if err != nil {
		t.Fatal(err)
	}
	var head [10]byte
	if _, err := io.ReadFull(es.Body, head[:]); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { _ = es.Body.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close blocked")
	}
	// tar.gz: stream container, body only valid inside the walk
	tgz := gzipBytes(t, buildTar(t, []tarEntry{{name: "a/one.txt", content: []byte("ONE")}, {name: "a/two.txt", content: []byte("TWO")}}))
	e.write(t, "pack.tar.gz", tgz)
	if b, err := read("/pack.tar.gz", "a/two.txt"); err != nil || string(b) != "TWO" {
		t.Fatalf("tar.gz entry: %q %v", b, err)
	}
	resp, err := e.svc.Entries(ctx, j, EntriesRequest{UserID: e.user.ID, Archive: "/pack.tar.gz", Inner: "a"})
	if err != nil || resp.Total != 2 || resp.SupportsModify || resp.ModifyReason != "format" {
		t.Fatalf("tar.gz listing: %+v %v", resp, err)
	}
	// size limit
	e.svc.o.MaxEntries = 0
	huge := buildZip(t, []zipEntry{{name: "h.bin", content: []byte("x")}})
	e.write(t, "huge.zip", huge)
	ix := indexFromZip(t, huge)
	ix.Entries[0].Size = MaxEntryContent + 1
	e.svc.index.put(indexKey(e.user.ID, "/huge.zip", int64(len(huge)), mtimeOf(t, e.home+"/huge.zip"), "auto"), ix)
	if _, err := read("/huge.zip", "h.bin"); !IsCode(err, "FILE_TOO_LARGE") {
		t.Fatalf("size limit: %v", err)
	}
	if dirs := e.tmpDirs(t); len(dirs) != 0 {
		t.Fatalf("preview left working folders: %v", dirs)
	}
	entries, _ := os.ReadDir(e.cfg.DataDir + "/tmp")
	for _, d := range entries {
		if !strings.HasPrefix(d.Name(), "zip-") {
			t.Fatalf("unexpected file in DATA_DIR/tmp after preview: %s", d.Name())
		}
	}
}

func mtimeOf(t *testing.T, p string) int64 {
	t.Helper()
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return st.ModTime().UnixNano()
}

// TestEntriesPasswordAndSessions: an AES zip lists without a password
// (names are not encrypted) and flags the members; a wrong password is
// refused before any session exists; the right one yields a session bound
// to the user and archive; OpenEntry needs the password; a 7z with
// encrypted headers cannot even be listed without it.
func TestEntriesPasswordAndSessions(t *testing.T) {
	e := newEnv(t)
	j := e.jail(t)
	ctx := context.Background()
	e.write(t, "src/secret.txt", []byte("top secret"))
	e.write(t, "src/plain.txt", []byte("plain"))
	cr := resultOf[CompressResult](t, e.compress(t, CompressPayload{Paths: []string{"/src"}, Format: FormatZip, Name: "locked.zip", Dest: "/"}, "pw-123"))
	if cr.Path != "/locked.zip" {
		t.Fatalf("compress: %+v", cr)
	}
	resp, err := e.svc.Entries(ctx, j, EntriesRequest{UserID: e.user.ID, Archive: "/locked.zip", Inner: "src"})
	if err != nil || !resp.Encrypted || resp.Session != "" || resp.Total != 2 || !resp.Items[0].Encrypted || resp.SupportsModify || resp.ModifyReason != "encrypted" {
		t.Fatalf("encrypted listing without password: %+v %v", resp, err)
	}
	if _, err := e.svc.Entries(ctx, j, EntriesRequest{UserID: e.user.ID, Archive: "/locked.zip", Password: "nope"}); !errors.Is(err, ErrPasswordWrong) {
		t.Fatalf("wrong password: %v", err)
	}
	resp, err = e.svc.Entries(ctx, j, EntriesRequest{UserID: e.user.ID, Archive: "/locked.zip", Password: "pw-123"})
	if err != nil || len(resp.Session) != 32 {
		t.Fatalf("right password: %+v %v", resp, err)
	}
	sess := resp.Session
	again, err := e.svc.Entries(ctx, j, EntriesRequest{UserID: e.user.ID, Archive: "/locked.zip", Password: "pw-123", Inner: "src"})
	if err != nil || again.Session != sess {
		t.Fatalf("session reused: %q vs %q %v", again.Session, sess, err)
	}
	if s, ok := e.svc.Session(sess, e.user.ID, "/locked.zip"); !ok || s.Password != "pw-123" {
		t.Fatal("session lookup")
	}
	if _, ok := e.svc.Session(sess, "other-user", "/locked.zip"); ok {
		t.Fatal("session leaks across users")
	}
	if _, ok := e.svc.Session(sess, e.user.ID, "/other.zip"); ok {
		t.Fatal("session leaks across archives")
	}
	if _, err := e.svc.OpenEntry(ctx, j, e.user.ID, "/locked.zip", "src/secret.txt", "", "auto"); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("entry without password: %v", err)
	}
	es, err := e.svc.OpenEntry(ctx, j, e.user.ID, "/locked.zip", "src/secret.txt", "wrong", "auto")
	if err == nil {
		_, rerr := io.ReadAll(es.Body)
		_ = es.Body.Close()
		err = rerr
	}
	if !errors.Is(err, ErrPasswordWrong) {
		t.Fatalf("entry with wrong password: %v", err)
	}
	es, err = e.svc.OpenEntry(ctx, j, e.user.ID, "/locked.zip", "src/secret.txt", "pw-123", "auto")
	if err != nil {
		t.Fatal(err)
	}
	b, err := io.ReadAll(es.Body)
	_ = es.Body.Close()
	if err != nil || string(b) != "top secret" {
		t.Fatalf("decrypted entry: %q %v", b, err)
	}
	// session expiry
	e.svc.sessions.mu.Lock()
	e.svc.sessions.m[sess].expires = time.Now().Add(-time.Second)
	e.svc.sessions.mu.Unlock()
	if _, ok := e.svc.Session(sess, e.user.ID, "/locked.zip"); ok {
		t.Fatal("expired session still valid")
	}
	// 7z with encrypted headers: listing itself needs the password
	have7z(t)
	sz := sevenZipFixture(t, "hdr.7z", "pw-7z", true, map[string][]byte{"inner/x.txt": []byte("X")})
	e.write(t, "hdr.7z", sz)
	if _, err := e.svc.Entries(ctx, j, EntriesRequest{UserID: e.user.ID, Archive: "/hdr.7z"}); !errors.Is(err, ErrPasswordRequired) {
		t.Fatalf("7z -mhe without password: %v", err)
	}
	r7, err := e.svc.Entries(ctx, j, EntriesRequest{UserID: e.user.ID, Archive: "/hdr.7z", Password: "pw-7z", Inner: "inner"})
	if err != nil || r7.Total != 1 || r7.Items[0].Base != "x.txt" {
		t.Fatalf("7z -mhe with password: %+v %v", r7, err)
	}
	es, err = e.svc.OpenEntry(ctx, j, e.user.ID, "/hdr.7z", "inner/x.txt", "pw-7z", "auto")
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(es.Body)
	_ = es.Body.Close()
	if string(b) != "X" {
		t.Fatalf("7z entry: %q", b)
	}
}
