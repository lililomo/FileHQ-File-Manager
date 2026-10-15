package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/bzip2"
	"compress/flate"
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"time"

	"github.com/bodgit/sevenzip"
	"github.com/klauspost/compress/zstd"
	"github.com/klauspost/pgzip"
	"github.com/nwaples/rardecode/v2"
	"github.com/ulikunitz/xz"

	"kppbwi.app/filemanager/internal/archive/zipaes"
)

// Entry is one archive member as seen by the extractor.
type Entry struct {
	// Name is the decoded (UTF-8) name as stored, before SafePath.
	Name string
	// Size is the uncompressed size, -1 when the container does not know.
	Size           int64
	CompressedSize int64
	ModTime        time.Time
	Mode           fs.FileMode
	IsDir          bool
	IsSymlink      bool
	LinkTarget     string
	// Hardlink names the earlier entry this one is a hard link to (tar).
	Hardlink  string
	Encrypted bool
	Method    string
	// Open returns the body (nil for directories/links). For stream formats
	// it is only valid inside the Walk callback.
	Open func() (io.ReadCloser, error)
}

// Source reads one archive.
type Source interface {
	Format() Format
	// Index returns every entry without bodies when the container keeps a
	// cheap index (zip, 7z, rar); ok=false for streams (tar.*, single).
	Index(ctx context.Context) (entries []Entry, ok bool, err error)
	// Walk visits entries in archive order; bodies are read inside fn.
	Walk(ctx context.Context, fn func(e *Entry) error) error
	// Encrypted reports whether any entry needs a password (after Index).
	Encrypted() bool
}

// OpenOptions configure Open.
type OpenOptions struct {
	Name     string
	Password string
	Encoding string
}

// Open builds a Source for the detected format. CLI-only formats (iso)
// return ErrCLIOnly; the caller falls back to the 7-Zip path.
func Open(f *os.File, size int64, format Format, o OpenOptions) (Source, error) {
	dec := NewNameDecoder(o.Encoding)
	switch {
	case format.IsZip():
		return newZipSource(f, size, format, o.Password, dec)
	case format == FormatSevenZip:
		return newSevenZipSource(f, size, o.Password)
	case format == FormatRar:
		return newRarSource(f, o.Password)
	case format.IsTar():
		return &tarSource{f: f, format: format, dec: dec}, nil
	case format.IsSingle():
		return &singleSource{f: f, format: format, name: o.Name}, nil
	case format == FormatIso:
		return nil, ErrCLIOnly
	}
	return nil, unsupportedErr("format cannot be read", map[string]any{"format": string(format)})
}

// ErrCLIOnly: the format is only readable through the 7-Zip CLI.
var ErrCLIOnly = errors.New("archive: format needs the 7-Zip CLI")

// openDecompressor wraps r with the decoder of a compression format.
func openDecompressor(c Format, r io.Reader) (io.ReadCloser, error) {
	switch c {
	case FormatGz:
		return pgzip.NewReader(r)
	case FormatBz2:
		return io.NopCloser(bzip2.NewReader(r)), nil
	case FormatXz:
		xr, err := xz.NewReader(r)
		if err != nil {
			return nil, err
		}
		return io.NopCloser(xr), nil
	case FormatZst:
		d, err := zstd.NewReader(r, zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true))
		if err != nil {
			return nil, err
		}
		return d.IOReadCloser(), nil
	}
	return nil, unsupportedErr("unknown compression", map[string]any{"format": string(c)})
}

// ---- zip ----

type zipSource struct {
	zr       *zip.Reader
	format   Format
	password string
	dec      *NameDecoder
	enc      bool
}

func newZipSource(f *os.File, size int64, format Format, password string, dec *NameDecoder) (*zipSource, error) {
	zr, err := zip.NewReader(f, size)
	if err != nil {
		return nil, corruptErr("not a valid zip file")
	}
	raw := make([]string, 0, len(zr.File))
	enc := false
	for _, zf := range zr.File {
		if zf.Flags&0x800 == 0 {
			raw = append(raw, zf.Name)
		}
		if zf.Flags&0x1 != 0 {
			enc = true
		}
	}
	dec.Prepare(raw)
	return &zipSource{zr: zr, format: format, password: password, dec: dec, enc: enc}, nil
}

func (z *zipSource) Format() Format   { return z.format }
func (z *zipSource) Encrypted() bool  { return z.enc }
func (z *zipSource) Detected() string { return z.dec.Detected() }

func (z *zipSource) entry(zf *zip.File) Entry {
	name := z.dec.Decode(zf.Name, zf.Flags&0x800 != 0)
	mode := zf.Mode()
	e := Entry{Name: name, Size: int64(zf.UncompressedSize64), CompressedSize: int64(zf.CompressedSize64), ModTime: zf.Modified, Mode: mode, //nolint:gosec // sizes fit
		IsDir: strings.HasSuffix(zf.Name, "/") || mode.IsDir(), Encrypted: zf.Flags&0x1 != 0, Method: zipMethodName(zf)}
	if mode&fs.ModeSymlink != 0 {
		e.IsSymlink = true
		// the link target is the entry body (≤ 4 KiB)
		if rc, err := z.open(zf); err == nil {
			b, _ := io.ReadAll(io.LimitReader(rc, 4096))
			_ = rc.Close()
			e.LinkTarget = string(b)
		}
	}
	if !e.IsDir && !e.IsSymlink {
		zf := zf
		e.Open = func() (io.ReadCloser, error) { return z.open(zf) }
	}
	return e
}

func zipMethodName(zf *zip.File) string {
	m := zf.Method
	if m == zipaes.MethodAES {
		if ex, ok := zipaes.ParseExtra(zf.Extra); ok {
			m = ex.Method
		}
	}
	switch m {
	case zip.Store:
		return "store"
	case zip.Deflate:
		return "deflate"
	case 12:
		return "bzip2"
	case 14:
		return "lzma"
	case 93:
		return "zstd"
	case 95:
		return "xz"
	}
	return fmt.Sprintf("method-%d", m)
}

// open returns the plaintext body, decrypting AES/ZipCrypto entries with
// the password (SPEC §8.11 step 6/7).
func (z *zipSource) open(zf *zip.File) (io.ReadCloser, error) {
	if zf.Flags&0x1 == 0 {
		return zf.Open()
	}
	if z.password == "" {
		return nil, ErrPasswordRequired
	}
	raw, err := zf.OpenRaw()
	if err != nil {
		return nil, corruptErr("cannot read entry")
	}
	var (
		r      io.Reader
		method = zf.Method
	)
	if zf.Method == zipaes.MethodAES {
		ex, ok := zipaes.ParseExtra(zf.Extra)
		if !ok {
			return nil, corruptErr("malformed AES entry")
		}
		method = ex.Method
		ar, err := zipaes.NewReader(raw, int64(zf.CompressedSize64), z.password, ex.Strength) //nolint:gosec // fits
		if err != nil {
			if errors.Is(err, zipaes.ErrWrongPassword) {
				return nil, ErrPasswordWrong
			}
			return nil, corruptErr("encrypted entry truncated")
		}
		r = ar
	} else {
		check := byte(zf.CRC32 >> 24)
		if zf.Flags&0x8 != 0 {
			check = byte(zf.ModifiedTime >> 8)
		}
		zr, err := zipaes.NewZipCryptoReader(raw, z.password, check)
		if err != nil {
			if errors.Is(err, zipaes.ErrWrongPassword) {
				return nil, ErrPasswordWrong
			}
			return nil, corruptErr("encrypted entry truncated")
		}
		r = zr
	}
	switch method {
	case zip.Store:
	case zip.Deflate:
		r = flate.NewReader(r)
	case 12:
		r = bzip2.NewReader(r)
	default:
		return nil, unsupportedErr("zip compression method not supported", map[string]any{"method": method})
	}
	// AE-1 and ZipCrypto keep the CRC: verify it so a wrong password that
	// slipped through the check bytes is still reported.
	if zf.CRC32 != 0 {
		r = &crcReader{r: r, want: zf.CRC32, h: crc32.NewIEEE()}
	}
	return io.NopCloser(r), nil
}

type crcReader struct {
	r    io.Reader
	want uint32
	h    hashWriter32
}

type hashWriter32 interface {
	io.Writer
	Sum32() uint32
}

func (c *crcReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		_, _ = c.h.Write(p[:n])
	}
	if err == io.EOF && c.h.Sum32() != c.want {
		return n, ErrPasswordWrong
	}
	return n, err
}

func (z *zipSource) Index(context.Context) ([]Entry, bool, error) {
	out := make([]Entry, 0, len(z.zr.File))
	for _, zf := range z.zr.File {
		out = append(out, z.entry(zf))
	}
	return out, true, nil
}

func (z *zipSource) Walk(ctx context.Context, fn func(e *Entry) error) error {
	for _, zf := range z.zr.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		e := z.entry(zf)
		if err := fn(&e); err != nil {
			return err
		}
	}
	return nil
}

// ---- 7z (bodgit/sevenzip, the engine behind mholt/archives) ----

// sevenZipSource opens a fresh sevenzip.Reader per operation: bodgit serves
// a solid folder as one forward stream, and re-opening an entry after a
// partial read (or after a later entry) on the same Reader corrupts the
// LZMA state. Headers are small, so re-parsing is cheap.
type sevenZipSource struct {
	f        *os.File
	size     int64
	password string
	// enc is detected once without the password: 7z headers carry no
	// per-entry flag the library exposes, and a reader opened with the right
	// password cannot tell an encrypted archive from a plain one (review C).
	enc bool
	// hdrEnc: the header itself is encrypted (-mhe=on) → listing needs the
	// password, so a cached index must not be served without one.
	hdrEnc bool
}

func newSevenZipSource(f *os.File, size int64, password string) (*sevenZipSource, error) {
	src := &sevenZipSource{f: f, size: size, password: password}
	if _, err := src.reader(); err != nil {
		return nil, err
	}
	src.enc = src.detectEncrypted()
	return src, nil
}

// detectEncrypted opens the archive WITHOUT a password and tries to read the
// first regular member: encrypted headers fail at open, encrypted bodies at
// the first read ("aes7z: no password set").
func (s *sevenZipSource) detectEncrypted() bool {
	r, err := sevenzip.NewReader(s.f, s.size)
	if err != nil {
		s.hdrEnc = true
		return true
	}
	for _, zf := range r.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return true
		}
		var probe [1]byte
		_, rerr := rc.Read(probe[:])
		_ = rc.Close()
		return rerr != nil && !errors.Is(rerr, io.EOF)
	}
	return false
}

func (s *sevenZipSource) reader() (*sevenzip.Reader, error) {
	r, err := sevenzip.NewReaderWithPassword(s.f, s.size, s.password)
	if err != nil {
		switch {
		case isPasswordErr(err):
			if s.password == "" {
				return nil, ErrPasswordRequired
			}
			return nil, ErrPasswordWrong
		case s.password == "" && isEncryptedHeaderErr(err):
			// -mhe=on: the header itself is AES-encrypted; without a password
			// bodgit cannot parse it ("unexpected id"). The signature was
			// already verified by Detect, so a parse failure here means
			// "password required" rather than "corrupt" (review C).
			return nil, ErrPasswordRequired
		case s.password != "":
			// a wrong password on encrypted headers surfaces as a parse error
			// (same mapping as the 7-Zip CLI "can't open as archive" case)
			return nil, ErrPasswordWrong
		}
		return nil, corruptErr("not a valid 7z file")
	}
	return r, nil
}

// isEncryptedHeaderErr matches the error bodgit/sevenzip reports when the
// archive header is encrypted (-mhe=on) and no/the wrong password was given.
func isEncryptedHeaderErr(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "unexpected id")
}

func isPasswordErr(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "password") || strings.Contains(s, "checksum") || strings.Contains(s, "crc")
}

func (s *sevenZipSource) Format() Format { return FormatSevenZip }

// Encrypted reports the password-less probe made at open (see detectEncrypted).
func (s *sevenZipSource) Encrypted() bool { return s.enc }

// HeaderEncrypted reports whether the member list itself needs the password.
func (s *sevenZipSource) HeaderEncrypted() bool { return s.hdrEnc }

func (s *sevenZipSource) entry(zf *sevenzip.File) Entry {
	fi := zf.FileInfo()
	e := Entry{Name: zf.Name, Size: fi.Size(), CompressedSize: -1, ModTime: zf.Modified, Mode: fi.Mode(), IsDir: fi.IsDir(), Encrypted: s.enc && !fi.IsDir()}
	if fi.Mode()&fs.ModeSymlink != 0 {
		e.IsSymlink = true
		if rc, err := zf.Open(); err == nil {
			b, _ := io.ReadAll(io.LimitReader(rc, 4096))
			_ = rc.Close()
			e.LinkTarget = string(b)
		}
	}
	if !e.IsDir && !e.IsSymlink {
		e.Open = func() (io.ReadCloser, error) {
			rc, err := zf.Open()
			if err != nil {
				if isPasswordErr(err) || s.password != "" {
					return nil, ErrPasswordWrong
				}
				return nil, corruptErr("cannot read 7z entry")
			}
			return &passwordAwareReader{rc: rc, enc: s.password != ""}, nil
		}
	}
	return e
}

// passwordAwareReader maps a decode failure on a password-protected stream
// to ARCHIVE_PASSWORD_WRONG (bodgit reports it as an LZMA/checksum error).
type passwordAwareReader struct {
	rc  io.ReadCloser
	enc bool
}

func (p *passwordAwareReader) Read(b []byte) (int, error) {
	n, err := p.rc.Read(b)
	if err != nil && !errors.Is(err, io.EOF) && (p.enc || isPasswordErr(err)) {
		return n, ErrPasswordWrong
	}
	return n, err
}

func (p *passwordAwareReader) Close() error { return p.rc.Close() }

func (s *sevenZipSource) Index(context.Context) ([]Entry, bool, error) {
	r, err := s.reader()
	if err != nil {
		return nil, true, err
	}
	out := make([]Entry, 0, len(r.File))
	for _, zf := range r.File {
		e := s.entry(zf)
		e.Open = nil
		out = append(out, e)
	}
	return out, true, nil
}

func (s *sevenZipSource) Walk(ctx context.Context, fn func(e *Entry) error) error {
	r, err := s.reader()
	if err != nil {
		return err
	}
	for _, zf := range r.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		e := s.entry(zf)
		if err := fn(&e); err != nil {
			return err
		}
	}
	return nil
}

// ---- rar (nwaples/rardecode, the engine behind mholt/archives) ----

type rarSource struct {
	f        *os.File
	password string
}

func newRarSource(f *os.File, password string) (*rarSource, error) {
	return &rarSource{f: f, password: password}, nil
}

func (r *rarSource) Format() Format { return FormatRar }

// HeaderEncrypted reports a -hp archive: rardecode refuses to open it
// without a password (ErrArchiveEncrypted), so the cached index must not
// be served to a request without one.
func (r *rarSource) HeaderEncrypted() bool {
	if _, err := r.f.Seek(0, io.SeekStart); err != nil {
		return false
	}
	_, err := rardecode.NewReader(r.f)
	_, _ = r.f.Seek(0, io.SeekStart)
	return errors.Is(err, rardecode.ErrArchiveEncrypted)
}

func (r *rarSource) reader() (*rardecode.Reader, error) {
	if _, err := r.f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	rr, err := rardecode.NewReader(r.f, rardecode.Password(r.password))
	if err != nil {
		return nil, mapRarErr(err, r.password)
	}
	return rr, nil
}

func mapRarErr(err error, password string) error {
	switch {
	case errors.Is(err, rardecode.ErrArchiveEncrypted), errors.Is(err, rardecode.ErrArchivedFileEncrypted):
		if password == "" {
			return ErrPasswordRequired
		}
		return ErrPasswordWrong
	case errors.Is(err, rardecode.ErrBadPassword):
		if password == "" {
			return ErrPasswordRequired
		}
		return ErrPasswordWrong
	case errors.Is(err, rardecode.ErrNoSig):
		return corruptErr("not a valid rar file")
	}
	return corruptErr("cannot read rar file: " + err.Error())
}

func (r *rarSource) Encrypted() bool {
	rr, err := r.reader()
	if err != nil {
		return errors.Is(err, ErrPasswordRequired) || errors.Is(err, ErrPasswordWrong)
	}
	for {
		h, err := rr.Next()
		if err != nil {
			return errors.Is(mapRarErr(err, r.password), ErrPasswordRequired)
		}
		if h.Encrypted || h.HeaderEncrypted {
			return true
		}
	}
}

func rarEntry(h *rardecode.FileHeader, body io.Reader) Entry {
	e := Entry{Name: h.Name, Size: h.UnPackedSize, CompressedSize: h.PackedSize, ModTime: h.ModificationTime, Mode: h.Mode(), IsDir: h.IsDir, Encrypted: h.Encrypted}
	if h.UnKnownSize {
		e.Size = -1
	}
	if h.Mode()&fs.ModeSymlink != 0 {
		e.IsSymlink = true
		b, _ := io.ReadAll(io.LimitReader(body, 4096))
		e.LinkTarget = string(b)
	}
	if !e.IsDir && !e.IsSymlink {
		e.Open = func() (io.ReadCloser, error) { return io.NopCloser(&rarBody{r: body}), nil }
	}
	return e
}

type rarBody struct{ r io.Reader }

func (b *rarBody) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	if err != nil && err != io.EOF {
		return n, mapRarErr(err, "x")
	}
	return n, err
}

func (r *rarSource) Index(ctx context.Context) ([]Entry, bool, error) {
	rr, err := r.reader()
	if err != nil {
		return nil, true, err
	}
	var out []Entry
	for {
		if err := ctx.Err(); err != nil {
			return nil, true, err
		}
		h, err := rr.Next()
		if errors.Is(err, io.EOF) {
			return out, true, nil
		}
		if err != nil {
			return nil, true, mapRarErr(err, r.password)
		}
		e := rarEntry(h, rr)
		e.Open = nil
		out = append(out, e)
	}
}

func (r *rarSource) Walk(ctx context.Context, fn func(e *Entry) error) error {
	rr, err := r.reader()
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		h, err := rr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return mapRarErr(err, r.password)
		}
		e := rarEntry(h, rr)
		if err := fn(&e); err != nil {
			return err
		}
	}
}

// ---- tar (optionally compressed) ----

type tarSource struct {
	f      *os.File
	format Format
	dec    *NameDecoder
}

func (t *tarSource) Format() Format   { return t.format }
func (t *tarSource) Encrypted() bool  { return false }
func (t *tarSource) Detected() string { return t.dec.Detected() }

func (t *tarSource) Index(context.Context) ([]Entry, bool, error) { return nil, false, nil }

func (t *tarSource) Walk(ctx context.Context, fn func(e *Entry) error) error {
	if _, err := t.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	var r io.Reader = t.f
	if c := t.format.Compression(); c != "" {
		rc, err := openDecompressor(c, t.f)
		if err != nil {
			return corruptErr("cannot decompress stream")
		}
		defer func() { _ = rc.Close() }()
		r = rc
	}
	tr := tar.NewReader(r)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return corruptErr("invalid tar stream: " + err.Error())
		}
		e := Entry{Name: t.dec.Decode(h.Name, false), Size: h.Size, CompressedSize: -1, ModTime: h.ModTime, Mode: h.FileInfo().Mode()}
		switch h.Typeflag {
		case tar.TypeDir:
			e.IsDir = true
		case tar.TypeSymlink:
			e.IsSymlink, e.LinkTarget = true, h.Linkname
		case tar.TypeLink:
			e.Hardlink = t.dec.Decode(h.Linkname, false)
		case tar.TypeReg:
			e.Open = func() (io.ReadCloser, error) { return io.NopCloser(tr), nil }
		case tar.TypeXGlobalHeader:
			continue
		default:
			// char/block devices, fifos, sockets: reported, never created
			e.Mode |= fs.ModeDevice
		}
		if err := fn(&e); err != nil {
			return err
		}
	}
}

// ---- single compressed file ----

type singleSource struct {
	f      *os.File
	format Format
	name   string
}

func (s *singleSource) Format() Format  { return s.format }
func (s *singleSource) Encrypted() bool { return false }

func (s *singleSource) Index(context.Context) ([]Entry, bool, error) { return nil, false, nil }

func (s *singleSource) Walk(ctx context.Context, fn func(e *Entry) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := s.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	name := Stem(path.Base(s.name))
	if name == "" || name == path.Base(s.name) {
		name = path.Base(s.name) + ".out"
	}
	var size int64 = -1
	if s.format == FormatGz {
		// ISIZE trailer: size mod 2^32 (a hint only; the stream is bounded anyway)
		if st, err := s.f.Stat(); err == nil && st.Size() >= 8 {
			var tail [4]byte
			if _, err := s.f.ReadAt(tail[:], st.Size()-4); err == nil {
				size = int64(uint32(tail[0]) | uint32(tail[1])<<8 | uint32(tail[2])<<16 | uint32(tail[3])<<24)
			}
		}
	}
	st, _ := s.f.Stat()
	e := Entry{Name: name, Size: size, CompressedSize: -1, Mode: 0o640}
	if st != nil {
		e.ModTime = st.ModTime()
		e.CompressedSize = st.Size()
	}
	e.Open = func() (io.ReadCloser, error) {
		if _, err := s.f.Seek(0, io.SeekStart); err != nil {
			return nil, err
		}
		rc, err := openDecompressor(s.format, s.f)
		if err != nil {
			return nil, corruptErr("cannot decompress stream")
		}
		return rc, nil
	}
	return fn(&e)
}
