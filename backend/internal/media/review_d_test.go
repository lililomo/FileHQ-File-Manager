package media

import (
	"bytes"
	"encoding/binary"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// Review D (Step R-D): fuzz targets for the two parsers that read untrusted
// bytes — the EXIF/TIFF walker and the WebVTT/SubRip sanitiser — plus the
// invariants the sanitiser must keep for every input.

// FuzzParseTIFF: no panic, bounded time, strings sanitised, GPS in range.
func FuzzParseTIFF(f *testing.F) {
	for _, bo := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		f.Add(sampleTIFF(bo))
		f.Add(buildTIFF(bo, []tagVal{asciiTag(tagMake, "X")}, nil, nil))
	}
	f.Add([]byte("II*\x00\x08\x00\x00\x00"))
	f.Add([]byte("MM\x00*\x00\x00\x00\x08\x00\x01"))
	f.Add(bytes.Repeat([]byte{0xff}, 64))
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > MaxExifBytes {
			data = data[:MaxExifBytes]
		}
		start := time.Now()
		ex, err := ParseTIFF(data)
		if d := time.Since(start); d > 200*time.Millisecond {
			t.Fatalf("ParseTIFF took %s for %d bytes", d, len(data))
		}
		if err != nil || ex == nil {
			return
		}
		checkExifInvariants(t, ex)
	})
}

// FuzzExtractExif: the container walkers (JPEG/PNG/WebP/TIFF/HEIF) never
// panic or read unboundedly on arbitrary bytes.
func FuzzExtractExif(f *testing.F) {
	tiff := sampleTIFF(binary.LittleEndian)
	f.Add(jpegWithAPP1(append([]byte("Exif\x00\x00"), tiff...)))
	f.Add(pngWithExif(tiff))
	f.Add(webpWithExif(tiff))
	f.Add(heifWithExif(tiff))
	f.Add(tiff)
	f.Add([]byte("\xff\xd8\xff\xe1\x00\x02"))
	f.Add([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\x00eXIf"))
	f.Add([]byte("RIFF\x00\x00\x00\x00WEBPEXIF\xff\xff\xff\xff"))
	f.Add([]byte("\x00\x00\x00\x10ftypheic\x00\x00\x00\x01meta\xff\xff\xff\xff\xff\xff\xff\xff"))
	f.Fuzz(func(t *testing.T, data []byte) {
		start := time.Now()
		raw, err := ExtractExif(bytes.NewReader(data))
		if d := time.Since(start); d > 200*time.Millisecond {
			t.Fatalf("ExtractExif took %s for %d bytes", d, len(data))
		}
		if err != nil {
			return
		}
		if len(raw) > MaxExifBytes+16 {
			t.Fatalf("extracted %d bytes > limit", len(raw))
		}
		if ex, perr := ParseTIFF(raw); perr == nil && ex != nil {
			checkExifInvariants(t, ex)
		}
	})
}

func checkExifInvariants(t *testing.T, ex *Exif) {
	t.Helper()
	for _, s := range []string{ex.Make, ex.Model, ex.Lens, ex.Software, ex.DateTime, ex.DateTimeOriginal} {
		if len(s) > 256 {
			t.Fatalf("string longer than 256: %d", len(s))
		}
		for _, r := range s {
			if r < 0x20 || r == 0x7f {
				t.Fatalf("control character in %q", s)
			}
		}
		if !utf8.ValidString(s) {
			t.Fatalf("invalid utf-8 in %q", s)
		}
	}
	if ex.GPS != nil {
		if ex.GPS.Lat < -90 || ex.GPS.Lat > 90 || ex.GPS.Lon < -180 || ex.GPS.Lon > 180 || (ex.GPS.Lat == 0 && ex.GPS.Lon == 0) {
			t.Fatalf("gps out of range %+v", ex.GPS)
		}
	}
	if ex.Orientation < 0 || ex.Orientation > 8 || ex.Width < 0 || ex.Height < 0 || ex.ISO < 0 {
		t.Fatalf("numeric field out of range %+v", ex)
	}
}

var (
	vttTimingLine = regexp.MustCompile(`^\d{2,4}:\d{2}:\d{2}\.\d{3} --> \d{2,4}:\d{2}:\d{2}\.\d{3}( [a-z]+:[A-Za-z0-9.,%-]+)*$`)
	vttTag        = regexp.MustCompile(`</?[a-zA-Z]`)
)

// checkVTTInvariants: the document is "WEBVTT" + cues only; every cue is a
// valid timing line followed by non-blank text lines where the only tags
// are <b>, <i>, <u> (and their closers), no raw "<", ">" or "&" survive
// outside entities and no text line can be mistaken for a timing line.
func checkVTTInvariants(t *testing.T, out string) {
	t.Helper()
	if !strings.HasPrefix(out, "WEBVTT\n\n") {
		t.Fatalf("missing header: %q", out)
	}
	lines := strings.Split(strings.TrimSuffix(out[len("WEBVTT\n\n"):], "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		return
	}
	inCue := false
	for _, line := range lines {
		switch {
		case line == "":
			inCue = false
		case !inCue:
			if !vttTimingLine.MatchString(line) {
				t.Fatalf("expected timing line, got %q in\n%s", line, out)
			}
			inCue = true
		default:
			if strings.Contains(line, "-->") {
				t.Fatalf("arrow in cue text %q", line)
			}
			if strings.TrimSpace(line) == "" {
				t.Fatalf("blank text line inside cue")
			}
			stripped := strings.NewReplacer("<b>", "", "</b>", "", "<i>", "", "</i>", "", "<u>", "", "</u>", "",
				"&amp;", "", "&lt;", "", "&gt;", "", "&nbsp;", "", "&lrm;", "", "&rlm;", "").Replace(line)
			if strings.ContainsAny(stripped, "<>&") || vttTag.MatchString(stripped) {
				t.Fatalf("markup leaked in %q (stripped %q)", line, stripped)
			}
		}
	}
}

func FuzzSanitizeVTT(f *testing.F) {
	f.Add("WEBVTT\n\n00:00:01.000 --> 00:00:02.000 line:50% align:start\n<b>bold</b> <script>x</script>\n")
	f.Add("1\n00:00:01,000 --> 00:00:02,000\n<font color=red>hi</font>\n\n2\n00:00:03,000 --> 00:00:04,000\n-->\n")
	f.Add("00:01.000 --> 00:02.000\ntext\n\n\n00:03.000 --> 00:04.000\n<v Bob><c.red>x</c></v> <00:01.500>k\n")
	f.Add("STYLE\n::cue { color: red }\n\nNOTE x\n\n99:59:59.999 --> 100:00:00.000\n&amp; &lt; &gt; &bogus; a<b\n")
	f.Add("\xef\xbb\xbfWEBVTT\r\n\r\n00:00:00.000 --> 00:00:01.000\r\nline\r\n")
	f.Fuzz(func(t *testing.T, src string) {
		if len(src) > MaxSubtitleBytes {
			src = src[:MaxSubtitleBytes]
		}
		start := time.Now()
		out := SanitizeVTT(src)
		if d := time.Since(start); d > 500*time.Millisecond {
			t.Fatalf("SanitizeVTT took %s for %d bytes", d, len(src))
		}
		checkVTTInvariants(t, out)
		if again := SanitizeVTT(out); again != out {
			t.Fatalf("not idempotent:\n%q\n%q", out, again)
		}
		srt := SRTToVTT(src)
		checkVTTInvariants(t, srt)
	})
}

// TestReviewDSanitizerTable: explicit hostile inputs (checklist a).
func TestReviewDSanitizerTable(t *testing.T) {
	cases := map[string]string{
		"script":        "00:00:01.000 --> 00:00:02.000\n<script>alert(1)</script>",
		"img":           "00:00:01.000 --> 00:00:02.000\n<img src=x onerror=alert(1)>",
		"font":          "00:00:01.000 --> 00:00:02.000\n<font color=red>x</font>",
		"class":         "00:00:01.000 --> 00:00:02.000\n<c.red.bg_blue>x</c>",
		"voice":         "00:00:01.000 --> 00:00:02.000\n<v Bob>x</v>",
		"karaoke":       "00:00:01.000 --> 00:00:02.000\n<00:00:01.500>x<00:00:01.900>y",
		"arrow":         "00:00:01.000 --> 00:00:02.000\nx --> y\n00:00:05.000 --> 00:00:06.000 injected",
		"blank":         "00:00:01.000 --> 00:00:02.000\nx\n\n\ny",
		"settings":      "00:00:01.000 --> 00:00:02.000 line:1 line:abc position:50%,center size:200% align:middle vertical:lr region:r1\nx",
		"style":         "STYLE\n::cue { background: url(javascript:1) }\n\n00:00:01.000 --> 00:00:02.000\nx",
		"entity":        "00:00:01.000 --> 00:00:02.000\n&lt;b&gt; &amp;amp; &#60;script&#62; &unknown;",
		"attr-tag":      "00:00:01.000 --> 00:00:02.000\n<b onclick=x>b</b><i\n>i</i><u/>",
		"timestamp-ovf": "00:00:01.000 --> 00:00:02.000\n<99999999999999999999:00:00.000>x",
		"bad-minutes":   "00:60:01.000 --> 00:61:02.000\nx",
		"hours-5digits": "12345:00:01.000 --> 12345:00:02.000\nx",
		"null-bytes":    "00:00:01.000 --> 00:00:02.000\nx\x00y<b\x00>z",
		"nested":        "00:00:01.000 --> 00:00:02.000\n<b><i><u><b><i><u>deep</u></i></b></u></i></b>",
		"unicode-lt":    "00:00:01.000 --> 00:00:02.000\n＜script＞ ＜b＞",
	}
	for name, src := range cases {
		out := SanitizeVTT(src)
		checkVTTInvariants(t, out)
		if again := SanitizeVTT(out); again != out {
			t.Fatalf("%s: not idempotent\n%q\n%q", name, out, again)
		}
		t.Logf("%-14s → %q", name, strings.TrimPrefix(out, "WEBVTT\n\n"))
	}
	// size:200% is syntactically valid (the browser clamps it); the malformed
	// and unknown settings are dropped
	if out := SanitizeVTT(cases["settings"]); !strings.Contains(out, " line:1 position:50%,center size:200% vertical:lr\n") || strings.Contains(out, "line:abc") || strings.Contains(out, "align:middle") || strings.Contains(out, "region") {
		t.Fatalf("settings: %q", out)
	}
	if out := SanitizeVTT(cases["bad-minutes"]); strings.Contains(out, "-->") {
		t.Fatalf("bad minutes accepted: %q", out)
	}
	if out := SanitizeVTT(cases["hours-5digits"]); strings.Contains(out, "-->") {
		t.Fatalf("5-digit hours accepted: %q", out)
	}
}
