package listing

import (
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/unicode/norm"
)

func TestNaturalLess(t *testing.T) {
	c := NewCollator("id")
	pairs := [][2]string{
		{"file2", "file10"},
		{"file1", "file2"},
		{"0.0", "1.0"},
		{"a-21", "a-123"},
		{"File3", "file10"},
		{"file10", "File11"},
		{"abc", "abd"},
		{"e.txt", "é.txt"},
		{"é.txt", "f.txt"},
		{"1", "a"}, // numbers before text
		{"x1y2", "x1y10"},
		{"IMG_0009.jpg", "IMG_0010.jpg"},
		{"v1.2.9", "v1.10.0"},
	}
	for _, p := range pairs {
		if !NaturalLess(p[0], p[1], c) {
			t.Errorf("want %q < %q", p[0], p[1])
		}
		if NaturalLess(p[1], p[0], c) {
			t.Errorf("want !(%q < %q)", p[1], p[0])
		}
	}
	// Equal numeric value with different leading zeros: deterministic byte tie-break.
	if !NaturalLess("01", "1", c) || NaturalLess("1", "01", c) {
		t.Fatal("01 vs 1 must be ordered by bytes (01 first)")
	}
	if NaturalLess("a", "a", c) {
		t.Fatal("irreflexive")
	}
	// Case-insensitive primary order but still a total order.
	if !NaturalLess("File3", "file3", c) || NaturalLess("file3", "File3", c) {
		t.Fatal("File3/file3 tie-break by bytes")
	}
	// NFD and NFC spellings are the same name.
	nfc := "é.txt"
	nfd := norm.NFD.String(nfc)
	if nfc == nfd {
		t.Fatal("fixture: expected different byte forms")
	}
	if CompareNatural(NaturalKey(nfc, c), NaturalKey(nfd, c)) != 0 {
		t.Fatal("NFD/NFC must compare equal")
	}
	// English locale works too.
	if !NaturalLess("file2", "file10", NewCollator("en")) {
		t.Fatal("en collator")
	}
}

func TestNaturalSortExample(t *testing.T) {
	c := NewCollator("id")
	names := []string{"Zeta", "file10", "gambar", "File11", "file2", "file1"}
	slices.SortFunc(names, func(a, b string) int {
		if NaturalLess(a, b, c) {
			return -1
		}
		if NaturalLess(b, a, c) {
			return 1
		}
		return 0
	})
	want := "file1,file2,file10,File11,gambar,Zeta"
	if got := strings.Join(names, ","); got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

type fakeItem struct {
	name  string
	dir   bool
	size  int64
	mtime int64
	ext   string
	btime *int64
	note  string
	pin   bool
	label string
	media *Media
}

func build(t *testing.T, locale string, defs []fakeItem) []*Item {
	t.Helper()
	c := NewCollator(locale)
	items := make([]*Item, 0, len(defs))
	for _, d := range defs {
		it := &Item{Name: d.name, Path: "/" + d.name, IsDir: d.dir, Size: d.size, Ext: d.ext, TypeLabel: d.label, Pinned: d.pin, Media: d.media}
		if d.dir {
			it.Type = "dir"
		} else {
			it.Type = "file"
		}
		it.mtime = time.Unix(0, d.mtime)
		if d.btime != nil {
			bt := time.Unix(0, *d.btime)
			it.btime = &bt
		}
		if d.note != "" {
			n := d.note
			it.Note = &n
		}
		it.nameKey = NaturalKey(d.name, c)
		items = append(items, it)
	}
	return items
}

func names(items []*Item) string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.Name)
	}
	return strings.Join(out, ",")
}

func TestSortFields(t *testing.T) {
	i64 := func(v int64) *int64 { return &v }
	defs := []fakeItem{
		{name: "file10", size: 10, mtime: 30, ext: "txt", btime: i64(5), label: "type.text.plain"},
		{name: "docs", dir: true, mtime: 10, label: "type.folder"},
		{name: "file2", size: 200, mtime: 20, ext: "go", note: "penting", label: "type.code.go"},
		{name: "README", size: 200, mtime: 40, ext: "", btime: i64(1), label: "type.text.plain"},
		{name: "File3", size: 5, mtime: 50, ext: "txt", label: "type.text.plain", media: &Media{Width: 10, Height: 10, DurationMs: 3}},
		{name: "arsip", dir: true, mtime: 60, label: "type.folder"},
	}
	cases := []struct {
		spec SortSpec
		want string
	}{
		{SortSpec{Field: FieldName, FoldersFirst: true}, "arsip,docs,file2,File3,file10,README"},
		{SortSpec{Field: FieldName, Desc: true, FoldersFirst: true}, "docs,arsip,README,file10,File3,file2"},
		{SortSpec{Field: FieldName}, "arsip,docs,file2,File3,file10,README"},
		{SortSpec{Field: FieldSize, FoldersFirst: true}, "arsip,docs,File3,file10,file2,README"},
		// desc flips only the primary key: equal sizes keep natural name order.
		{SortSpec{Field: FieldSize, Desc: true, FoldersFirst: true}, "arsip,docs,file2,README,file10,File3"},
		{SortSpec{Field: FieldMtime, Desc: true, FoldersFirst: true}, "arsip,docs,File3,README,file10,file2"},
		// unknown (no extension) last even when descending.
		{SortSpec{Field: FieldExt, FoldersFirst: true}, "arsip,docs,file2,File3,file10,README"},
		{SortSpec{Field: FieldExt, Desc: true, FoldersFirst: true}, "arsip,docs,File3,file10,file2,README"},
		{SortSpec{Field: FieldBtime, FoldersFirst: true}, "arsip,docs,README,file10,file2,File3"},
		{SortSpec{Field: FieldNote, FoldersFirst: true}, "arsip,docs,file2,File3,file10,README"},
		{SortSpec{Field: FieldType, FoldersFirst: true}, "arsip,docs,file2,File3,file10,README"},
		{SortSpec{Field: FieldDimensions, Desc: true, FoldersFirst: true}, "arsip,docs,File3,file2,file10,README"},
		{SortSpec{Field: FieldDuration, FoldersFirst: true}, "arsip,docs,File3,file2,file10,README"},
		// secondary field: size asc then mtime desc.
		{SortSpec{Field: FieldSize, Field2: FieldMtime, Desc2: true, FoldersFirst: true}, "arsip,docs,File3,file10,README,file2"},
	}
	for _, c := range cases {
		items := build(t, "id", defs)
		sortItems(items, c.spec)
		if got := names(items); got != c.want {
			t.Errorf("%s: got %s want %s", c.spec, got, c.want)
		}
	}
}

func TestSortPinnedAndStable(t *testing.T) {
	defs := []fakeItem{
		{name: "b", size: 1, mtime: 1, label: "x"},
		{name: "a", size: 1, mtime: 1, label: "x"},
		{name: "z", size: 1, mtime: 1, label: "x", pin: true},
		{name: "sub", dir: true, mtime: 1, label: "f"},
	}
	items := build(t, "en", defs)
	sortItems(items, SortSpec{Field: FieldSize, FoldersFirst: true})
	if got := names(items); got != "z,sub,a,b" {
		t.Fatalf("pinned first, then folders, then name tie-break: %s", got)
	}
	// Sorting twice yields the same order (determinism).
	again := build(t, "en", defs)
	sortItems(again, SortSpec{Field: FieldSize, FoldersFirst: true})
	if names(again) != names(items) {
		t.Fatal("not deterministic")
	}
}

func TestParseSortSpec(t *testing.T) {
	spec, _, err := ParseSortSpec("", "", "", "", true)
	if err != nil || spec != DefaultSort {
		t.Fatalf("default: %v %v", spec, err)
	}
	spec, _, err = ParseSortSpec("size", "desc", "mtime", "asc", false)
	if err != nil || spec.Field != FieldSize || !spec.Desc || spec.Field2 != FieldMtime || spec.Desc2 || spec.FoldersFirst {
		t.Fatalf("parsed: %+v %v", spec, err)
	}
	if _, f, err := ParseSortSpec("nope", "", "", "", true); err == nil || f != "sort" {
		t.Fatal("unknown field accepted")
	}
	if _, f, err := ParseSortSpec("name", "sideways", "", "", true); err == nil || f != "dir" {
		t.Fatal("bad dir accepted")
	}
	// sort2 equal to sort is ignored.
	spec, _, _ = ParseSortSpec("name", "", "name", "desc", true)
	if spec.Field2 != "" {
		t.Fatal("duplicate secondary should be dropped")
	}
}
