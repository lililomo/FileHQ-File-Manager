package files

import (
	"bytes"
	"errors"
	"testing"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
)

func mustEncode(t *testing.T, enc string, text string) []byte {
	t.Helper()
	switch enc {
	case "shift_jis":
		b, err := japanese.ShiftJIS.NewEncoder().Bytes([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		return b
	case "gbk":
		b, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		return b
	case "windows-1252":
		b, err := charmap.Windows1252.NewEncoder().Bytes([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		return b
	case EncUTF16LE:
		b, err := unicode.UTF16(unicode.LittleEndian, unicode.UseBOM).NewEncoder().Bytes([]byte(text))
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	t.Fatalf("unknown %s", enc)
	return nil
}

// TestDetectAndRoundTrip: every supported route of SPEC §8.13 step 3 is
// detected (BOM certain, UTF-8 by validity, legacy by chardet) and encoding
// the decoded text back yields the original bytes.
func TestDetectAndRoundTrip(t *testing.T) {
	jp := "日本語のテキストです。これはシフトJISで保存されたファイルです。\n二行目もあります。\n"
	zh := "这是一个用GBK编码保存的中文文本文件。它包含了几行中文内容，用于编码检测。\n第二行内容。\n"
	cases := []struct {
		name string
		data []byte
		enc  string
		bom  bool
		text string
	}{
		{"utf8", []byte("héllo wörld\n"), EncUTF8, false, "héllo wörld\n"},
		{"utf8-bom", append([]byte{0xEF, 0xBB, 0xBF}, []byte("abc\n")...), EncUTF8, true, "abc\n"},
		{"utf16le-bom", mustEncode(t, EncUTF16LE, "hi ☃\r\n"), EncUTF16LE, true, "hi ☃\r\n"},
		{"shift_jis", mustEncode(t, "shift_jis", jp), "shift_jis", false, jp},
		{"gbk", mustEncode(t, "gbk", zh), "gbk", false, zh},
	}
	for _, c := range cases {
		det := DetectEncoding(c.data)
		if det.Encoding != c.enc || det.BOM != c.bom {
			t.Fatalf("%s: detected %+v, want %s bom=%v", c.name, det, c.enc, c.bom)
		}
		text, lossy, err := DecodeText(c.data, det.Encoding)
		if err != nil || lossy || text != c.text {
			t.Fatalf("%s: decode %q lossy=%v err=%v", c.name, text, lossy, err)
		}
		back, err := EncodeText(text, det.Encoding, det.BOM)
		if err != nil || !bytes.Equal(back, c.data) {
			t.Fatalf("%s: round trip differs (%v): %x vs %x", c.name, err, back, c.data)
		}
	}
}

// TestDetectFallbackAndLossy: bytes that are no valid UTF-8 and no
// confident legacy charset fall back to windows-1252; undecodable bytes
// are flagged lossy; a UTF-8 file with a broken sequence is lossy too.
func TestDetectFallbackAndLossy(t *testing.T) {
	data := []byte("caf\xe9 au lait\n")
	det := DetectEncoding(data)
	if det.Encoding != "windows-1252" && det.Encoding != "iso-8859-1" {
		t.Fatalf("latin bytes: %+v", det)
	}
	text, lossy, err := DecodeText(data, "windows-1252")
	if err != nil || lossy || text != "café au lait\n" {
		t.Fatalf("1252: %q %v %v", text, lossy, err)
	}
	_, lossy, _ = DecodeText([]byte("x \x81 y"), "windows-1252")
	if !lossy {
		t.Fatal("0x81 has no mapping in windows-1252: must be lossy")
	}
	_, lossy, _ = DecodeText([]byte("ok \xff\xfe bad"), EncUTF8)
	if !lossy {
		t.Fatal("invalid UTF-8 must be lossy")
	}
}

// TestEncodeUnrepresentable reports the first character the target
// charset lacks, with its line and column.
func TestEncodeUnrepresentable(t *testing.T) {
	_, err := EncodeText("first line\nsecond ☃ here\n", "windows-1252", false)
	var ee *EncodeError
	if !errors.As(err, &ee) {
		t.Fatalf("want EncodeError, got %v", err)
	}
	if ee.Line != 2 || ee.Col != 8 || ee.Rune != '☃' {
		t.Fatalf("position: %+v", ee)
	}
	if _, err := EncodeText("plain ascii", "shift_jis", false); err != nil {
		t.Fatal(err)
	}
	if n := NormalizeEncoding("UTF8"); n != EncUTF8 {
		t.Fatalf("alias: %q", n)
	}
	if n := NormalizeEncoding("klingon"); n != "" {
		t.Fatalf("unknown must be empty: %q", n)
	}
}

func TestEOL(t *testing.T) {
	for _, c := range []struct{ text, eol string }{
		{"a\nb\n", EOLLF}, {"a\r\nb\r\n", EOLCRLF}, {"a\r\nb\n", EOLMixed}, {"single", EOLLF}, {"", EOLLF},
	} {
		if got := DetectEOL(c.text); got != c.eol {
			t.Fatalf("%q: %s want %s", c.text, got, c.eol)
		}
	}
	if got := ConvertEOL("a\r\nb\nc", EOLCRLF); got != "a\r\nb\r\nc" {
		t.Fatalf("to crlf: %q", got)
	}
	if got := ConvertEOL("a\r\nb\nc", EOLLF); got != "a\nb\nc" {
		t.Fatalf("to lf: %q", got)
	}
	if got := ConvertEOL("a\r\nb\nc", EOLKeep); got != "a\r\nb\nc" {
		t.Fatalf("keep: %q", got)
	}
}
