package listing

import (
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/fsjail"
)

func TestCursorRoundTrip(t *testing.T) {
	s := NewSigner([]byte("secret-key-for-tests"))
	c := NewCollator("id")
	now := time.Unix(1_700_000_000, 123)
	bt := now.Add(-time.Hour)
	last := &Item{Name: "file10", Path: "/docs/file10", Size: 42, TypeLabel: "type.text.plain", Ext: "txt", mtime: now, btime: &bt, modeNum: 0o640}
	last.nameKey = NaturalKey(last.Name, c)
	n := "catatan"
	last.Note = &n

	specs := []SortSpec{
		{Field: FieldName, FoldersFirst: true},
		{Field: FieldSize, Desc: true, FoldersFirst: true},
		{Field: FieldMtime, Field2: FieldName, FoldersFirst: true},
		{Field: FieldBtime},
		{Field: FieldExt, Field2: FieldSize},
		{Field: FieldNote},
		{Field: FieldMode},
		{Field: FieldType},
	}
	for _, spec := range specs {
		cur := s.Encode(spec, last)
		pseudo, err := s.Decode(cur, spec, "/docs", c)
		if err != nil {
			t.Fatalf("%s: decode: %v", spec, err)
		}
		cmp := comparator{spec: spec}
		if cmp.compare(pseudo, last) != 0 {
			t.Fatalf("%s: pseudo item must compare equal to the original (got %d)", spec, cmp.compare(pseudo, last))
		}
		if pseudo.Path != "/docs/file10" {
			t.Fatalf("path %q", pseudo.Path)
		}
	}
	// Root directory path reconstruction.
	cur := s.Encode(DefaultSort, last)
	pseudo, _ := s.Decode(cur, DefaultSort, fsjail.Root, c)
	if pseudo.Path != "/file10" {
		t.Fatalf("root path %q", pseudo.Path)
	}
	// Directory with item count sorts by count.
	dir := &Item{Name: "sub", Path: "/sub", IsDir: true}
	cnt := int64(7)
	dir.ItemCount = &cnt
	dir.nameKey = NaturalKey("sub", c)
	spec := SortSpec{Field: FieldSize, FoldersFirst: true}
	pseudo, err := s.Decode(s.Encode(spec, dir), spec, fsjail.Root, c)
	if err != nil || pseudo.ItemCount == nil || *pseudo.ItemCount != 7 || !pseudo.IsDir {
		t.Fatalf("dir cursor: %+v %v", pseudo, err)
	}
}

func TestCursorRejects(t *testing.T) {
	s := NewSigner([]byte("k1"))
	c := NewCollator("id")
	last := &Item{Name: "a", Path: "/a"}
	last.nameKey = NaturalKey("a", c)
	cur := s.Encode(DefaultSort, last)

	bad := []string{"", "not-base64!!", base64.RawURLEncoding.EncodeToString([]byte("short"))}
	for _, b := range bad {
		if _, err := s.Decode(b, DefaultSort, fsjail.Root, c); !errors.Is(err, ErrBadCursor) {
			t.Fatalf("%q: want ErrBadCursor, got %v", b, err)
		}
	}
	// Tampered payload.
	raw, _ := base64.RawURLEncoding.DecodeString(cur)
	raw[2] ^= 0x01
	if _, err := s.Decode(base64.RawURLEncoding.EncodeToString(raw), DefaultSort, fsjail.Root, c); !errors.Is(err, ErrBadCursor) {
		t.Fatal("tampered cursor accepted")
	}
	// Different sort spec.
	if _, err := s.Decode(cur, SortSpec{Field: FieldSize, FoldersFirst: true}, fsjail.Root, c); !errors.Is(err, ErrBadCursor) {
		t.Fatal("cursor for another sort accepted")
	}
	// Different secret.
	if _, err := NewSigner([]byte("k2")).Decode(cur, DefaultSort, fsjail.Root, c); !errors.Is(err, ErrBadCursor) {
		t.Fatal("cursor from another key accepted")
	}
}
