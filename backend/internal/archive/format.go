package archive

import (
	"bytes"
	"errors"
	"io"
	"path"
	"strings"
)

// Format identifies an archive or compression format (SPEC §8.11 matrix).
type Format string

// Formats.
const (
	FormatZip      Format = "zip"
	FormatSevenZip Format = "7z"
	FormatRar      Format = "rar"
	FormatTar      Format = "tar"
	FormatTarGz    Format = "tar.gz"
	FormatTarBz2   Format = "tar.bz2"
	FormatTarXz    Format = "tar.xz"
	FormatTarZst   Format = "tar.zst"
	FormatGz       Format = "gz"
	FormatBz2      Format = "bz2"
	FormatXz       Format = "xz"
	FormatZst      Format = "zst"
	FormatJar      Format = "jar"
	FormatWar      Format = "war"
	FormatIso      Format = "iso"
)

// Engine names reported by the capability table.
const (
	EngineGo  = "go"
	EngineCLI = "7z"
)

// Capability describes what the server can do with one format.
type Capability struct {
	Format        Format `json:"format"`
	Extension     string `json:"extension"`
	CanRead       bool   `json:"canRead"`
	CanWrite      bool   `json:"canWrite"`
	PasswordRead  bool   `json:"passwordRead"`
	PasswordWrite bool   `json:"passwordWrite"`
	EncryptNames  bool   `json:"encryptNames"`
	SplitVolume   bool   `json:"splitVolume"`
	Levels        bool   `json:"levels"`
	// SingleFile formats compress exactly one regular file.
	SingleFile bool   `json:"singleFile"`
	Engine     string `json:"engine"`
	// Reason explains a disabled capability (dependency missing, licence).
	Reason string `json:"reason,omitempty"`
}

// capabilityTable is the static matrix; Capabilities() applies the tool
// availability on top of it.
var capabilityTable = []Capability{
	{Format: FormatZip, Extension: "zip", CanRead: true, CanWrite: true, PasswordRead: true, PasswordWrite: true, Levels: true, Engine: EngineGo},
	{Format: FormatSevenZip, Extension: "7z", CanRead: true, CanWrite: true, PasswordRead: true, PasswordWrite: true, EncryptNames: true, SplitVolume: true, Levels: true, Engine: EngineCLI},
	{Format: FormatRar, Extension: "rar", CanRead: true, CanWrite: false, PasswordRead: true, Engine: EngineGo, Reason: "rar_licence"},
	{Format: FormatTar, Extension: "tar", CanRead: true, CanWrite: true, Engine: EngineGo},
	{Format: FormatTarGz, Extension: "tar.gz", CanRead: true, CanWrite: true, Levels: true, Engine: EngineGo},
	{Format: FormatTarBz2, Extension: "tar.bz2", CanRead: true, CanWrite: true, Levels: true, Engine: EngineGo},
	{Format: FormatTarXz, Extension: "tar.xz", CanRead: true, CanWrite: true, Engine: EngineGo},
	{Format: FormatTarZst, Extension: "tar.zst", CanRead: true, CanWrite: true, Levels: true, Engine: EngineGo},
	{Format: FormatGz, Extension: "gz", CanRead: true, CanWrite: true, Levels: true, SingleFile: true, Engine: EngineGo},
	{Format: FormatBz2, Extension: "bz2", CanRead: true, CanWrite: true, Levels: true, SingleFile: true, Engine: EngineGo},
	{Format: FormatXz, Extension: "xz", CanRead: true, CanWrite: true, SingleFile: true, Engine: EngineGo},
	{Format: FormatZst, Extension: "zst", CanRead: true, CanWrite: true, Levels: true, SingleFile: true, Engine: EngineGo},
	{Format: FormatJar, Extension: "jar", CanRead: true, CanWrite: true, Levels: true, Engine: EngineGo},
	{Format: FormatWar, Extension: "war", CanRead: true, CanWrite: true, Levels: true, Engine: EngineGo},
	{Format: FormatIso, Extension: "iso", CanRead: true, CanWrite: false, Engine: EngineCLI},
}

// ParseFormat accepts the matrix names and the common aliases.
func ParseFormat(s string) (Format, bool) {
	s = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "."))
	switch s {
	case "tgz":
		return FormatTarGz, true
	case "tbz2", "tbz":
		return FormatTarBz2, true
	case "txz":
		return FormatTarXz, true
	case "tzst":
		return FormatTarZst, true
	case "gzip":
		return FormatGz, true
	case "bzip2":
		return FormatBz2, true
	case "zstd":
		return FormatZst, true
	}
	for _, c := range capabilityTable {
		if string(c.Format) == s {
			return c.Format, true
		}
	}
	return "", false
}

// Extension returns the file extension (without dot) used for new archives.
func (f Format) Extension() string {
	for _, c := range capabilityTable {
		if c.Format == f {
			return c.Extension
		}
	}
	return string(f)
}

// IsZip reports whether the container is a zip file (zip, jar, war).
func (f Format) IsZip() bool { return f == FormatZip || f == FormatJar || f == FormatWar }

// IsTar reports whether the container is a tar stream (optionally compressed).
func (f Format) IsTar() bool {
	return f == FormatTar || f == FormatTarGz || f == FormatTarBz2 || f == FormatTarXz || f == FormatTarZst
}

// IsSingle reports whether the format compresses one file without a container.
func (f Format) IsSingle() bool {
	return f == FormatGz || f == FormatBz2 || f == FormatXz || f == FormatZst
}

// Compression returns the stream compression of tar.* and single formats.
func (f Format) Compression() Format {
	switch f {
	case FormatTarGz, FormatGz:
		return FormatGz
	case FormatTarBz2, FormatBz2:
		return FormatBz2
	case FormatTarXz, FormatXz:
		return FormatXz
	case FormatTarZst, FormatZst:
		return FormatZst
	}
	return ""
}

// Stem strips the archive extension of name ("backup.tar.gz" → "backup").
func Stem(name string) string {
	lower := strings.ToLower(name)
	for _, ext := range []string{".tar.gz", ".tar.bz2", ".tar.xz", ".tar.zst", ".tgz", ".tbz2", ".txz", ".tzst"} {
		if strings.HasSuffix(lower, ext) && len(name) > len(ext) {
			return name[:len(name)-len(ext)]
		}
	}
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		return name[:i]
	}
	return name
}

// ErrUnknownFormat: neither magic bytes nor the extension identify the file.
var ErrUnknownFormat = errors.New("archive: unknown format")

var (
	magicZip   = []byte("PK\x03\x04")
	magicZipE  = []byte("PK\x05\x06")
	magicZipS  = []byte("PK\x07\x08")
	magic7z    = []byte("7z\xBC\xAF\x27\x1C")
	magicRar   = []byte("Rar!\x1A\x07")
	magicGz    = []byte{0x1f, 0x8b}
	magicBz2   = []byte("BZh")
	magicXz    = []byte{0xfd, '7', 'z', 'X', 'Z', 0x00}
	magicZst   = []byte{0x28, 0xb5, 0x2f, 0xfd}
	magicUstar = []byte("ustar")
	magicIso   = []byte("CD001")
)

// byExtension maps the (compound) extension to a format.
func byExtension(name string) (Format, bool) {
	lower := strings.ToLower(name)
	for _, ext := range []string{"tar.gz", "tar.bz2", "tar.xz", "tar.zst"} {
		if strings.HasSuffix(lower, "."+ext) {
			return Format(ext), true
		}
	}
	ext := strings.TrimPrefix(path.Ext(lower), ".")
	if ext == "" {
		return "", false
	}
	return ParseFormat(ext)
}

// Detect identifies the format from the first bytes (magic), then the
// extension for compressed streams (tar inside gzip vs plain gzip) and as a
// fallback. probe reads at an offset; size is the file size.
func Detect(name string, r io.ReaderAt, size int64) (Format, error) {
	head := make([]byte, 512)
	n, _ := r.ReadAt(head, 0)
	head = head[:n]
	byExt, extKnown := byExtension(name)
	has := func(m []byte) bool { return bytes.HasPrefix(head, m) }
	switch {
	case has(magicZip), has(magicZipE), has(magicZipS):
		if extKnown && byExt.IsZip() {
			return byExt, nil
		}
		return FormatZip, nil
	case has(magic7z):
		return FormatSevenZip, nil
	case has(magicRar):
		return FormatRar, nil
	case has(magicGz):
		return compressedKind(FormatGz, byExt, extKnown, r), nil
	case has(magicBz2):
		return compressedKind(FormatBz2, byExt, extKnown, r), nil
	case has(magicXz):
		return compressedKind(FormatXz, byExt, extKnown, r), nil
	case has(magicZst):
		return compressedKind(FormatZst, byExt, extKnown, r), nil
	}
	if size > 262+5 {
		var at [5]byte
		if m, _ := r.ReadAt(at[:], 257); m == 5 && bytes.Equal(at[:], magicUstar) {
			return FormatTar, nil
		}
	}
	if size > 0x8001+5 {
		var at [5]byte
		if m, _ := r.ReadAt(at[:], 0x8001); m == 5 && bytes.Equal(at[:], magicIso) {
			return FormatIso, nil
		}
	}
	if extKnown && byExt == FormatTar && len(head) >= 512 && looksLikeTarHeader(head) {
		return FormatTar, nil
	}
	return "", ErrUnknownFormat
}

// compressedKind decides between tar.<c> and a single compressed file: the
// extension wins; without one the decompressed head is checked for a tar
// header.
func compressedKind(c Format, byExt Format, extKnown bool, r io.ReaderAt) Format {
	if extKnown {
		if byExt.IsTar() && byExt.Compression() == c {
			return byExt
		}
		if byExt == c {
			return c
		}
	}
	// no (or misleading) extension: peek the decompressed stream
	sr := io.NewSectionReader(r, 0, 1<<62)
	rc, err := openDecompressor(c, sr)
	if err != nil {
		return c
	}
	defer func() { _ = rc.Close() }()
	buf := make([]byte, 512)
	n, _ := io.ReadFull(rc, buf)
	if n == 512 && looksLikeTarHeader(buf) {
		switch c {
		case FormatGz:
			return FormatTarGz
		case FormatBz2:
			return FormatTarBz2
		case FormatXz:
			return FormatTarXz
		case FormatZst:
			return FormatTarZst
		}
	}
	return c
}

// looksLikeTarHeader checks the ustar magic or a valid header checksum.
func looksLikeTarHeader(b []byte) bool {
	if len(b) < 512 {
		return false
	}
	if bytes.Equal(b[257:262], magicUstar) {
		return true
	}
	// v7 tar: verify the checksum field
	var sum int64
	for i, c := range b[:512] {
		if i >= 148 && i < 156 {
			sum += ' '
		} else {
			sum += int64(c)
		}
	}
	field := strings.Trim(string(b[148:156]), " \x00")
	if field == "" {
		return false
	}
	var want int64
	for _, c := range field {
		if c < '0' || c > '7' {
			return false
		}
		want = want*8 + int64(c-'0')
	}
	return want == sum
}
