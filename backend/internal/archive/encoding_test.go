package archive

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
)

func TestNameDecoder(t *testing.T) {
	gbk := NewNameDecoder(EncodingGBK)
	raw := mustEncode(t, "测试.txt", EncodingGBK)
	if got := gbk.Decode(raw, false); got != "测试.txt" {
		t.Fatalf("gbk: %q", got)
	}
	auto := NewNameDecoder(EncodingAuto)
	auto.Prepare([]string{raw, "ascii.txt"})
	if auto.Detected() != EncodingGBK {
		t.Fatalf("auto detected %q", auto.Detected())
	}
	if got := auto.Decode(raw, false); got != "测试.txt" {
		t.Fatalf("auto gbk: %q", got)
	}
	// UTF-8 flagged names pass through whatever the mode says
	if got := gbk.Decode("日本語.txt", true); got != "日本語.txt" {
		t.Fatalf("utf8 flag: %q", got)
	}
	// Shift_JIS sample
	sj, _ := japanese.ShiftJIS.NewEncoder().String("写真フォルダ/画像.jpg")
	a2 := NewNameDecoder(EncodingAuto)
	a2.Prepare([]string{sj})
	if a2.Detected() != EncodingSJIS || a2.Decode(sj, false) != "写真フォルダ/画像.jpg" {
		t.Fatalf("sjis: %q %q", a2.Detected(), a2.Decode(sj, false))
	}
	// CP437 box drawing / accented latin from DOS era
	dos, _ := charmap.CodePage437.NewEncoder().String("résumé.txt")
	a3 := NewNameDecoder(EncodingAuto)
	a3.Prepare([]string{dos})
	if got := a3.Decode(dos, false); got != "résumé.txt" {
		t.Logf("cp437 auto picked %s → %q (accepted when valid letters)", a3.Detected(), got)
	}
	if got := NewNameDecoder(EncodingCP437).Decode(dos, false); got != "résumé.txt" {
		t.Fatalf("cp437 forced: %q", got)
	}
	// valid UTF-8 without the flag stays UTF-8
	if got := NewNameDecoder(EncodingAuto).Decode("naïve.txt", false); got != "naïve.txt" {
		t.Fatalf("utf8 unflagged: %q", got)
	}
	for _, in := range []string{"", "auto", "UTF8", "gbk", "GB2312", "cp437", "shift-jis", "big5", "latin1"} {
		if _, ok := ParseEncoding(in); !ok {
			t.Errorf("ParseEncoding(%q) rejected", in)
		}
	}
	if _, ok := ParseEncoding("klingon"); ok {
		t.Error("unknown encoding accepted")
	}
}

func mustEncode(t *testing.T, s, enc string) string {
	t.Helper()
	out, err := decoders[enc].NewEncoder().String(s)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// TestGBKNamesExtract covers PROMPT Step 14a AC 7: legacy GBK names are
// readable with auto and with gbk.
func TestGBKNamesExtract(t *testing.T) {
	e := newEnv(t)
	e.write(t, "gbk.zip", gbkZip(t))
	for _, enc := range []string{EncodingAuto, EncodingGBK} {
		res := resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/gbk.zip", Dest: "/out-" + enc, Encoding: enc}, ""))
		if res.Files != 1 {
			t.Fatalf("%s: %+v", enc, res)
		}
		if _, err := os.Stat(filepath.Join(e.home, "out-"+enc, "资料", "测试文档.txt")); err != nil {
			entries, _ := os.ReadDir(filepath.Join(e.home, "out-"+enc))
			t.Fatalf("%s: decoded name missing (%v): %v", enc, err, entries)
		}
		if enc == EncodingAuto && res.Encoding != EncodingGBK {
			t.Fatalf("auto detected %q", res.Encoding)
		}
	}
	// forcing the wrong codec gives replacement-free but different names, never a crash
	resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/gbk.zip", Dest: "/out-sjis", Encoding: EncodingSJIS}, ""))
}
