package download

import (
	"mime"
	"net/url"
	"strings"
	"testing"
)

func TestPolicyTable(t *testing.T) {
	cases := []struct {
		mime, name  string
		purpose     Purpose
		inline      bool
		contentType string
	}{
		{"image/jpeg", "foto.jpg", PurposeMedia, true, "image/jpeg"},
		{"image/png", "a.png", PurposeInline, true, "image/png"},
		{"image/webp", "a.webp", PurposeMedia, true, "image/webp"},
		{"image/gif", "a.gif", PurposeMedia, true, "image/gif"},
		{"image/avif", "a.avif", PurposeMedia, true, "image/avif"},
		{"image/bmp", "a.bmp", PurposeMedia, true, "image/bmp"},
		{"video/mp4", "a.mp4", PurposeMedia, true, "video/mp4"},
		{"video/webm", "a.webm", PurposeMedia, true, "video/webm"},
		{"audio/mpeg", "a.mp3", PurposeMedia, true, "audio/mpeg"},
		{"audio/flac", "a.flac", PurposeMedia, true, "audio/flac"},
		{"application/pdf", "a.pdf", PurposeMedia, false, "application/pdf"},
		{"text/plain", "a.txt", PurposeInline, false, "text/plain"},
		{"application/json", "a.json", PurposeInline, false, "application/json"},
		{"text/csv", "a.csv", PurposeInline, false, "text/csv"},
		// dangerous: always attachment + octet-stream, whatever the purpose
		{"text/html", "a.html", PurposeMedia, false, OctetStream},
		{"text/html; charset=utf-8", "a.html", PurposeInline, false, OctetStream},
		{"application/xhtml+xml", "a.xhtml", PurposeMedia, false, OctetStream},
		{"image/svg+xml", "a.svg", PurposeMedia, false, OctetStream},
		{"text/xml", "a.xml", PurposeMedia, false, OctetStream},
		{"application/javascript", "a.js", PurposeMedia, false, OctetStream},
		{"text/javascript", "a.mjs", PurposeMedia, false, OctetStream},
		{"text/css", "a.css", PurposeMedia, false, OctetStream},
		{"application/x-shockwave-flash", "a.swf", PurposeMedia, false, OctetStream},
		// extension says dangerous even when bytes look harmless
		{"text/plain", "page.html", PurposeMedia, false, OctetStream},
		{"text/plain", "page.HTM", PurposeMedia, false, OctetStream},
		{"text/plain", "x.svg", PurposeMedia, false, OctetStream},
		{"text/plain", "x.js", PurposeInline, false, OctetStream},
		{"application/octet-stream", "x.xml", PurposeInline, false, OctetStream},
		{"application/vnd.something+xml", "x.bin", PurposeInline, false, OctetStream},
		// download purpose never inlines
		{"image/jpeg", "foto.jpg", PurposeDownload, false, "image/jpeg"},
		{"video/mp4", "a.mp4", PurposeDownload, false, "video/mp4"},
		{"text/html", "a.html", PurposeDownload, false, OctetStream},
		{"", "unknown.bin", PurposeDownload, false, OctetStream},
	}
	for _, c := range cases {
		d := Policy(c.mime, c.name, c.purpose)
		if d.Inline != c.inline || d.ContentType != c.contentType {
			t.Errorf("Policy(%q,%q,%s) = inline=%v type=%q, want inline=%v type=%q", c.mime, c.name, c.purpose, d.Inline, d.ContentType, c.inline, c.contentType)
		}
		if d.Dangerous && (d.Inline || d.ContentType != OctetStream) {
			t.Errorf("dangerous %q served inline/typed", c.name)
		}
	}
}

func TestContentDisposition(t *testing.T) {
	v := ContentDisposition(false, `Laporan "Q3" – résumé.pdf`)
	if !strings.HasPrefix(v, "attachment; ") {
		t.Fatalf("kind: %s", v)
	}
	kind, params, err := mime.ParseMediaType(v)
	if err != nil {
		t.Fatalf("parse %q: %v", v, err)
	}
	if kind != "attachment" {
		t.Fatalf("kind %q", kind)
	}
	// mime.ParseMediaType prefers filename* (decoded into "filename"); check both segments raw.
	if params["filename"] != `Laporan "Q3" – résumé.pdf` {
		t.Fatalf("decoded filename* %q", params["filename"])
	}
	if !strings.Contains(v, `filename="Laporan _Q3_ _ r_sum_.pdf"; `) {
		t.Fatalf("ascii fallback missing in %s", v)
	}
	i := strings.Index(v, "filename*=UTF-8''")
	if i < 0 {
		t.Fatalf("no filename*: %s", v)
	}
	raw := v[i+len("filename*=UTF-8''"):]
	dec, err := url.PathUnescape(raw)
	if err != nil || dec != `Laporan "Q3" – résumé.pdf` {
		t.Fatalf("filename* round-trip: %q (%v)", dec, err)
	}
	if strings.ContainsAny(raw, `" ;`) {
		t.Fatalf("unescaped chars in %q", raw)
	}
	if ContentDisposition(true, "a.jpg") != `inline; filename="a.jpg"; filename*=UTF-8''a.jpg` {
		t.Fatalf("inline: %s", ContentDisposition(true, "a.jpg"))
	}
	if ContentDisposition(false, "") != "attachment" {
		t.Fatal("empty name")
	}
}
