package listing

import (
	"strings"
	"testing"

	"kppbwi.app/filemanager/internal/fsjail"
)

// FuzzCursor (Step 20, SPEC §10.6): an arbitrary cursor string is either
// rejected with ErrBadCursor or decodes into an item that re-encodes; the
// decoder must never panic on hostile input (cursors are client-supplied
// and HMAC-signed, so only genuine cursors reach the JSON layer).
func FuzzCursor(f *testing.F) {
	secret := []byte("0123456789abcdef0123456789abcdef")
	s := NewSigner(secret)
	c := NewCollator("id")
	it := &Item{Name: "laporan (2).txt", Path: "/docs/laporan (2).txt", Size: 1234, Mtime: "2026-10-22T00:00:00.000Z"}
	for _, spec := range []SortSpec{{Field: FieldName}, {Field: FieldSize, Desc: true, Field2: FieldName}, {Field: FieldMtime, FoldersFirst: true}} {
		f.Add(s.Encode(spec, it), spec.String())
	}
	f.Add("", "name")
	f.Add("AAAA", "name")
	f.Add(strings.Repeat("A", 5000), "size")
	f.Fuzz(func(t *testing.T, cur, specStr string) {
		spec, _, err := ParseSortSpec(specStr, "asc", "", "", false)
		if err != nil {
			spec = SortSpec{Field: FieldName}
		}
		got, err := s.Decode(cur, spec, fsjail.VPath("/docs"), c)
		if err != nil {
			if got != nil {
				t.Fatalf("error with item: %v", err)
			}
			return
		}
		if got == nil {
			t.Fatal("nil item without error")
		}
		if strings.ContainsAny(got.Name, "/\x00") {
			t.Fatalf("decoded name %q", got.Name)
		}
		if again := s.Encode(spec, got); again == "" {
			t.Fatal("re-encode produced empty cursor")
		}
	})
}
