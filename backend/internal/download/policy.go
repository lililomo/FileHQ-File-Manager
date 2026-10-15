package download

import (
	"fmt"
	"mime"
	"path"
	"strings"
	"unicode"
)

// OctetStream is the Content-Type every attachment of a dangerous type gets
// (ADR-06): the browser never gets to interpret user-supplied HTML/SVG/XML/JS
// from the application origin.
const OctetStream = "application/octet-stream"

// Disposition is the verdict of the inline/attachment policy (SPEC §8.9).
type Disposition struct {
	Inline      bool   // Content-Disposition: inline (only raster image, audio, video)
	ContentType string // what the response carries; OctetStream for dangerous types
	Dangerous   bool   // the detected or declared type is active content
}

// dangerousMIME lists sniffed types that must never be served with their own
// type. Anything the browser could execute or that can carry scripts.
var dangerousMIME = map[string]bool{
	"text/html": true, "application/xhtml+xml": true,
	"image/svg+xml": true,
	"text/xml":      true, "application/xml": true,
	"text/javascript": true, "application/javascript": true, "application/x-javascript": true, "application/ecmascript": true,
	"text/css":                      true,
	"application/x-shockwave-flash": true,
	"application/vnd.ms-htmlhelp":   true, "text/vbscript": true,
	"application/mathml+xml": true, "application/rss+xml": true, "application/atom+xml": true, "application/xslt+xml": true,
	"text/cache-manifest": true, "application/manifest+json": true,
	// web archives / mail bodies embed HTML parts a browser may render
	"multipart/related": true, "message/rfc822": true, "application/x-mimearchive": true,
}

// dangerousExt is the extension side of the same rule: a .html file whose
// first bytes look like plain text is still HTML to the browser once sniffed
// (nosniff stops that, but belt and braces).
var dangerousExt = map[string]bool{
	"html": true, "htm": true, "xhtml": true, "xht": true, "shtml": true,
	"svg": true, "svgz": true,
	"xml": true, "xsl": true, "xslt": true, "rss": true, "atom": true, "mathml": true,
	"js": true, "mjs": true, "cjs": true, "jsx": true, "ts": true, "vbs": true, "hta": true,
	"css": true, "swf": true, "chm": true, "appcache": true, "webmanifest": true, "url": true,
	"mhtml": true, "mht": true, "eml": true,
}

// inlineRaster are the raster image types a browser renders without
// executing anything (SPEC §8.9 "Raster image").
var inlineRaster = map[string]bool{
	"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true,
	"image/avif": true, "image/bmp": true, "image/x-icon": true, "image/vnd.microsoft.icon": true,
	"image/heic": true, "image/heif": true, "image/tiff": true, "image/apng": true,
}

// Policy decides how a file is served: detectedMIME comes from magic bytes
// (media.DetectMime), name supplies the extension. purpose download forces
// an attachment; media/inline allow raster, audio and video inline. PDF and
// text stay attachments (viewers fetch them, SPEC §8.9 table).
func Policy(detectedMIME, name string, purpose Purpose) Disposition {
	mt := strings.ToLower(strings.TrimSpace(detectedMIME))
	if i := strings.IndexByte(mt, ';'); i >= 0 {
		mt = strings.TrimSpace(mt[:i])
	}
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(name), "."))
	if dangerousMIME[mt] || dangerousExt[ext] || strings.HasSuffix(mt, "+xml") || strings.HasSuffix(mt, "/xml") {
		return Disposition{Inline: false, ContentType: OctetStream, Dangerous: true}
	}
	if mt == "" {
		mt = OctetStream
	}
	d := Disposition{Inline: false, ContentType: mt}
	if purpose != PurposeMedia && purpose != PurposeInline {
		return d
	}
	switch {
	case inlineRaster[mt]:
		d.Inline = true
	case strings.HasPrefix(mt, "video/"), strings.HasPrefix(mt, "audio/"):
		d.Inline = true
	}
	return d
}

// ContentDisposition renders `inline`/`attachment` with an ASCII fallback
// and an RFC 5987 UTF-8 name (SPEC §8.9 "Nama file UTF-8").
func ContentDisposition(inline bool, filename string) string {
	kind := "attachment"
	if inline {
		kind = "inline"
	}
	if filename == "" {
		return kind
	}
	ascii := asciiFallback(filename)
	// url.PathEscape keeps too much (e.g. "+"), so escape per RFC 5987 ourselves.
	return fmt.Sprintf(`%s; filename="%s"; filename*=UTF-8''%s`, kind, ascii, rfc5987(filename))
}

func asciiFallback(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('_')
		case r < 0x20 || r == 0x7f:
			b.WriteByte('_')
		case r > unicode.MaxASCII:
			b.WriteByte('_')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func rfc5987(name string) string {
	const attrChar = "!#$&+-.^_`|~"
	var b strings.Builder
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
			b.WriteByte(c)
		case strings.IndexByte(attrChar, c) >= 0:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// ParseContentDisposition parses a Content-Disposition value (mime.ParseMediaType).
func ParseContentDisposition(v string) (string, map[string]string, error) {
	return mime.ParseMediaType(v)
}
