package search

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/files"
)

// TestNameSearchTenThousand (AC 1): "lap" over 10 000 files answers in
// under 2 s with truncated=false, ext=pdf filters correctly, results are
// paged from the cache and sorted naturally.
func TestNameSearchTenThousand(t *testing.T) {
	e := newEnv(t)
	for i := 0; i < 10000; i++ {
		ext := "txt"
		if i%10 == 0 {
			ext = "pdf"
		}
		name := fmt.Sprintf("file-%05d.%s", i, ext)
		if i%100 == 0 {
			name = fmt.Sprintf("Laporan-%05d.%s", i, ext)
		}
		e.write(t, fmt.Sprintf("d%02d/%s", i%50, name), "x")
	}
	e.write(t, "lap/laptop.txt", "y")
	j := e.jail(t)
	ctx := context.Background()
	start := time.Now()
	res, err := e.svc.SearchNames(ctx, j, e.user.ID, NameQuery{Path: "/", Q: "lap", Recursive: true, Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	took := time.Since(start)
	t.Logf("10k name search: %s wall, %d ms server", took, res.TookMs)
	if took > 2*time.Second {
		t.Fatalf("10k name search took %s", took)
	}
	// 100 "Laporan-*" + folder "lap" + "laptop.txt"
	if res.Total != 102 || res.Truncated || len(res.Items) != 50 || res.NextCursor == "" {
		t.Fatalf("total=%d truncated=%v page=%d cursor=%q", res.Total, res.Truncated, len(res.Items), res.NextCursor)
	}
	if res.Items[0].Name != "lap" || !res.Items[0].IsDir || res.Items[0].Location != "/" {
		t.Fatalf("first item %+v", res.Items[0])
	}
	if res.Items[1].Name != "Laporan-00000.pdf" || res.Items[1].Location != "/d00" {
		t.Fatalf("second item %+v", res.Items[1])
	}
	page2, err := e.svc.SearchNames(ctx, j, e.user.ID, NameQuery{Path: "/", Q: "lap", Recursive: true, Limit: 50, Cursor: res.NextCursor})
	if err != nil || len(page2.Items) != 50 || page2.NextCursor == "" || page2.TookMs != res.TookMs {
		t.Fatalf("page 2: %v %d %q", err, len(page2.Items), page2.NextCursor)
	}
	page3, err := e.svc.SearchNames(ctx, j, e.user.ID, NameQuery{Path: "/", Q: "lap", Recursive: true, Limit: 50, Cursor: page2.NextCursor})
	if err != nil || len(page3.Items) != 2 || page3.NextCursor != "" {
		t.Fatalf("page 3: %v %d %q", err, len(page3.Items), page3.NextCursor)
	}
	pdf, err := e.svc.SearchNames(ctx, j, e.user.ID, NameQuery{Path: "/", Q: "lap", Recursive: true, Ext: []string{"PDF"}})
	if err != nil {
		t.Fatal(err)
	}
	if pdf.Total != 100 { // every Laporan-* index is a multiple of 10 → pdf
		t.Fatalf("ext=pdf total %d", pdf.Total)
	}
	for _, it := range pdf.Items {
		if it.Ext != "pdf" || it.IsDir {
			t.Fatalf("ext filter leaked %+v", it)
		}
	}
	// a filter alone is a valid query; categories work on folders too
	cats, err := e.svc.SearchNames(ctx, j, e.user.ID, NameQuery{Path: "/", Recursive: false, Types: []files.Category{files.CatFolder}})
	if err != nil || cats.Total != 51 {
		t.Fatalf("folders at root: %v %d", err, cats.Total)
	}
	// history: one row per (kind, query, scope) — the later search with the
	// ext filter replaced the first "lap" row; cache hits add nothing
	h, err := e.svc.History(ctx, e.user.ID, "name")
	if err != nil || len(h) != 1 || h[0].Query != "lap" || string(h[0].Filters) != `{"ext":["pdf"]}` {
		t.Fatalf("history %+v %v", h, err)
	}
}

// TestNameSearchValidationAndIsolation (AC 6): path=../ → PATH_INVALID,
// empty query without filter → VALIDATION_FAILED, .system never listed,
// glob/regex modes, bad regex rejected, size/date filters, tag filter.
func TestNameSearchValidationAndIsolation(t *testing.T) {
	e := newEnv(t)
	e.write(t, "docs/Rapat 2026.docx", strings.Repeat("a", 2048))
	e.write(t, "docs/notes.txt", "n")
	e.write(t, "img/photo.JPG", strings.Repeat("b", 100))
	j := e.jail(t)
	ctx := context.Background()
	for _, p := range []string{"../", "/..", "/docs/../../etc", "/.system"} {
		_, err := e.svc.SearchNames(ctx, j, e.user.ID, NameQuery{Path: p, Q: "a", Recursive: true})
		if !IsCode(err, CodePathInvalid) {
			t.Fatalf("path %q: %v", p, err)
		}
	}
	if _, err := e.svc.SearchNames(ctx, j, e.user.ID, NameQuery{Path: "/", Recursive: true}); !IsCode(err, CodeValidation) {
		t.Fatalf("empty query: %v", err)
	}
	if _, err := e.svc.SearchNames(ctx, j, e.user.ID, NameQuery{Path: "/nope", Q: "a"}); !IsCode(err, CodeNotFound) {
		t.Fatalf("missing folder: %v", err)
	}
	if _, err := e.svc.SearchNames(ctx, j, e.user.ID, NameQuery{Path: "/", Q: "(", Regex: true, Recursive: true}); !IsCode(err, CodeValidation) {
		t.Fatalf("bad regex: %v", err)
	}
	all, err := e.svc.SearchNames(ctx, j, e.user.ID, NameQuery{Path: "/", Q: "*", Glob: true, Recursive: true})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range all.Items {
		if strings.HasPrefix(it.Path, "/.system") {
			t.Fatalf(".system leaked: %s", it.Path)
		}
	}
	if all.Total != 5 { // docs, img, Rapat, notes, photo
		t.Fatalf("glob * total %d", all.Total)
	}
	glob, err := e.svc.SearchNames(ctx, j, e.user.ID, NameQuery{Path: "/", Q: "*.jpg", Recursive: true})
	if err != nil || glob.Total != 1 || glob.Items[0].Name != "photo.JPG" {
		t.Fatalf("glob: %v %+v", err, glob.Items)
	}
	re, err := e.svc.SearchNames(ctx, j, e.user.ID, NameQuery{Path: "/", Q: `^rapat \d{4}\.docx$`, Regex: true, Recursive: true})
	if err != nil || re.Total != 1 {
		t.Fatalf("regex: %v %d", err, re.Total)
	}
	big, err := e.svc.SearchNames(ctx, j, e.user.ID, NameQuery{Path: "/", MinSize: 1000, Recursive: true})
	if err != nil || big.Total != 1 || big.Items[0].Name != "Rapat 2026.docx" {
		t.Fatalf("minSize: %v %+v", err, big.Items)
	}
	future, err := e.svc.SearchNames(ctx, j, e.user.ID, NameQuery{Path: "/", Q: "a", From: time.Now().Add(time.Hour), Recursive: true})
	if err != nil || future.Total != 0 {
		t.Fatalf("from in the future: %v %d", err, future.Total)
	}
	nonrec, err := e.svc.SearchNames(ctx, j, e.user.ID, NameQuery{Path: "/", Q: "o", Recursive: false})
	if err != nil || nonrec.Total != 1 || nonrec.Items[0].Name != "docs" {
		t.Fatalf("non-recursive: %v %+v", err, nonrec.Items)
	}
	// tag filter: nothing tagged → empty, no error
	tagged, err := e.svc.SearchNames(ctx, j, e.user.ID, NameQuery{Path: "/", Tag: "red", Recursive: true})
	if err != nil || tagged.Total != 0 {
		t.Fatalf("tag: %v %d", err, tagged.Total)
	}
	// bad cursor
	if _, err := e.svc.SearchNames(ctx, j, e.user.ID, NameQuery{Path: "/", Q: "o", Cursor: "zzz:1"}); !IsCode(err, CodeValidation) {
		t.Fatalf("bad cursor: %v", err)
	}
}

// TestNameSearchRateLimit: the 31st fresh search within a minute is refused.
func TestNameSearchRateLimit(t *testing.T) {
	e := newEnv(t)
	e.write(t, "a.txt", "a")
	j := e.jail(t)
	ctx := context.Background()
	for i := 0; i < NameRateLimit; i++ {
		if _, err := e.svc.SearchNames(ctx, j, e.user.ID, NameQuery{Path: "/", Q: fmt.Sprintf("q%d", i)}); err != nil {
			t.Fatalf("search %d: %v", i, err)
		}
	}
	_, err := e.svc.SearchNames(ctx, j, e.user.ID, NameQuery{Path: "/", Q: "late"})
	if !IsCode(err, CodeRateLimited) {
		t.Fatalf("want RATE_LIMITED, got %v", err)
	}
	// cached pages are free
	if _, err := e.svc.SearchNames(ctx, j, e.user.ID, NameQuery{Path: "/", Q: "q0"}); err != nil {
		t.Fatalf("cached search: %v", err)
	}
}
