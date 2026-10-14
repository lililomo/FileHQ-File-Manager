package media

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseFFprobeAudioWithCover(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "ffprobe_mp3.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := Meta{Mime: "audio/mpeg", Kind: KindAudio}
	if err := ParseFFprobe(data, &m); err != nil {
		t.Fatal(err)
	}
	if m.DurationMs != 1045 || m.Bitrate != 124222 {
		t.Fatalf("duration/bitrate = %d/%d", m.DurationMs, m.Bitrate)
	}
	if !m.HasCover || m.CodecVideo != "" || m.Width != 0 {
		t.Fatalf("attached picture must become HasCover, got %+v", m)
	}
	if m.CodecAudio != "mp3" {
		t.Fatalf("codec audio = %q", m.CodecAudio)
	}
	if m.Tags == nil || m.Tags.Title != "Lagu Uji" || m.Tags.Artist != "Tester" {
		t.Fatalf("tags = %+v", m.Tags)
	}
}

func TestParseFFprobeVideo(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "ffprobe_mp4.json"))
	if err != nil {
		t.Fatal(err)
	}
	m := Meta{Mime: "video/mp4", Kind: KindVideo}
	if err := ParseFFprobe(data, &m); err != nil {
		t.Fatal(err)
	}
	if m.Width != 320 || m.Height != 240 || m.CodecVideo != "h264" {
		t.Fatalf("video stream = %dx%d %s", m.Width, m.Height, m.CodecVideo)
	}
	if m.DurationMs != 2000 || m.HasCover {
		t.Fatalf("duration=%d cover=%v", m.DurationMs, m.HasCover)
	}
	if m.Tags != nil {
		t.Fatalf("no tags expected, got %+v", m.Tags)
	}
}

func TestParseFFprobeGarbage(t *testing.T) {
	m := Meta{}
	if err := ParseFFprobe([]byte("not json"), &m); err == nil {
		t.Fatal("garbage accepted")
	}
	if err := ParseFFprobe([]byte(`{"streams":[{"codec_type":"video","duration":"abc"}]}`), &m); err != nil || m.DurationMs != 0 {
		t.Fatalf("bad duration should be ignored: %v %d", err, m.DurationMs)
	}
}

func TestKindOf(t *testing.T) {
	cases := []struct {
		mime, name string
		want       Kind
	}{
		{"image/jpeg", "a.jpg", KindImage},
		{"image/svg+xml", "a.svg", KindSVG},
		{"video/x-matroska", "a.mkv", KindVideo},
		{"audio/mpeg", "a.mp3", KindAudio},
		{"application/pdf", "a.pdf", KindPDF},
		{"application/octet-stream", "a.heic", KindImage}, // extension fallback
		{"application/octet-stream", "a.svg", KindSVG},
		{"text/plain", "a.txt", KindOther},
		{"application/octet-stream", "noext", KindOther},
	}
	for _, c := range cases {
		if got := KindOf(c.mime, c.name); got != c.want {
			t.Errorf("KindOf(%q,%q)=%q want %q", c.mime, c.name, got, c.want)
		}
	}
}
