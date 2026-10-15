package files

import (
	"bytes"
	"errors"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/saintfish/chardet"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// Text encodings of the editor (SPEC §8.13 "Membuka file" step 3): BOM →
// certain; valid UTF-8 → utf-8; otherwise chardet over the candidate list
// with confidence ≥ 50 %, fallback windows-1252. Names are the lowercase
// IANA-style labels the API exchanges.

const (
	EncUTF8    = "utf-8"
	EncUTF16LE = "utf-16le"
	EncUTF16BE = "utf-16be"
)

// encodingDef binds an API name to its x/text implementation.
type encodingDef struct {
	name    string
	enc     encoding.Encoding
	chardet string // chardet charset label(s) that map here, comma separated
}

var encodingDefs = []encodingDef{
	{EncUTF8, unicode.UTF8, "UTF-8"},
	{EncUTF16LE, unicode.UTF16(unicode.LittleEndian, unicode.IgnoreBOM), "UTF-16LE"},
	{EncUTF16BE, unicode.UTF16(unicode.BigEndian, unicode.IgnoreBOM), "UTF-16BE"},
	{"gbk", simplifiedchinese.GBK, "GB-18030,GB18030,GBK,GB2312"},
	{"gb18030", simplifiedchinese.GB18030, ""},
	{"big5", traditionalchinese.Big5, "Big5"},
	{"shift_jis", japanese.ShiftJIS, "Shift_JIS"},
	{"euc-jp", japanese.EUCJP, "EUC-JP"},
	{"iso-2022-jp", japanese.ISO2022JP, "ISO-2022-JP"},
	{"euc-kr", korean.EUCKR, "EUC-KR"},
	{"windows-1252", charmap.Windows1252, "windows-1252,ISO-8859-1"},
	{"iso-8859-1", charmap.ISO8859_1, ""},
	{"iso-8859-2", charmap.ISO8859_2, "ISO-8859-2"},
	{"iso-8859-15", charmap.ISO8859_15, "ISO-8859-15"},
	{"windows-1250", charmap.Windows1250, "windows-1250"},
	{"windows-1251", charmap.Windows1251, "windows-1251"},
	{"koi8-r", charmap.KOI8R, "KOI8-R"},
}

// detectCandidates are the chardet charsets the editor trusts (SPEC §8.13).
var detectCandidates = map[string]string{}

func init() {
	for _, d := range encodingDefs {
		for _, label := range strings.Split(d.chardet, ",") {
			if label != "" {
				detectCandidates[strings.ToLower(label)] = d.name
			}
		}
	}
}

var encodingAliases = map[string]string{
	"utf8": EncUTF8, "utf-8-sig": EncUTF8, "utf16le": EncUTF16LE, "utf-16": EncUTF16LE, "utf16be": EncUTF16BE,
	"latin1": "iso-8859-1", "latin-1": "iso-8859-1", "cp1252": "windows-1252", "cp1251": "windows-1251", "cp1250": "windows-1250",
	"sjis": "shift_jis", "shift-jis": "shift_jis", "ms932": "shift_jis", "cp932": "shift_jis", "gb2312": "gbk", "cp936": "gbk",
	"big-5": "big5", "cp950": "big5", "euckr": "euc-kr", "cp949": "euc-kr", "eucjp": "euc-jp", "koi8r": "koi8-r",
}

// Encodings lists the supported API names in a stable order.
func Encodings() []string {
	out := make([]string, 0, len(encodingDefs))
	for _, d := range encodingDefs {
		out = append(out, d.name)
	}
	return out
}

// NormalizeEncoding maps user input to a supported name ("" when unknown).
func NormalizeEncoding(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	if a, ok := encodingAliases[n]; ok {
		n = a
	}
	for _, d := range encodingDefs {
		if d.name == n {
			return n
		}
	}
	return ""
}

func encodingByName(name string) (encodingDef, bool) {
	for _, d := range encodingDefs {
		if d.name == name {
			return d, true
		}
	}
	return encodingDef{}, false
}

// Detected is the outcome of DetectEncoding.
type Detected struct {
	Encoding  string
	BOM       bool
	Confident bool // BOM, valid UTF-8 or chardet ≥ 50 %
}

var (
	bomUTF8    = []byte{0xEF, 0xBB, 0xBF}
	bomUTF16LE = []byte{0xFF, 0xFE}
	bomUTF16BE = []byte{0xFE, 0xFF}
)

// DetectEncoding inspects data (the whole file or a large prefix).
func DetectEncoding(data []byte) Detected {
	switch {
	case bytes.HasPrefix(data, bomUTF8):
		return Detected{Encoding: EncUTF8, BOM: true, Confident: true}
	case bytes.HasPrefix(data, bomUTF16LE):
		return Detected{Encoding: EncUTF16LE, BOM: true, Confident: true}
	case bytes.HasPrefix(data, bomUTF16BE):
		return Detected{Encoding: EncUTF16BE, BOM: true, Confident: true}
	}
	if utf8.Valid(data) {
		return Detected{Encoding: EncUTF8, Confident: true}
	}
	if len(data) > 64<<10 {
		data = data[:64<<10]
	}
	res, err := chardet.NewTextDetector().DetectAll(data)
	if err == nil {
		sort.SliceStable(res, func(i, j int) bool { return res[i].Confidence > res[j].Confidence })
		for _, r := range res {
			name, ok := detectCandidates[strings.ToLower(r.Charset)]
			if !ok || name == EncUTF8 || name == EncUTF16LE || name == EncUTF16BE {
				continue
			}
			if r.Confidence >= 50 {
				return Detected{Encoding: name, Confident: true}
			}
			break
		}
	}
	return Detected{Encoding: "windows-1252"}
}

// DecodeText converts data from enc to a Go (UTF-8) string, dropping a BOM.
// lossy reports characters that had no mapping (replaced by U+FFFD).
func DecodeText(data []byte, enc string) (text string, lossy bool, err error) {
	d, ok := encodingByName(enc)
	if !ok {
		return "", false, errors.New("unsupported encoding")
	}
	switch enc {
	case EncUTF8:
		data = bytes.TrimPrefix(data, bomUTF8)
		if utf8.Valid(data) {
			return string(data), false, nil
		}
		return strings.ToValidUTF8(string(data), "�"), true, nil
	case EncUTF16LE:
		data = bytes.TrimPrefix(data, bomUTF16LE)
	case EncUTF16BE:
		data = bytes.TrimPrefix(data, bomUTF16BE)
	}
	out, err := d.enc.NewDecoder().Bytes(data)
	if err != nil {
		return "", false, err
	}
	text = string(out)
	lossy = strings.ContainsRune(text, '�')
	return text, lossy, nil
}

// EncodeError reports a character the target encoding cannot represent.
type EncodeError struct {
	Encoding string
	Offset   int // byte offset in the UTF-8 text
	Line     int // 1-based
	Col      int // 1-based, in runes
	Rune     rune
}

func (e *EncodeError) Error() string {
	return "character " + string(e.Rune) + " cannot be encoded as " + e.Encoding
}

// EncodeText converts UTF-8 text to enc, prefixing a BOM when asked (UTF-16
// always gets one; UTF-8 only when bom is true). The first unrepresentable
// character aborts with an EncodeError carrying its position.
func EncodeText(text, enc string, bom bool) ([]byte, error) {
	d, ok := encodingByName(enc)
	if !ok {
		return nil, errors.New("unsupported encoding")
	}
	var out bytes.Buffer
	switch enc {
	case EncUTF8:
		if bom {
			out.Write(bomUTF8)
		}
		out.WriteString(text)
		return out.Bytes(), nil
	case EncUTF16LE:
		out.Write(bomUTF16LE)
	case EncUTF16BE:
		out.Write(bomUTF16BE)
	}
	t := d.enc.NewEncoder()
	src := []byte(text)
	dst := make([]byte, 0, len(src)+16)
	buf := make([]byte, 4096)
	consumed := 0
	for {
		nDst, nSrc, err := t.Transform(buf, src[consumed:], true)
		dst = append(dst, buf[:nDst]...)
		consumed += nSrc
		switch {
		case err == nil:
			out.Write(dst)
			return out.Bytes(), nil
		case errors.Is(err, transform.ErrShortDst):
			continue
		case errors.Is(err, transform.ErrShortSrc):
			continue
		default:
			r, _ := utf8.DecodeRune(src[consumed:])
			line, col := positionOf(text, consumed)
			return nil, &EncodeError{Encoding: enc, Offset: consumed, Line: line, Col: col, Rune: r}
		}
	}
}

// positionOf returns the 1-based line and rune column of byte offset off.
func positionOf(text string, off int) (line, col int) {
	if off > len(text) {
		off = len(text)
	}
	line = 1 + strings.Count(text[:off], "\n")
	start := strings.LastIndexByte(text[:off], '\n') + 1
	col = 1 + utf8.RuneCountInString(text[start:off])
	return line, col
}

// EOL labels (SPEC §8.13 step 4).
const (
	EOLLF    = "lf"
	EOLCRLF  = "crlf"
	EOLMixed = "mixed"
	EOLKeep  = "keep"
)

// DetectEOL counts CRLF vs lone LF: crlf when every newline is CRLF, lf
// when none is, mixed otherwise; a file without newlines reports lf.
func DetectEOL(text string) string {
	crlf := strings.Count(text, "\r\n")
	lf := strings.Count(text, "\n") - crlf
	switch {
	case crlf > 0 && lf == 0:
		return EOLCRLF
	case crlf > 0 && lf > 0:
		return EOLMixed
	default:
		return EOLLF
	}
}

// ConvertEOL rewrites every line ending to eol (lf or crlf); keep (or any
// other value) leaves the text untouched.
func ConvertEOL(text, eol string) string {
	switch eol {
	case EOLLF:
		return strings.ReplaceAll(text, "\r\n", "\n")
	case EOLCRLF:
		return strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\n", "\r\n")
	}
	return text
}
