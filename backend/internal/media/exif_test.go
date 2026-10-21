package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"math/rand"
	"strings"
	"testing"
)

// --- fixture builders (nothing binary is committed) ---

type tagVal struct {
	tag, typ uint16
	count    uint32
	data     []byte
}

func asciiTag(tag uint16, s string) tagVal {
	return tagVal{tag: tag, typ: typeASCII, count: uint32(len(s) + 1), data: append([]byte(s), 0)}
}

func shortTag(bo binary.ByteOrder, tag uint16, v uint16) tagVal {
	b := make([]byte, 2)
	bo.PutUint16(b, v)
	return tagVal{tag: tag, typ: typeShort, count: 1, data: b}
}

func longTag(bo binary.ByteOrder, tag uint16, v uint32) tagVal {
	b := make([]byte, 4)
	bo.PutUint32(b, v)
	return tagVal{tag: tag, typ: typeLong, count: 1, data: b}
}

func byteTag(tag uint16, v byte) tagVal {
	return tagVal{tag: tag, typ: typeByte, count: 1, data: []byte{v}}
}

func rationalTag(bo binary.ByteOrder, tag uint16, pairs ...[2]uint32) tagVal {
	b := make([]byte, 8*len(pairs))
	for i, p := range pairs {
		bo.PutUint32(b[i*8:], p[0])
		bo.PutUint32(b[i*8+4:], p[1])
	}
	return tagVal{tag: tag, typ: typeRational, count: uint32(len(pairs)), data: b}
}

// buildTIFF lays out IFD0 (+ pointer entries), the Exif IFD and the GPS IFD
// back to back, with out-of-line values after the last IFD.
func buildTIFF(bo binary.ByteOrder, ifd0, exif, gps []tagVal) []byte {
	ifdSize := func(n int) int { return 2 + 12*n + 4 }
	n0 := len(ifd0)
	if exif != nil {
		n0++
	}
	if gps != nil {
		n0++
	}
	ifd0Off := 8
	exifOff := ifd0Off + ifdSize(n0)
	gpsOff := exifOff
	if exif != nil {
		gpsOff += ifdSize(len(exif))
	}
	valuesOff := gpsOff
	if gps != nil {
		valuesOff += ifdSize(len(gps))
	}
	buf := make([]byte, valuesOff)
	var values []byte
	if bo == binary.LittleEndian {
		copy(buf, "II")
	} else {
		copy(buf, "MM")
	}
	bo.PutUint16(buf[2:], 42)
	bo.PutUint32(buf[4:], uint32(ifd0Off))
	writeIFD := func(off int, entries []tagVal) {
		bo.PutUint16(buf[off:], uint16(len(entries)))
		for i, e := range entries {
			p := off + 2 + i*12
			bo.PutUint16(buf[p:], e.tag)
			bo.PutUint16(buf[p+2:], e.typ)
			bo.PutUint32(buf[p+4:], e.count)
			if len(e.data) <= 4 {
				copy(buf[p+8:], e.data)
			} else {
				bo.PutUint32(buf[p+8:], uint32(valuesOff+len(values)))
				values = append(values, e.data...)
			}
		}
		bo.PutUint32(buf[off+2+12*len(entries):], 0) // next IFD
	}
	all := append([]tagVal{}, ifd0...)
	if exif != nil {
		all = append(all, longTag(bo, tagExifIFD, uint32(exifOff)))
	}
	if gps != nil {
		all = append(all, longTag(bo, tagGPSIFD, uint32(gpsOff)))
	}
	writeIFD(ifd0Off, all)
	if exif != nil {
		writeIFD(exifOff, exif)
	}
	if gps != nil {
		writeIFD(gpsOff, gps)
	}
	return append(buf, values...)
}

// sampleTIFF is a camera-like block with GPS in the southern/western hemisphere.
func sampleTIFF(bo binary.ByteOrder) []byte {
	ifd0 := []tagVal{
		asciiTag(tagMake, "Canon"),
		asciiTag(tagModel, "EOS R6  "),
		shortTag(bo, tagOrientation, 6),
		asciiTag(tagSoftware, "Lightroom"),
		asciiTag(tagDateTime, "2024:05:01 10:20:30"),
	}
	exif := []tagVal{
		rationalTag(bo, tagExposureTime, [2]uint32{1, 250}),
		rationalTag(bo, tagFNumber, [2]uint32{28, 10}),
		shortTag(bo, tagISO, 400),
		asciiTag(tagDateTimeOriginal, "2024:04:30 18:05:00"),
		shortTag(bo, tagFlash, 0x19), // fired, auto
		rationalTag(bo, tagFocalLength, [2]uint32{500, 10}),
		longTag(bo, tagPixelX, 6000),
		longTag(bo, tagPixelY, 4000),
		shortTag(bo, tagFocal35, 75),
		asciiTag(tagLensModel, "RF50mm F1.8 STM"),
	}
	gps := []tagVal{
		asciiTag(tagGPSLatRef, "S"),
		rationalTag(bo, tagGPSLat, [2]uint32{8, 1}, [2]uint32{13, 1}, [2]uint32{1200, 100}),
		asciiTag(tagGPSLonRef, "E"),
		rationalTag(bo, tagGPSLon, [2]uint32{114, 1}, [2]uint32{22, 1}, [2]uint32{30, 1}),
		byteTag(tagGPSAltRef, 0),
		rationalTag(bo, tagGPSAlt, [2]uint32{125, 10}),
	}
	return buildTIFF(bo, ifd0, exif, gps)
}

func jpegWithAPP1(payload []byte) []byte {
	var b bytes.Buffer
	b.Write([]byte{0xFF, 0xD8})
	b.Write([]byte{0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 1, 0, 1, 0, 0})
	if payload != nil {
		b.Write([]byte{0xFF, 0xE1})
		_ = binary.Write(&b, binary.BigEndian, uint16(len(payload)+2))
		b.Write(payload)
	}
	b.Write([]byte{0xFF, 0xDB, 0x00, 0x04, 0, 0}) // DQT stub
	b.Write([]byte{0xFF, 0xDA, 0x00, 0x02})       // SOS
	b.Write([]byte{1, 2, 3, 0xFF, 0xD9})
	return b.Bytes()
}

func pngChunk(typ string, data []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, uint32(len(data)))
	b.WriteString(typ)
	b.Write(data)
	_ = binary.Write(&b, binary.BigEndian, crc32.ChecksumIEEE(append([]byte(typ), data...)))
	return b.Bytes()
}

// pngWithExif builds a PNG skeleton; tiff=nil leaves the eXIf chunk out.
func pngWithExif(tiff []byte) []byte {
	var b bytes.Buffer
	b.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr, 4)
	binary.BigEndian.PutUint32(ihdr[4:], 4)
	ihdr[8], ihdr[9] = 8, 2
	b.Write(pngChunk("IHDR", ihdr))
	b.Write(pngChunk("IDAT", []byte{0, 1, 2, 3}))
	if tiff != nil {
		b.Write(pngChunk("eXIf", tiff))
	}
	b.Write(pngChunk("IEND", nil))
	return b.Bytes()
}

func webpWithExif(tiff []byte) []byte {
	var body bytes.Buffer
	body.WriteString("WEBP")
	body.WriteString("VP8X")
	_ = binary.Write(&body, binary.LittleEndian, uint32(10))
	body.Write([]byte{0x08, 0, 0, 0, 3, 0, 0, 3, 0, 0})
	body.WriteString("EXIF")
	_ = binary.Write(&body, binary.LittleEndian, uint32(len(tiff)))
	body.Write(tiff)
	if len(tiff)%2 == 1 {
		body.WriteByte(0)
	}
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(body.Len()))
	b.Write(body.Bytes())
	return b.Bytes()
}

func isoBoxBytes(typ string, payload []byte) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.BigEndian, uint32(8+len(payload)))
	b.WriteString(typ)
	b.Write(payload)
	return b.Bytes()
}

// heifWithExif builds ftyp + meta(iinf, iloc) + mdat with the Exif item.
func heifWithExif(tiff []byte) []byte {
	ftyp := isoBoxBytes("ftyp", []byte("heic\x00\x00\x00\x00mif1heic"))
	infe := isoBoxBytes("infe", append([]byte{2, 0, 0, 0, 0, 1, 0, 0}, []byte("Exif\x00")...))
	iinf := isoBoxBytes("iinf", append([]byte{0, 0, 0, 0, 0, 1}, infe...))
	exifItem := append([]byte{0, 0, 0, 6}, append([]byte("Exif\x00\x00"), tiff...)...)
	// iloc v0: offset_size 4, length_size 4, base_offset_size 0, item_count 1,
	// item_ID 1, data_reference_index 0, extent_count 1, offset, length
	ilocPayload := []byte{0, 0, 0, 0, 0x44, 0x00, 0, 1, 0, 1, 0, 0, 0, 1}
	ilocPayload = append(ilocPayload, 0, 0, 0, 0) // offset (patched)
	ilocPayload = binary.BigEndian.AppendUint32(ilocPayload, uint32(len(exifItem)))
	iloc := isoBoxBytes("iloc", ilocPayload)
	metaPayload := append([]byte{0, 0, 0, 0}, append(iinf, iloc...)...)
	meta := isoBoxBytes("meta", metaPayload)
	mdatOff := len(ftyp) + len(meta) + 8
	// patch the extent offset: iloc payload offset field sits 14 bytes in
	ilocStart := len(ftyp) + 8 + 4 + len(iinf) + 8
	out := append(append([]byte{}, ftyp...), meta...)
	binary.BigEndian.PutUint32(out[ilocStart+14:], uint32(mdatOff))
	return append(out, isoBoxBytes("mdat", exifItem)...)
}

// --- tests ---

func checkSample(t *testing.T, ex *Exif) {
	t.Helper()
	if ex == nil {
		t.Fatal("nil exif")
	}
	if ex.Make != "Canon" || ex.Model != "EOS R6" || ex.Software != "Lightroom" || ex.Lens != "RF50mm F1.8 STM" {
		t.Fatalf("strings: %+v", ex)
	}
	if ex.Orientation != 6 || ex.ISO != 400 || ex.FocalLength35 != 75 || ex.Width != 6000 || ex.Height != 4000 {
		t.Fatalf("numbers: %+v", ex)
	}
	if ex.ExposureTime != "1/250" || ex.FNumber != 2.8 || ex.FocalLength != 50 {
		t.Fatalf("exposure/f/focal: %+v", ex)
	}
	if ex.DateTimeOriginal != "2024-04-30T18:05:00" || ex.DateTime != "2024-05-01T10:20:30" {
		t.Fatalf("dates: %+v", ex)
	}
	if ex.Flash == nil || !*ex.Flash {
		t.Fatalf("flash: %+v", ex.Flash)
	}
	if ex.GPS == nil || ex.GPS.Lat != -8.22 || ex.GPS.Lon != 114.375 || ex.GPS.Alt == nil || *ex.GPS.Alt != 12.5 {
		t.Fatalf("gps: %+v", ex.GPS)
	}
}

func TestParseTIFFBothByteOrders(t *testing.T) {
	for _, bo := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		ex, err := ParseTIFF(sampleTIFF(bo))
		if err != nil {
			t.Fatalf("%v: %v", bo, err)
		}
		checkSample(t, ex)
		pub := ex.Public()
		if pub.GPS != nil || !ex.HasGPS() || pub.Make != "Canon" {
			t.Fatalf("Public must strip GPS only: %+v", pub)
		}
	}
}

func TestExtractFromContainers(t *testing.T) {
	tiff := sampleTIFF(binary.LittleEndian)
	cases := map[string][]byte{
		"jpeg": jpegWithAPP1(append([]byte("Exif\x00\x00"), tiff...)),
		"png":  pngWithExif(tiff),
		"webp": webpWithExif(tiff),
		"tiff": tiff,
		"heif": heifWithExif(tiff),
	}
	for name, data := range cases {
		ex, err := ReadExif(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		checkSample(t, ex)
	}
}

func TestNoExif(t *testing.T) {
	cases := map[string][]byte{
		"jpeg-no-app1":  jpegWithAPP1(nil),
		"jpeg-xmp-app1": jpegWithAPP1([]byte("http://ns.adobe.com/xap/1.0/\x00<x/>")),
		"png-plain":     pngWithExif(nil),
		"text":          []byte("hello world, not an image at all"),
		"empty":         {},
		"heif-no-item":  isoBoxBytes("ftyp", []byte("heic\x00\x00\x00\x00")),
	}
	for name, data := range cases {
		if _, err := ReadExif(bytes.NewReader(data)); !errors.Is(err, ErrNoExif) {
			t.Fatalf("%s: want ErrNoExif, got %v", name, err)
		}
	}
}

func TestParseTIFFRejectsGarbage(t *testing.T) {
	for _, b := range [][]byte{nil, []byte("II*"), []byte("XX\x2a\x00\x08\x00\x00\x00"), []byte("II\x2a\x00\xff\xff\xff\xff")} {
		if _, err := ParseTIFF(b); err == nil {
			t.Fatalf("garbage %q accepted", b)
		}
	}
}

// TestParseTIFFMutationsNeverPanic corrupts the sample in many ways: the
// parser must return (possibly partial) data or an error, never panic.
func TestParseTIFFMutationsNeverPanic(t *testing.T) {
	base := sampleTIFF(binary.BigEndian)
	rng := rand.New(rand.NewSource(17))
	for i := 0; i < 3000; i++ {
		b := append([]byte{}, base...)
		switch i % 4 {
		case 0: // flip a byte
			b[rng.Intn(len(b))] = byte(rng.Intn(256))
		case 1: // truncate
			b = b[:rng.Intn(len(b))]
		case 2: // overwrite a 4-byte window (offsets/counts)
			p := rng.Intn(len(b) - 4)
			binary.BigEndian.PutUint32(b[p:], uint32(rng.Int63()))
		case 3: // several flips
			for k := 0; k < 5; k++ {
				b[rng.Intn(len(b))] ^= byte(1 << uint(rng.Intn(8)))
			}
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("mutation %d panicked: %v", i, r)
				}
			}()
			_, _ = ParseTIFF(b)
			_, _ = ReadExif(bytes.NewReader(jpegWithAPP1(append([]byte("Exif\x00\x00"), b...))))
		}()
	}
}

func TestExposureAndDateFormatting(t *testing.T) {
	if exposure(0.004) != "1/250" || exposure(2) != "2" || exposure(1.5) != "1.5" || exposure(0) != "" {
		t.Fatalf("exposure: %q %q %q %q", exposure(0.004), exposure(2), exposure(1.5), exposure(0))
	}
	if exifDate("2024:05:01 10:20:30") != "2024-05-01T10:20:30" || exifDate("0000:00:00 00:00:00") != "" || exifDate("2024") != "2024" || exifDate("2024:05:01 10:2x:30") != "2024:05:01 10:2x:30" {
		t.Fatal("date normalisation")
	}
}

func TestASCIIIsSanitised(t *testing.T) {
	bo := binary.LittleEndian
	tiff := buildTIFF(bo, []tagVal{asciiTag(tagMake, "Bad\x01\xff"+strings.Repeat("x", 400))}, nil, nil)
	ex, err := ParseTIFF(tiff)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(ex.Make, 1) || strings.ContainsRune(ex.Make, 0xFFFD) || len(ex.Make) > 256 || !strings.HasPrefix(ex.Make, "Badx") {
		t.Fatalf("make = %q", ex.Make)
	}
}

func TestGPSInvalidIgnored(t *testing.T) {
	bo := binary.LittleEndian
	gps := []tagVal{
		asciiTag(tagGPSLatRef, "N"),
		rationalTag(bo, tagGPSLat, [2]uint32{95, 1}, [2]uint32{0, 1}, [2]uint32{0, 1}), // > 90
		asciiTag(tagGPSLonRef, "E"),
		rationalTag(bo, tagGPSLon, [2]uint32{10, 1}, [2]uint32{0, 1}, [2]uint32{0, 1}),
	}
	ex, err := ParseTIFF(buildTIFF(bo, []tagVal{asciiTag(tagMake, "X")}, nil, gps))
	if err != nil || ex.GPS != nil {
		t.Fatalf("out-of-range gps must be dropped: %v %+v", err, ex.GPS)
	}
	if ExifOf(nil) != nil || ExifOf(ptr("{bad")) != nil || ExifOf(ptr(`{"make":"A"}`)).Make != "A" {
		t.Fatal("ExifOf")
	}
}

func ptr(s string) *string { return &s }
