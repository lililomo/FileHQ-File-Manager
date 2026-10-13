package listing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/listing/fixture"
)

var raceEnabled = false // set by race_test.go

func newEnv(t *testing.T, names ...string) (*Service, *fsjail.Jail, string) {
	t.Helper()
	dir := t.TempDir()
	for _, n := range names {
		if strings.HasSuffix(n, "/") {
			if err := os.Mkdir(filepath.Join(dir, strings.TrimSuffix(n, "/")), 0o750); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, n), []byte(n), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	j, err := fsjail.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	svc := New(Options{Secret: []byte("test-secret"), PageSize: 500})
	return svc, j, dir
}

func doList(t *testing.T, svc *Service, j *fsjail.Jail, req ListRequest) *ListResult {
	t.Helper()
	if req.UserID == "" {
		req.UserID = "u1"
	}
	if req.Sort.Field == "" {
		req.Sort = DefaultSort
	}
	if req.Locale == "" {
		req.Locale = "id"
	}
	req.Hidden = true
	res, err := svc.List(context.Background(), j, req)
	if err != nil {
		t.Fatalf("list %s: %v", req.Dir, err)
	}
	return res
}

func TestListNaturalOrderAC(t *testing.T) {
	svc, j, _ := newEnv(t, "file1", "file2", "file10", "File3", "é.txt")
	res := doList(t, svc, j, ListRequest{Dir: fsjail.Root})
	// Locale-aware: é collates right after e, i.e. before f (SPEC §8.3 example).
	if got := names(res.Items); got != "é.txt,file1,file2,File3,file10" {
		t.Fatalf("order: %s", got)
	}
	if res.Total != 5 || res.NextCursor != nil || res.ParentPath != nil || res.Dir.ItemCount != 5 || res.Path != "/" {
		t.Fatalf("result meta: %+v", res)
	}
	it := res.Items[0]
	if it.Type != "file" || it.Ext != "txt" || it.Mime == nil || *it.Mime != "text/plain" || it.TypeLabel != "type.text.plain" || it.Size != int64(len("é.txt")) || it.Mode != "rw-r-----" || it.ModeOctal != "640" || it.Etag == "" || it.Shared != "none" {
		t.Fatalf("item: %+v", it)
	}
}

func TestListFoldersHiddenFilterTypes(t *testing.T) {
	svc, j, _ := newEnv(t, "docs/", "zz/", ".hidden", "b.jpg", "a.txt", "c.mp4")
	res := doList(t, svc, j, ListRequest{Dir: fsjail.Root})
	if got := names(res.Items); got != ".hidden,docs,zz,a.txt,b.jpg,c.mp4" && got != "docs,zz,.hidden,a.txt,b.jpg,c.mp4" {
		t.Fatalf("folders first: %s", got)
	}
	if res.Items[0].Name == "docs" && !res.Items[0].IsDir {
		t.Fatal("docs must be a dir")
	}
	req := ListRequest{UserID: "u1", Dir: fsjail.Root, Sort: DefaultSort, Locale: "id", Hidden: false}
	r2, err := svc.List(context.Background(), j, req)
	if err != nil || strings.Contains(names(r2.Items), ".hidden") || r2.Total != 5 {
		t.Fatalf("hidden=false: %v %s", err, names(r2.Items))
	}
	req.Hidden, req.Filter = true, "A.T"
	r3, _ := svc.List(context.Background(), j, req)
	if names(r3.Items) != "a.txt" || r3.Total != 1 {
		t.Fatalf("filter: %s", names(r3.Items))
	}
	req.Filter, req.Types = "", []files.Category{files.CatFolder, files.CatVideo}
	r4, _ := svc.List(context.Background(), j, req)
	if names(r4.Items) != "docs,zz,c.mp4" {
		t.Fatalf("types: %s", names(r4.Items))
	}
	req.Sort = SortSpec{Field: FieldName, FoldersFirst: false}
	req.Types = nil
	r5, _ := svc.List(context.Background(), j, req)
	if names(r5.Items) != ".hidden,a.txt,b.jpg,c.mp4,docs,zz" {
		t.Fatalf("no folders first: %s", names(r5.Items))
	}
}

func TestListCursorStableUnderInsert(t *testing.T) {
	svc, j, dir := newEnv(t, "file1", "file2", "file10", "File3", "é.txt")
	base := ListRequest{UserID: "u1", Dir: fsjail.Root, Sort: DefaultSort, Locale: "id", Hidden: true, Limit: 2}
	p1, err := svc.List(context.Background(), j, base)
	if err != nil || names(p1.Items) != "é.txt,file1" || p1.NextCursor == nil || p1.Total != 5 {
		t.Fatalf("page1: %v %s", err, names(p1.Items))
	}
	// A file appears between pages (as if uploaded); the app invalidates
	// the folder after its own mutations.
	if err := os.WriteFile(filepath.Join(dir, "file12"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	svc.Invalidate("u1", "", fsjail.Root)
	base.Cursor = *p1.NextCursor
	p2, err := svc.List(context.Background(), j, base)
	if err != nil || names(p2.Items) != "file2,File3" || p2.NextCursor == nil || p2.Total != 6 {
		t.Fatalf("page2: %v %s total=%d", err, names(p2.Items), p2.Total)
	}
	base.Cursor = *p2.NextCursor
	p3, err := svc.List(context.Background(), j, base)
	if err != nil || names(p3.Items) != "file10,file12" || p3.NextCursor != nil {
		t.Fatalf("page3: %v %s next=%v", err, names(p3.Items), p3.NextCursor)
	}
	// A file inserted *before* the cursor position is simply not on later
	// pages: no duplicates, no skips of items after the cursor.
	if err := os.WriteFile(filepath.Join(dir, "file0"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	svc.Invalidate("u1", "", fsjail.Root)
	base.Cursor = *p1.NextCursor
	p2b, _ := svc.List(context.Background(), j, base)
	if names(p2b.Items) != "file2,File3" {
		t.Fatalf("page2 after insert-before: %s", names(p2b.Items))
	}
	// Bad cursor.
	base.Cursor = "garbage"
	if _, err := svc.List(context.Background(), j, base); !errors.Is(err, ErrBadCursor) {
		t.Fatalf("want ErrBadCursor, got %v", err)
	}
	// Cursor with another sort spec.
	base.Cursor, base.Sort = *p1.NextCursor, SortSpec{Field: FieldSize, FoldersFirst: true}
	if _, err := svc.List(context.Background(), j, base); !errors.Is(err, ErrBadCursor) {
		t.Fatalf("want ErrBadCursor for mismatched spec, got %v", err)
	}
	// limit clamp.
	base.Cursor, base.Sort, base.Limit = "", DefaultSort, 5000
	p, _ := svc.List(context.Background(), j, base)
	if len(p.Items) != 7 {
		t.Fatalf("limit clamp: %d", len(p.Items))
	}
}

func TestListCacheAndCounts(t *testing.T) {
	svc, j, dir := newEnv(t, "docs/", "x.txt")
	if err := os.WriteFile(filepath.Join(dir, "docs", "inner.txt"), []byte("x"), 0o640); err != nil {
		t.Fatal(err)
	}
	r1 := doList(t, svc, j, ListRequest{Dir: fsjail.Root, Counts: true})
	if r1.Items[0].ItemCount == nil || *r1.Items[0].ItemCount != 1 {
		t.Fatalf("counts: %+v", r1.Items[0].ItemCount)
	}
	r2 := doList(t, svc, j, ListRequest{Dir: fsjail.Root})
	if r2.Items[0].ItemCount != nil {
		t.Fatal("counts must not leak into the cached slice")
	}
	_, _, hits, _ := svc.Cache().Stats()
	if hits != 1 {
		t.Fatalf("expected cache hit, hits=%d", hits)
	}
	// Sub-directory listing and parent path.
	sub := doList(t, svc, j, ListRequest{Dir: "/docs"})
	if sub.ParentPath == nil || *sub.ParentPath != "/" || names(sub.Items) != "inner.txt" || sub.Items[0].Path != "/docs/inner.txt" {
		t.Fatalf("sub: %+v", sub)
	}
	// Not a directory.
	if _, err := svc.List(context.Background(), j, ListRequest{UserID: "u1", Dir: "/x.txt", Sort: DefaultSort, Hidden: true}); !errors.Is(err, ErrNotDirectory) {
		t.Fatalf("want ErrNotDirectory, got %v", err)
	}
	if _, err := svc.List(context.Background(), j, ListRequest{UserID: "u1", Dir: "/missing", Sort: DefaultSort, Hidden: true}); !errors.Is(err, fsjail.ErrNotFound) {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	// Stat.
	st, err := svc.Stat(j, "u1", "", "/docs")
	if err != nil || !st.IsDir || st.ItemCount == nil || *st.ItemCount != 1 || st.Etag == "" {
		t.Fatalf("stat dir: %+v %v", st, err)
	}
	st, err = svc.Stat(j, "u1", "", "/x.txt")
	if err != nil || st.IsDir || st.Size != 5 || st.StatView()["nlink"] == nil {
		t.Fatalf("stat file: %+v %v", st, err)
	}
	if _, err := svc.Stat(j, "u1", "", "/nope"); !errors.Is(err, fsjail.ErrNotFound) {
		t.Fatalf("stat missing: %v", err)
	}
}

func TestListSymlinks(t *testing.T) {
	svc, j, dir := newEnv(t, "real.txt", "sub/")
	if err := os.Symlink("real.txt", filepath.Join(dir, "ok.lnk")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../etc/passwd", filepath.Join(dir, "evil.lnk")); err != nil {
		t.Fatal(err)
	}
	res := doList(t, svc, j, ListRequest{Dir: fsjail.Root})
	byName := map[string]*Item{}
	for _, it := range res.Items {
		byName[it.Name] = it
	}
	if ok := byName["ok.lnk"]; !ok.IsSymlink || ok.Type != "symlink" || ok.Broken || ok.SymlinkTarget != "real.txt" {
		t.Fatalf("ok.lnk: %+v", ok)
	}
	if evil := byName["evil.lnk"]; !evil.IsSymlink || !evil.Broken || evil.BrokenReason != "escape" {
		t.Fatalf("evil.lnk: %+v", evil)
	}
}

// TestList10kLatency is the PROMPT Step 5 performance gate (p95 < 500 ms
// cold on 10 000 entries). Under -race only the numbers are logged.
func TestList10kLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	dir := t.TempDir()
	if err := fixture.Generate(dir, 10_000); err != nil {
		t.Fatal(err)
	}
	j, err := fsjail.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()
	svc := New(Options{Secret: []byte("s"), PageSize: 500})
	const runs = 10
	durs := make([]time.Duration, 0, runs)
	for range runs {
		svc.InvalidateUser("u1")
		start := time.Now()
		res, err := svc.List(context.Background(), j, ListRequest{UserID: "u1", Dir: fsjail.Root, Sort: DefaultSort, Hidden: true, Locale: "id"})
		if err != nil {
			t.Fatal(err)
		}
		durs = append(durs, time.Since(start))
		if res.Total != 10_000 || len(res.Items) != 500 || res.NextCursor == nil {
			t.Fatalf("10k listing: total=%d items=%d", res.Total, len(res.Items))
		}
	}
	sort.Slice(durs, func(i, k int) bool { return durs[i] < durs[k] })
	p95 := durs[(runs*95+99)/100-1]
	warm := time.Now()
	if _, err := svc.List(context.Background(), j, ListRequest{UserID: "u1", Dir: fsjail.Root, Sort: DefaultSort, Hidden: true, Locale: "id"}); err != nil {
		t.Fatal(err)
	}
	t.Logf("ListDir10k cold: min=%v median=%v p95=%v max=%v; warm(cache)=%v; race=%v", durs[0], durs[runs/2], p95, durs[runs-1], time.Since(warm), raceEnabled)
	if !raceEnabled && p95 >= 500*time.Millisecond {
		t.Fatalf("p95 %v ≥ 500 ms", p95)
	}
}

func BenchmarkListDir10k(b *testing.B) {
	dir := b.TempDir()
	if err := fixture.Generate(dir, 10_000); err != nil {
		b.Fatal(err)
	}
	j, err := fsjail.Open(dir)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = j.Close() }()
	svc := New(Options{Secret: []byte("s"), PageSize: 500})
	req := ListRequest{UserID: "u1", Dir: fsjail.Root, Sort: DefaultSort, Hidden: true, Locale: "id"}
	b.ResetTimer()
	for range b.N {
		svc.InvalidateUser("u1")
		if _, err := svc.List(context.Background(), j, req); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkListDir10kCached(b *testing.B) {
	dir := b.TempDir()
	if err := fixture.Generate(dir, 10_000); err != nil {
		b.Fatal(err)
	}
	j, err := fsjail.Open(dir)
	if err != nil {
		b.Fatal(err)
	}
	defer func() { _ = j.Close() }()
	svc := New(Options{Secret: []byte("s"), PageSize: 500})
	req := ListRequest{UserID: "u1", Dir: fsjail.Root, Sort: DefaultSort, Hidden: true, Locale: "id"}
	if _, err := svc.List(context.Background(), j, req); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for range b.N {
		if _, err := svc.List(context.Background(), j, req); err != nil {
			b.Fatal(err)
		}
	}
}
