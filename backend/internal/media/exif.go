package media

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strings"
	"unicode"
)

// EXIF subset kept in media_metadata.exif_json (SPEC §4.2) and shown by the
// viewer info panel (SPEC §8.14). The parser is deliberately small: it reads
// IFD0, the Exif sub-IFD and the GPS IFD of a TIFF structure with strict
// bounds checks and never follows the IFD1/thumbnail chain. The TIFF blob is
// located by walking the container (JPEG APP1, PNG eXIf, WebP EXIF, TIFF,
// HEIF/AVIF iloc) instead of scanning the whole file, so a 24 MP photo costs
// a few KB of reads. ADR-10 explains why no third-party EXIF library is used.

// ErrNoExif reports a file without an EXIF segment.
var ErrNoExif = errors.New("media: no exif")

// MaxExifBytes bounds the TIFF blob handed to ParseTIFF (JPEG APP1 is ≤ 64 KB;
// PNG/WebP/HEIF payloads may be larger when they embed a thumbnail).
const MaxExifBytes = 1 << 20

// maxContainerScan bounds how far into a container the EXIF search goes.
const maxContainerScan = 8 << 20

// GPS is the position stored with the photo; sent only on request.
type GPS struct {
	Lat float64  `json:"lat"`
	Lon float64  `json:"lon"`
	Alt *float64 `json:"alt,omitempty"`
}

// Exif is the parsed subset (strings trimmed, numbers normalised).
type Exif struct {
	Make             string  `json:"make,omitempty"`
	Model            string  `json:"model,omitempty"`
	Lens             string  `json:"lens,omitempty"`
	Software         string  `json:"software,omitempty"`
	DateTimeOriginal string  `json:"dateTimeOriginal,omitempty"` // 2006-01-02T15:04:05 (no zone)
	DateTime         string  `json:"dateTime,omitempty"`
	ExposureTime     string  `json:"exposureTime,omitempty"` // "1/250" or "2"
	FNumber          float64 `json:"fNumber,omitempty"`
	ISO              int     `json:"iso,omitempty"`
	FocalLength      float64 `json:"focalLength,omitempty"`
	FocalLength35    int     `json:"focalLength35,omitempty"`
	Orientation      int     `json:"orientation,omitempty"` // 1..8
	Flash            *bool   `json:"flash,omitempty"`
	Width            int     `json:"width,omitempty"` // PixelXDimension
	Height           int     `json:"height,omitempty"`
	GPS              *GPS    `json:"gps,omitempty"`
}

// Empty reports whether nothing useful was found.
func (e *Exif) Empty() bool {
	return e == nil || *e == Exif{}
}

// Public returns a copy without the GPS block (default API answer).
func (e *Exif) Public() *Exif {
	if e == nil {
		return nil
	}
	c := *e
	c.GPS = nil
	return &c
}

// HasGPS reports whether a position is stored.
func (e *Exif) HasGPS() bool { return e != nil && e.GPS != nil }

// ReadExif locates and parses the EXIF block of an image. It returns
// ErrNoExif when the container has none; other errors mean a damaged block.
func ReadExif(r io.ReadSeeker) (*Exif, error) {
	raw, err := ExtractExif(r)
	if err != nil {
		return nil, err
	}
	return ParseTIFF(raw)
}

// ExtractExif returns the raw TIFF structure (starting at the byte-order
// mark) embedded in a JPEG, PNG, WebP, TIFF, HEIF or AVIF file.
func ExtractExif(r io.ReadSeeker) ([]byte, error) {
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	var head [12]byte
	n, err := io.ReadFull(r, head[:])
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if n < 4 {
		return nil, ErrNoExif
	}
	switch {
	case head[0] == 0xFF && head[1] == 0xD8:
		return exifFromJPEG(r)
	case bytes.Equal(head[:8], []byte("\x89PNG\r\n\x1a\n")):
		return exifFromPNG(r)
	case bytes.Equal(head[:4], []byte("RIFF")) && n >= 12 && bytes.Equal(head[8:12], []byte("WEBP")):
		return exifFromWebP(r)
	case bytes.Equal(head[:4], []byte("II*\x00")) || bytes.Equal(head[:4], []byte("MM\x00*")):
		return readLimited(r, 0, MaxExifBytes)
	case n >= 12 && bytes.Equal(head[4:8], []byte("ftyp")):
		return exifFromISOBMFF(r)
	}
	return nil, ErrNoExif
}

// readLimited reads up to limit bytes starting at off.
func readLimited(r io.ReadSeeker, off int64, limit int) ([]byte, error) {
	if _, err := r.Seek(off, io.SeekStart); err != nil {
		return nil, err
	}
	buf, err := io.ReadAll(io.LimitReader(r, int64(limit)))
	if err != nil {
		return nil, err
	}
	return buf, nil
}

var exifPrefix = []byte("Exif\x00\x00")

func stripExifPrefix(b []byte) []byte {
	if bytes.HasPrefix(b, exifPrefix) {
		return b[len(exifPrefix):]
	}
	return b
}

// exifFromJPEG walks the marker segments up to SOS (the APP1 EXIF segment
// precedes the image data in every valid JPEG).
func exifFromJPEG(r io.ReadSeeker) ([]byte, error) {
	pos := int64(2)
	var hdr [4]byte
	for seg := 0; seg < 64 && pos < maxContainerScan; seg++ {
		if _, err := r.Seek(pos, io.SeekStart); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(r, hdr[:2]); err != nil {
			return nil, ErrNoExif
		}
		if hdr[0] != 0xFF {
			return nil, ErrNoExif
		}
		marker := hdr[1]
		if marker == 0xFF { // fill byte
			pos++
			continue
		}
		switch {
		case marker == 0xD8, marker == 0x01, marker >= 0xD0 && marker <= 0xD7:
			pos += 2 // standalone marker
			continue
		case marker == 0xDA, marker == 0xD9:
			return nil, ErrNoExif // image data starts: no APP1 seen
		}
		if _, err := io.ReadFull(r, hdr[2:4]); err != nil {
			return nil, ErrNoExif
		}
		length := int(binary.BigEndian.Uint16(hdr[2:4]))
		if length < 2 {
			return nil, ErrNoExif
		}
		if marker == 0xE1 && length > 2+len(exifPrefix) {
			payload, err := readLimited(r, pos+4, min(length-2, MaxExifBytes))
			if err != nil {
				return nil, err
			}
			if bytes.HasPrefix(payload, exifPrefix) {
				return payload[len(exifPrefix):], nil
			}
		}
		pos += 2 + int64(length)
	}
	return nil, ErrNoExif
}

// exifFromPNG walks the chunk list looking for eXIf (allowed anywhere before IEND).
func exifFromPNG(r io.ReadSeeker) ([]byte, error) {
	pos := int64(8)
	var hdr [8]byte
	for chunk := 0; chunk < 256 && pos < maxContainerScan; chunk++ {
		if _, err := r.Seek(pos, io.SeekStart); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return nil, ErrNoExif
		}
		length := int64(binary.BigEndian.Uint32(hdr[:4]))
		typ := string(hdr[4:8])
		if typ == "IEND" {
			return nil, ErrNoExif
		}
		if typ == "eXIf" {
			if length == 0 || length > MaxExifBytes {
				return nil, ErrNoExif
			}
			data, err := readLimited(r, pos+8, int(length))
			if err != nil {
				return nil, err
			}
			return stripExifPrefix(data), nil
		}
		pos += 8 + length + 4
	}
	return nil, ErrNoExif
}

// exifFromWebP walks the RIFF chunks for EXIF (extended WebP, VP8X).
func exifFromWebP(r io.ReadSeeker) ([]byte, error) {
	pos := int64(12)
	var hdr [8]byte
	for chunk := 0; chunk < 256 && pos < maxContainerScan; chunk++ {
		if _, err := r.Seek(pos, io.SeekStart); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return nil, ErrNoExif
		}
		length := int64(binary.LittleEndian.Uint32(hdr[4:8]))
		if string(hdr[:4]) == "EXIF" {
			if length == 0 || length > MaxExifBytes {
				return nil, ErrNoExif
			}
			data, err := readLimited(r, pos+8, int(length))
			if err != nil {
				return nil, err
			}
			return stripExifPrefix(data), nil
		}
		pos += 8 + length + length%2
	}
	return nil, ErrNoExif
}

// exifFromISOBMFF handles HEIF/HEIC/AVIF: the meta box lists items (iinf),
// the Exif item's extent is found in iloc and its payload starts with a
// 4-byte offset to the TIFF header (ISO 23008-12 §A.2.1).
func exifFromISOBMFF(r io.ReadSeeker) ([]byte, error) {
	// top level: find meta
	pos := int64(0)
	var hdr [8]byte
	for box := 0; box < 64 && pos < maxContainerScan; box++ {
		if _, err := r.Seek(pos, io.SeekStart); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return nil, ErrNoExif
		}
		size := int64(binary.BigEndian.Uint32(hdr[:4]))
		typ := string(hdr[4:8])
		hlen := int64(8)
		switch size {
		case 1: // 64-bit size
			var big [8]byte
			if _, err := io.ReadFull(r, big[:]); err != nil {
				return nil, ErrNoExif
			}
			size = int64(binary.BigEndian.Uint64(big[:])) //nolint:gosec // negative → rejected below
			hlen = 16
		case 0:
			return nil, ErrNoExif
		}
		if size < hlen {
			return nil, ErrNoExif
		}
		if typ == "meta" {
			if size-hlen > 4<<20 {
				return nil, ErrNoExif
			}
			meta, err := readLimited(r, pos+hlen, int(size-hlen))
			if err != nil {
				return nil, err
			}
			return exifFromMeta(r, meta)
		}
		pos += size
	}
	return nil, ErrNoExif
}

// isoBox is one child box of a full box payload.
type isoBox struct {
	typ  string
	data []byte
}

func isoChildren(b []byte) []isoBox {
	var out []isoBox
	for len(b) >= 8 && len(out) < 256 {
		size := int(binary.BigEndian.Uint32(b[:4]))
		typ := string(b[4:8])
		if size == 0 {
			size = len(b)
		}
		if size < 8 || size > len(b) {
			break
		}
		out = append(out, isoBox{typ: typ, data: b[8:size]})
		b = b[size:]
	}
	return out
}

func exifFromMeta(r io.ReadSeeker, meta []byte) ([]byte, error) {
	if len(meta) < 4 {
		return nil, ErrNoExif
	}
	exifID := uint32(0)
	var iloc []byte
	for _, box := range isoChildren(meta[4:]) { // skip version/flags of the full box
		switch box.typ {
		case "iinf":
			exifID = isoExifItemID(box.data)
		case "iloc":
			iloc = box.data
		}
	}
	if exifID == 0 || iloc == nil {
		return nil, ErrNoExif
	}
	off, length, ok := isoLocate(iloc, exifID)
	if !ok || length < 4 || length > MaxExifBytes+16 || off > maxContainerScan*8 {
		return nil, ErrNoExif
	}
	data, err := readLimited(r, int64(off), int(length)) //nolint:gosec // both bounded just above
	if err != nil {
		return nil, err
	}
	if len(data) < 4 {
		return nil, ErrNoExif
	}
	skip := 4 + int(binary.BigEndian.Uint32(data[:4]))
	if skip < 4 || skip > len(data) {
		return nil, ErrNoExif
	}
	return stripExifPrefix(data[skip:]), nil
}

// isoExifItemID scans iinf for the infe entry whose item_type is "Exif".
func isoExifItemID(b []byte) uint32 {
	if len(b) < 4 {
		return 0
	}
	version := b[0]
	p := 4
	if version == 0 {
		p += 2
	} else {
		p += 4
	}
	if p > len(b) {
		return 0
	}
	for _, box := range isoChildren(b[p:]) {
		if box.typ != "infe" || len(box.data) < 4 {
			continue
		}
		v := box.data[0]
		d := box.data[4:]
		var id uint32
		switch v {
		case 2:
			if len(d) < 8 {
				continue
			}
			id = uint32(binary.BigEndian.Uint16(d[:2]))
			d = d[4:]
		case 3:
			if len(d) < 10 {
				continue
			}
			id = binary.BigEndian.Uint32(d[:4])
			d = d[6:]
		default:
			continue
		}
		if len(d) >= 4 && string(d[:4]) == "Exif" {
			return id
		}
	}
	return 0
}

// isoLocate returns the first extent (offset, length) of item id from iloc.
func isoLocate(b []byte, id uint32) (uint64, uint64, bool) {
	if len(b) < 8 {
		return 0, 0, false
	}
	version := b[0]
	offSize := int(b[4] >> 4)
	lenSize := int(b[4] & 0xF)
	baseSize := int(b[5] >> 4)
	idxSize := 0
	if version == 1 || version == 2 {
		idxSize = int(b[5] & 0xF)
	}
	p := 6
	var count uint32
	if version < 2 {
		count = uint32(binary.BigEndian.Uint16(b[p:]))
		p += 2
	} else {
		if len(b) < 10 {
			return 0, 0, false
		}
		count = binary.BigEndian.Uint32(b[p:])
		p += 4
	}
	readN := func(n int) (uint64, bool) {
		if n == 0 {
			return 0, true
		}
		if n != 4 && n != 8 || p+n > len(b) {
			return 0, false
		}
		var v uint64
		if n == 4 {
			v = uint64(binary.BigEndian.Uint32(b[p:]))
		} else {
			v = binary.BigEndian.Uint64(b[p:])
		}
		p += n
		return v, true
	}
	for i := uint32(0); i < count && i < 4096; i++ {
		var item uint32
		if version < 2 {
			if p+2 > len(b) {
				return 0, 0, false
			}
			item = uint32(binary.BigEndian.Uint16(b[p:]))
			p += 2
		} else {
			if p+4 > len(b) {
				return 0, 0, false
			}
			item = binary.BigEndian.Uint32(b[p:])
			p += 4
		}
		method := 0
		if version == 1 || version == 2 {
			if p+2 > len(b) {
				return 0, 0, false
			}
			method = int(b[p+1] & 0xF)
			p += 2
		}
		if p+2 > len(b) {
			return 0, 0, false
		}
		p += 2 // data_reference_index
		base, ok := readN(baseSize)
		if !ok {
			return 0, 0, false
		}
		if p+2 > len(b) {
			return 0, 0, false
		}
		extents := int(binary.BigEndian.Uint16(b[p:]))
		p += 2
		for e := 0; e < extents; e++ {
			if _, ok := readN(idxSize); !ok {
				return 0, 0, false
			}
			off, ok := readN(offSize)
			if !ok {
				return 0, 0, false
			}
			ln, ok := readN(lenSize)
			if !ok {
				return 0, 0, false
			}
			if item == id && e == 0 {
				if method != 0 { // idat/item construction: not supported
					return 0, 0, false
				}
				return base + off, ln, true
			}
		}
	}
	return 0, 0, false
}

// --- TIFF / IFD parsing ---

// tag numbers (CIPA DC-008)
const (
	tagMake             = 0x010F
	tagModel            = 0x0110
	tagOrientation      = 0x0112
	tagSoftware         = 0x0131
	tagDateTime         = 0x0132
	tagExifIFD          = 0x8769
	tagGPSIFD           = 0x8825
	tagExposureTime     = 0x829A
	tagFNumber          = 0x829D
	tagISO              = 0x8827
	tagDateTimeOriginal = 0x9003
	tagFlash            = 0x9209
	tagFocalLength      = 0x920A
	tagPixelX           = 0xA002
	tagPixelY           = 0xA003
	tagFocal35          = 0xA405
	tagLensModel        = 0xA434
	tagGPSLatRef        = 0x0001
	tagGPSLat           = 0x0002
	tagGPSLonRef        = 0x0003
	tagGPSLon           = 0x0004
	tagGPSAltRef        = 0x0005
	tagGPSAlt           = 0x0006
)

var typeSizes = [...]int{0, 1, 1, 2, 4, 8, 1, 1, 2, 4, 8, 4, 8}

const (
	typeByte      = 1
	typeASCII     = 2
	typeShort     = 3
	typeLong      = 4
	typeRational  = 5
	typeSLong     = 9
	typeSRational = 10
)

type ifdEntry struct {
	tag   uint16
	typ   uint16
	count uint32
	data  []byte // raw value bytes (inline or dereferenced)
}

type tiffReader struct {
	b  []byte
	bo binary.ByteOrder
}

// ParseTIFF parses the EXIF subset from a TIFF structure (byte-order mark first).
func ParseTIFF(b []byte) (*Exif, error) {
	if len(b) < 8 {
		return nil, errors.New("media: exif too short")
	}
	var bo binary.ByteOrder
	switch string(b[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return nil, errors.New("media: exif byte order")
	}
	if bo.Uint16(b[2:4]) != 42 {
		return nil, errors.New("media: exif magic")
	}
	t := &tiffReader{b: b, bo: bo}
	ifd0 := t.bo.Uint32(b[4:8])
	entries, err := t.readIFD(ifd0)
	if err != nil {
		return nil, err
	}
	ex := &Exif{}
	var exifOff, gpsOff uint32
	for _, e := range entries {
		switch e.tag {
		case tagMake:
			ex.Make = t.ascii(e)
		case tagModel:
			ex.Model = t.ascii(e)
		case tagSoftware:
			ex.Software = t.ascii(e)
		case tagDateTime:
			ex.DateTime = exifDate(t.ascii(e))
		case tagOrientation:
			if v, ok := t.uint(e); ok && v >= 1 && v <= 8 {
				ex.Orientation = int(v)
			}
		case tagExifIFD:
			exifOff, _ = t.uint(e)
		case tagGPSIFD:
			gpsOff, _ = t.uint(e)
		}
	}
	if exifOff != 0 {
		if sub, err := t.readIFD(exifOff); err == nil {
			for _, e := range sub {
				switch e.tag {
				case tagExposureTime:
					ex.ExposureTime = exposure(t.rational(e))
				case tagFNumber:
					ex.FNumber = round1(t.rational(e))
				case tagISO:
					if v, ok := t.uint(e); ok && v > 0 && v < 10_000_000 {
						ex.ISO = int(v)
					}
				case tagDateTimeOriginal:
					ex.DateTimeOriginal = exifDate(t.ascii(e))
				case tagFlash:
					if v, ok := t.uint(e); ok {
						fired := v&1 == 1
						ex.Flash = &fired
					}
				case tagFocalLength:
					ex.FocalLength = round1(t.rational(e))
				case tagFocal35:
					if v, ok := t.uint(e); ok && v > 0 && v < 100_000 {
						ex.FocalLength35 = int(v)
					}
				case tagPixelX:
					if v, ok := t.uint(e); ok && v > 0 && v < 1<<20 {
						ex.Width = int(v)
					}
				case tagPixelY:
					if v, ok := t.uint(e); ok && v > 0 && v < 1<<20 {
						ex.Height = int(v)
					}
				case tagLensModel:
					ex.Lens = t.ascii(e)
				}
			}
		}
	}
	if gpsOff != 0 {
		if sub, err := t.readIFD(gpsOff); err == nil {
			ex.GPS = t.gps(sub)
		}
	}
	return ex, nil
}

// readIFD returns the entries of the IFD at off with their value bytes
// resolved (inline or by offset), bounds-checked against the blob.
func (t *tiffReader) readIFD(off uint32) ([]ifdEntry, error) {
	b := t.b
	if off < 8 || int64(off)+2 > int64(len(b)) {
		return nil, errors.New("media: exif ifd offset")
	}
	n := int(t.bo.Uint16(b[off:]))
	if n == 0 || n > 512 {
		return nil, errors.New("media: exif ifd size")
	}
	start := int(off) + 2
	if start+n*12 > len(b) {
		return nil, errors.New("media: exif ifd truncated")
	}
	out := make([]ifdEntry, 0, n)
	for i := 0; i < n; i++ {
		p := start + i*12
		e := ifdEntry{tag: t.bo.Uint16(b[p:]), typ: t.bo.Uint16(b[p+2:]), count: t.bo.Uint32(b[p+4:])}
		if e.typ == 0 || int(e.typ) >= len(typeSizes) {
			continue
		}
		size := uint64(typeSizes[e.typ]) * uint64(e.count) //nolint:gosec // typeSizes entries are 1..8
		if size > 64<<10 {
			continue
		}
		if size <= 4 {
			e.data = b[p+8 : p+8+int(size)]
		} else {
			vo := uint64(t.bo.Uint32(b[p+8:]))
			if vo+size > uint64(len(b)) {
				continue
			}
			e.data = b[vo : vo+size]
		}
		out = append(out, e)
	}
	return out, nil
}

func (t *tiffReader) ascii(e ifdEntry) string {
	if e.typ != typeASCII && e.typ != typeByte {
		return ""
	}
	s := string(e.data)
	if i := strings.IndexByte(s, 0); i >= 0 {
		s = s[:i]
	}
	s = strings.ToValidUTF8(strings.TrimSpace(s), "")
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if len(s) > 256 {
		s = s[:256]
	}
	return s
}

// uint reads the first value of a SHORT/LONG/BYTE entry.
func (t *tiffReader) uint(e ifdEntry) (uint32, bool) {
	switch e.typ {
	case typeByte:
		if len(e.data) >= 1 {
			return uint32(e.data[0]), true
		}
	case typeShort:
		if len(e.data) >= 2 {
			return uint32(t.bo.Uint16(e.data)), true
		}
	case typeLong:
		if len(e.data) >= 4 {
			return t.bo.Uint32(e.data), true
		}
	}
	return 0, false
}

// rational reads the first RATIONAL/SRATIONAL value as a float (0 when absent).
func (t *tiffReader) rational(e ifdEntry) float64 {
	return t.rationalAt(e, 0)
}

func (t *tiffReader) rationalAt(e ifdEntry, i int) float64 {
	if (e.typ != typeRational && e.typ != typeSRational) || len(e.data) < (i+1)*8 {
		return 0
	}
	d := e.data[i*8:]
	var num, den float64
	if e.typ == typeRational {
		num, den = float64(t.bo.Uint32(d)), float64(t.bo.Uint32(d[4:]))
	} else {
		num, den = float64(int32(t.bo.Uint32(d))), float64(int32(t.bo.Uint32(d[4:]))) //nolint:gosec // SRATIONAL is two's complement by definition
	}
	if den == 0 {
		return 0
	}
	v := num / den
	if math.IsInf(v, 0) || math.IsNaN(v) {
		return 0
	}
	return v
}

func (t *tiffReader) gps(entries []ifdEntry) *GPS {
	var lat, lon, alt float64
	var latRef, lonRef string
	altRef := uint32(0)
	var haveLat, haveLon, haveAlt bool
	for _, e := range entries {
		switch e.tag {
		case tagGPSLatRef:
			latRef = t.ascii(e)
		case tagGPSLonRef:
			lonRef = t.ascii(e)
		case tagGPSLat:
			lat, haveLat = t.dms(e)
		case tagGPSLon:
			lon, haveLon = t.dms(e)
		case tagGPSAltRef:
			altRef, _ = t.uint(e)
		case tagGPSAlt:
			if e.count >= 1 {
				alt, haveAlt = t.rational(e), true
			}
		}
	}
	if !haveLat || !haveLon {
		return nil
	}
	if strings.EqualFold(latRef, "S") {
		lat = -lat
	}
	if strings.EqualFold(lonRef, "W") {
		lon = -lon
	}
	if lat < -90 || lat > 90 || lon < -180 || lon > 180 || (lat == 0 && lon == 0) {
		return nil
	}
	g := &GPS{Lat: round6(lat), Lon: round6(lon)}
	if haveAlt {
		if altRef == 1 {
			alt = -alt
		}
		a := round1(alt)
		g.Alt = &a
	}
	return g
}

// dms converts degrees/minutes/seconds rationals to decimal degrees.
func (t *tiffReader) dms(e ifdEntry) (float64, bool) {
	if e.count < 3 || (e.typ != typeRational && e.typ != typeSRational) {
		return 0, false
	}
	d, m, s := t.rationalAt(e, 0), t.rationalAt(e, 1), t.rationalAt(e, 2)
	if d < 0 || m < 0 || s < 0 {
		return 0, false
	}
	return d + m/60 + s/3600, true
}

// exifDate converts "2006:01:02 15:04:05" to "2006-01-02T15:04:05"; other
// shapes are returned trimmed (some cameras write partial dates).
func exifDate(s string) string {
	if len(s) == 19 && s[4] == ':' && s[7] == ':' && s[10] == ' ' && s[13] == ':' && s[16] == ':' {
		for i, c := range s {
			if i == 4 || i == 7 || i == 10 || i == 13 || i == 16 {
				continue
			}
			if c < '0' || c > '9' {
				return s
			}
		}
		if strings.HasPrefix(s, "0000") {
			return ""
		}
		return s[:4] + "-" + s[5:7] + "-" + s[8:10] + "T" + s[11:]
	}
	return s
}

// exposure formats seconds as "1/250" (below one second) or "2" / "0.5"-style otherwise.
func exposure(sec float64) string {
	switch {
	case sec <= 0:
		return ""
	case sec >= 1:
		return fmt.Sprintf("%g", round1(sec))
	default:
		return fmt.Sprintf("1/%d", int(math.Round(1/sec)))
	}
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }
func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

// ExifOf decodes exif_json of a cached row (nil when absent or unreadable).
func ExifOf(exifJSON *string) *Exif {
	if exifJSON == nil || *exifJSON == "" {
		return nil
	}
	var e Exif
	if json.Unmarshal([]byte(*exifJSON), &e) != nil {
		return nil
	}
	return &e
}
