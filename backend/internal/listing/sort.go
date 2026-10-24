package listing

import (
	"bytes"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"golang.org/x/text/unicode/norm"
)

// Field is a server-side sort field (SPEC §8.3 "Field sort").
type Field string

// Sort fields.
const (
	FieldName       Field = "name"
	FieldSize       Field = "size"
	FieldType       Field = "type"
	FieldExt        Field = "ext"
	FieldMtime      Field = "mtime"
	FieldBtime      Field = "btime"
	FieldAtime      Field = "atime"
	FieldMode       Field = "mode"
	FieldNote       Field = "note"
	FieldDimensions Field = "dimensions"
	FieldDuration   Field = "duration"
)

var allFields = []Field{FieldName, FieldSize, FieldType, FieldExt, FieldMtime, FieldBtime, FieldAtime, FieldMode, FieldNote, FieldDimensions, FieldDuration}

// ParseField validates a sort= value.
func ParseField(s string) (Field, bool) {
	if slices.Contains(allFields, Field(s)) {
		return Field(s), true
	}
	return "", false
}

// SortSpec is the complete ordering of a listing.
type SortSpec struct {
	Field        Field
	Desc         bool
	Field2       Field // optional secondary field ("" = none)
	Desc2        bool
	FoldersFirst bool
}

// DefaultSort is name ascending, folders first.
var DefaultSort = SortSpec{Field: FieldName, FoldersFirst: true}

// String is the canonical cache/cursor key of the spec.
func (s SortSpec) String() string {
	return fmt.Sprintf("%s:%t:%s:%t:%t", s.Field, s.Desc, s.Field2, s.Desc2, s.FoldersFirst)
}

// ParseSortSpec validates query parameters; empty strings fall back to the
// defaults. The returned reason names the offending parameter.
func ParseSortSpec(sort, dir, sort2, dir2 string, foldersFirst bool) (SortSpec, string, error) {
	spec := SortSpec{Field: FieldName, FoldersFirst: foldersFirst}
	if sort != "" {
		f, ok := ParseField(sort)
		if !ok {
			return spec, "sort", fmt.Errorf("listing: unknown sort field %q", sort)
		}
		spec.Field = f
	}
	var err error
	if spec.Desc, err = parseDir(dir); err != nil {
		return spec, "dir", err
	}
	if sort2 != "" {
		f, ok := ParseField(sort2)
		if !ok {
			return spec, "sort2", fmt.Errorf("listing: unknown sort field %q", sort2)
		}
		if f != spec.Field {
			spec.Field2 = f
		}
		if spec.Desc2, err = parseDir(dir2); err != nil {
			return spec, "dir2", err
		}
	}
	return spec, "", nil
}

func parseDir(s string) (bool, error) {
	switch s {
	case "", "asc":
		return false, nil
	case "desc":
		return true, nil
	}
	return false, fmt.Errorf("listing: direction must be asc or desc, got %q", s)
}

// NewCollator returns the case-insensitive collator for a UI locale ("id" or
// "en"; anything else falls back to Indonesian). Collators are not safe for
// concurrent use: create one per request.
func NewCollator(locale string) *collate.Collator {
	tag := language.Indonesian
	if locale == "en" {
		tag = language.English
	}
	return collate.New(tag, collate.IgnoreCase)
}

// natToken is one run of a name: either a number (ASCII digits) or text
// (represented by its collation key).
type natToken struct {
	num    bool
	digits string // leading zeros stripped; "" means 0
	key    []byte
}

// NatKey is the precomputed natural-sort key of a name.
type NatKey []natToken

// NaturalKey splits s into digit and non-digit runs. Digit runs compare
// numerically, text runs through the collator (case-insensitive,
// locale-aware). The collator's own numeric option is deliberately NOT used
// (golang/go#67997: "0.0" vs "1.0" mis-ordered); CI greps for it.
func NaturalKey(s string, c *collate.Collator) NatKey {
	return (&keyer{c: c}).key(s)
}

// keyer computes natural keys for many names with one collator. With runs
// set it memoises the collation key of every text run: the names of one
// folder share most runs ("file", ".txt", "IMG_"), and the collator is the
// most expensive part of a cold listing (Step 21 profile). Shared keys are
// never mutated (CompareNatural only reads them).
type keyer struct {
	c    *collate.Collator
	buf  collate.Buffer
	runs map[string][]byte
}

// maxMemoRuns bounds the memo of one listing build.
const maxMemoRuns = 8192

func newKeyer(c *collate.Collator) *keyer {
	return &keyer{c: c, runs: make(map[string][]byte, 64)}
}

func (k *keyer) key(s string) NatKey {
	s = norm.NFC.String(s) // NFD and NFC spellings of a name are the same name
	key := make(NatKey, 0, 4)
	i := 0
	for i < len(s) {
		j := i
		if isDigit(s[i]) {
			for j < len(s) && isDigit(s[j]) {
				j++
			}
			key = append(key, natToken{num: true, digits: strings.TrimLeft(s[i:j], "0")})
		} else {
			for j < len(s) && !isDigit(s[j]) {
				j++
			}
			key = append(key, natToken{key: k.text(s[i:j])})
		}
		i = j
	}
	return key
}

func (k *keyer) text(run string) []byte {
	if b, ok := k.runs[run]; ok {
		return b
	}
	b := append([]byte(nil), k.c.KeyFromString(&k.buf, run)...)
	k.buf.Reset()
	if k.runs != nil && len(k.runs) < maxMemoRuns {
		k.runs[run] = b
	}
	return b
}

func isDigit(b byte) bool { return b >= '0' && b <= '9' }

// CompareNatural orders two keys. Numbers sort before text at the same
// position; equal keys return 0 (callers tie-break byte-wise).
func CompareNatural(a, b NatKey) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		ta, tb := a[i], b[i]
		switch {
		case ta.num && tb.num:
			if len(ta.digits) != len(tb.digits) {
				if len(ta.digits) < len(tb.digits) {
					return -1
				}
				return 1
			}
			if r := strings.Compare(ta.digits, tb.digits); r != 0 {
				return r
			}
		case ta.num:
			return -1
		case tb.num:
			return 1
		default:
			if r := bytes.Compare(ta.key, tb.key); r != 0 {
				return r
			}
		}
	}
	switch {
	case len(a) < len(b):
		return -1
	case len(a) > len(b):
		return 1
	}
	return 0
}

// NaturalLess reports whether a sorts before b (SPEC §8.3): natural runs,
// case-insensitive, with a byte-wise tie-break so the order is total.
func NaturalLess(a, b string, c *collate.Collator) bool {
	if r := CompareNatural(NaturalKey(a, c), NaturalKey(b, c)); r != 0 {
		return r < 0
	}
	return a < b
}

// comparator implements the layered order of SPEC §8.3 step 4:
// pinned → folders first → field → field2 → natural name → path.
type comparator struct{ spec SortSpec }

func (c comparator) compare(a, b *Item) int {
	if a.Pinned != b.Pinned {
		if a.Pinned {
			return -1
		}
		return 1
	}
	if c.spec.FoldersFirst && a.IsDir != b.IsDir {
		if a.IsDir {
			return -1
		}
		return 1
	}
	if r := compareField(c.spec.Field, c.spec.Desc, a, b); r != 0 {
		return r
	}
	if c.spec.Field2 != "" {
		if r := compareField(c.spec.Field2, c.spec.Desc2, a, b); r != 0 {
			return r
		}
	}
	if c.spec.Field != FieldName {
		if r := CompareNatural(a.nameKey, b.nameKey); r != 0 {
			return r
		}
	}
	return strings.Compare(a.Path, b.Path)
}

// fieldValue extracts the sortable value of f. known=false means "unknown"
// and always sorts last regardless of direction.
type fieldValue struct {
	i int64
	s string
	k NatKey
}

func value(f Field, it *Item) (fieldValue, bool) {
	switch f {
	case FieldName:
		return fieldValue{k: it.nameKey, s: it.Name}, true
	case FieldSize:
		if it.IsDir {
			if it.ItemCount != nil {
				return fieldValue{i: *it.ItemCount}, true
			}
			return fieldValue{i: 0}, true
		}
		return fieldValue{i: it.Size}, true
	case FieldType:
		return fieldValue{s: it.TypeLabel}, true
	case FieldExt:
		return fieldValue{s: it.Ext}, it.Ext != ""
	case FieldMtime:
		return fieldValue{i: it.mtime.UnixNano()}, true
	case FieldAtime:
		return fieldValue{i: it.atime.UnixNano()}, true
	case FieldBtime:
		if it.btime == nil {
			return fieldValue{}, false
		}
		return fieldValue{i: it.btime.UnixNano()}, true
	case FieldMode:
		return fieldValue{i: int64(it.modeNum)}, true
	case FieldNote:
		if it.Note == nil || *it.Note == "" {
			return fieldValue{}, false
		}
		return fieldValue{s: *it.Note}, true
	case FieldDimensions:
		if it.Media == nil || it.Media.Width <= 0 || it.Media.Height <= 0 {
			return fieldValue{}, false
		}
		return fieldValue{i: int64(it.Media.Width) * int64(it.Media.Height)}, true
	case FieldDuration:
		if it.Media == nil || it.Media.DurationMs <= 0 {
			return fieldValue{}, false
		}
		return fieldValue{i: it.Media.DurationMs}, true
	}
	return fieldValue{}, false
}

func compareField(f Field, desc bool, a, b *Item) int {
	va, oka := value(f, a)
	vb, okb := value(f, b)
	switch {
	case !oka && !okb:
		return 0
	case !oka:
		return 1
	case !okb:
		return -1
	}
	var r int
	switch f {
	case FieldName:
		r = CompareNatural(va.k, vb.k)
		if r == 0 {
			r = strings.Compare(va.s, vb.s)
		}
	case FieldType, FieldExt, FieldNote:
		r = strings.Compare(strings.ToLower(va.s), strings.ToLower(vb.s))
	default:
		switch {
		case va.i < vb.i:
			r = -1
		case va.i > vb.i:
			r = 1
		}
	}
	if desc {
		r = -r
	}
	return r
}

// sortItems sorts in place (stable) and is the single sorting entry point.
func sortItems(items []*Item, spec SortSpec) {
	c := comparator{spec: spec}
	slices.SortStableFunc(items, c.compare)
}
