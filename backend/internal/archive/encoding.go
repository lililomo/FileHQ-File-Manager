package archive

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

// Name encodings accepted by the API (SPEC §6.3 `encoding`).
const (
	EncodingAuto    = "auto"
	EncodingUTF8    = "utf-8"
	EncodingGBK     = "gbk"
	EncodingCP437   = "cp437"
	EncodingSJIS    = "shift_jis"
	EncodingBig5    = "big5"
	EncodingCP1252  = "windows-1252"
	EncodingDefault = EncodingAuto
)

// ParseEncoding normalises the API spelling ("" → auto, "utf8" → utf-8).
func ParseEncoding(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", EncodingAuto:
		return EncodingAuto, true
	case "utf8", "utf-8":
		return EncodingUTF8, true
	case EncodingGBK, "gb2312", "gb18030":
		return EncodingGBK, true
	case EncodingCP437, "ibm437", "dos":
		return EncodingCP437, true
	case EncodingSJIS, "shift-jis", "sjis", "cp932":
		return EncodingSJIS, true
	case EncodingBig5, "cp950":
		return EncodingBig5, true
	case EncodingCP1252, "cp1252", "latin1", "iso-8859-1":
		return EncodingCP1252, true
	}
	return "", false
}

var decoders = map[string]encoding.Encoding{
	EncodingGBK:    simplifiedchinese.GB18030,
	EncodingCP437:  charmap.CodePage437,
	EncodingSJIS:   japanese.ShiftJIS,
	EncodingBig5:   traditionalchinese.Big5,
	EncodingCP1252: charmap.Windows1252,
}

// autoOrder is the detection preference for legacy (non UTF-8) names.
var autoOrder = []string{EncodingGBK, EncodingSJIS, EncodingBig5, EncodingCP437, EncodingCP1252}

// NameDecoder turns raw entry names into UTF-8 (SPEC §8.11 step 5).
type NameDecoder struct {
	mode     string // auto | utf-8 | <legacy>
	detected string
}

// NewNameDecoder builds a decoder for the requested encoding.
func NewNameDecoder(mode string) *NameDecoder {
	if mode == "" {
		mode = EncodingAuto
	}
	return &NameDecoder{mode: mode}
}

// Detected returns the encoding chosen by auto mode ("" = not needed yet).
func (d *NameDecoder) Detected() string { return d.detected }

// Prepare lets auto mode pick one encoding for a whole archive from every
// raw (non UTF-8 flagged) name, so all entries agree.
func (d *NameDecoder) Prepare(rawNames []string) {
	if d.mode != EncodingAuto || d.detected != "" {
		return
	}
	legacy := make([]string, 0, len(rawNames))
	for _, n := range rawNames {
		if !isASCII(n) {
			legacy = append(legacy, n)
		}
	}
	if len(legacy) == 0 {
		return
	}
	joined := strings.Join(legacy, "\n")
	if utf8.ValidString(joined) {
		d.detected = EncodingUTF8
		return
	}
	d.detected = detectLegacy(joined)
}

// Decode converts one raw name. utf8Flag is true when the container says
// the name is UTF-8 already (zip general purpose bit 11).
func (d *NameDecoder) Decode(raw string, utf8Flag bool) string {
	if utf8Flag || isASCII(raw) {
		return raw
	}
	mode := d.mode
	if mode == EncodingAuto {
		if d.detected == "" {
			d.Prepare([]string{raw})
		}
		mode = d.detected
		if mode == "" || mode == EncodingUTF8 {
			if utf8.ValidString(raw) {
				return raw
			}
			mode = EncodingCP437
		}
	}
	if mode == EncodingUTF8 {
		if utf8.ValidString(raw) {
			return raw
		}
		return strings.ToValidUTF8(raw, "�")
	}
	enc, ok := decoders[mode]
	if !ok {
		return strings.ToValidUTF8(raw, "�")
	}
	out, err := enc.NewDecoder().String(raw)
	if err != nil || !utf8.ValidString(out) {
		return strings.ToValidUTF8(raw, "�")
	}
	return out
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// detectLegacy scores the candidate encodings: a decode must succeed without
// replacement characters; letters (CJK ideographs, kana, Latin) score, control
// and symbol-only output does not. Ties keep autoOrder.
func detectLegacy(sample string) string {
	best, bestScore := EncodingCP437, -1
	for _, name := range autoOrder {
		out, err := decoders[name].NewDecoder().String(sample)
		if err != nil || strings.ContainsRune(out, '�') || !utf8.ValidString(out) {
			continue
		}
		score := 0
		for _, r := range out {
			switch {
			case unicode.Is(unicode.Han, r):
				score += 3
			case r >= 0xFF61 && r <= 0xFF9F:
				// half-width katakana: the usual symptom of GBK/Big5 bytes read as Shift_JIS
				score -= 3
			case r >= 0xE000 && r <= 0xF8FF:
				// private use area (GB18030 maps unassigned pairs here)
				score -= 3
			case unicode.Is(unicode.Hiragana, r), unicode.Is(unicode.Katakana, r), unicode.Is(unicode.Hangul, r):
				// full-width kana/hangul only appear when the codec is right
				score += 6
			case unicode.IsLetter(r):
				score += 2
			case unicode.IsControl(r):
				score -= 5
			case unicode.IsSymbol(r):
				score--
			}
		}
		if score > bestScore {
			best, bestScore = name, score
		}
	}
	return best
}
