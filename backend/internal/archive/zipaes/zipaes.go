// Package zipaes implements the WinZip AES encryption scheme (AE-1/AE-2:
// PBKDF2-HMAC-SHA1, AES-CTR with a little-endian counter, HMAC-SHA1 auth
// code) and traditional PKWARE "ZipCrypto" decryption on top of the standard
// archive/zip container (SPEC §8.11, ADR-08). It only touches the entry
// payload: the caller reads/writes headers with archive/zip's OpenRaw and
// CreateRaw, so no fork of the standard library is needed.
package zipaes

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // the WinZip AES scheme mandates HMAC-SHA1
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"

	"golang.org/x/crypto/pbkdf2"
)

// Constants of the scheme.
const (
	// MethodAES is the compression method of an AES-encrypted entry; the
	// real method is stored in the extra field.
	MethodAES uint16 = 99
	// ExtraID is the header id of the AES extra field.
	ExtraID uint16 = 0x9901
	// AuthLen is the length of the authentication code.
	AuthLen = 10
	// CheckLen is the length of the password verification value.
	CheckLen = 2
	// Iterations of PBKDF2.
	Iterations = 1000
	// VersionAE1 keeps the CRC-32 in the header; VersionAE2 sets it to 0.
	VersionAE1 uint16 = 1
	VersionAE2 uint16 = 2
)

// Strength selects the AES key size.
type Strength byte

// Key strengths of the extra field.
const (
	AES128 Strength = 1
	AES192 Strength = 2
	AES256 Strength = 3
)

// KeyLen returns the AES key length in bytes.
func (s Strength) KeyLen() int { return 8 * (int(s) + 1) }

// SaltLen returns the salt length (half the key length).
func (s Strength) SaltLen() int { return s.KeyLen() / 2 }

// Valid reports whether s is one of the three defined strengths.
func (s Strength) Valid() bool { return s >= AES128 && s <= AES256 }

// Overhead is the number of bytes added around the ciphertext.
func (s Strength) Overhead() int { return s.SaltLen() + CheckLen + AuthLen }

// Errors.
var (
	ErrWrongPassword = errors.New("zipaes: wrong password")
	ErrAuthFailed    = errors.New("zipaes: authentication code mismatch")
	ErrBadExtra      = errors.New("zipaes: malformed AES extra field")
	ErrTruncated     = errors.New("zipaes: encrypted data truncated")
)

// Extra is the decoded 0x9901 extra field.
type Extra struct {
	Version  uint16
	Strength Strength
	Method   uint16 // actual compression method (0 store, 8 deflate, ...)
}

// ParseExtra finds and decodes the AES field inside a zip extra block.
func ParseExtra(extra []byte) (Extra, bool) {
	for len(extra) >= 4 {
		id := binary.LittleEndian.Uint16(extra)
		size := int(binary.LittleEndian.Uint16(extra[2:]))
		if len(extra) < 4+size {
			return Extra{}, false
		}
		body := extra[4 : 4+size]
		if id == ExtraID && size >= 7 && body[2] == 'A' && body[3] == 'E' {
			e := Extra{Version: binary.LittleEndian.Uint16(body), Strength: Strength(body[4]), Method: binary.LittleEndian.Uint16(body[5:])}
			if !e.Strength.Valid() || (e.Version != VersionAE1 && e.Version != VersionAE2) {
				return Extra{}, false
			}
			return e, true
		}
		extra = extra[4+size:]
	}
	return Extra{}, false
}

// Bytes encodes the extra field (11 bytes).
func (e Extra) Bytes() []byte {
	b := make([]byte, 11)
	binary.LittleEndian.PutUint16(b, ExtraID)
	binary.LittleEndian.PutUint16(b[2:], 7)
	binary.LittleEndian.PutUint16(b[4:], e.Version)
	b[6], b[7] = 'A', 'E'
	b[8] = byte(e.Strength)
	binary.LittleEndian.PutUint16(b[9:], e.Method)
	return b
}

func deriveKeys(password string, salt []byte, s Strength) (encKey, macKey, check []byte) {
	n := s.KeyLen()
	dk := pbkdf2.Key([]byte(password), salt, Iterations, 2*n+CheckLen, sha1.New)
	return dk[:n], dk[n : 2*n], dk[2*n:]
}

// ctrStream is AES in CTR mode with the WinZip little-endian counter
// starting at 1 (crypto/cipher's CTR is big-endian).
type ctrStream struct {
	block   cipher.Block
	counter [16]byte
	buf     [16]byte
	used    int
}

func newCTR(key []byte) (*ctrStream, error) {
	b, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	c := &ctrStream{block: b, used: 16}
	return c, nil
}

func (c *ctrStream) XORKeyStream(dst, src []byte) {
	for i := range src {
		if c.used == 16 {
			// increment the 128-bit little-endian counter
			for j := 0; j < 16; j++ {
				c.counter[j]++
				if c.counter[j] != 0 {
					break
				}
			}
			c.block.Encrypt(c.buf[:], c.counter[:])
			c.used = 0
		}
		dst[i] = src[i] ^ c.buf[c.used]
		c.used++
	}
}

// Reader decrypts one entry and verifies the authentication code at EOF.
type Reader struct {
	r         io.Reader
	remaining int64 // ciphertext bytes still to read (excluding the auth code)
	ctr       *ctrStream
	mac       hashWriter
	auth      [AuthLen]byte
	done      bool
	err       error
}

type hashWriter interface {
	io.Writer
	Sum(b []byte) []byte
}

// NewReader wraps the raw (still compressed) payload of an entry whose
// stored size is size bytes: salt + check + ciphertext + auth code. The
// password is verified immediately (ErrWrongPassword); the auth code is
// verified when the ciphertext is exhausted (ErrAuthFailed).
func NewReader(r io.Reader, size int64, password string, s Strength) (*Reader, error) {
	if !s.Valid() {
		return nil, ErrBadExtra
	}
	if size < int64(s.Overhead()) {
		return nil, ErrTruncated
	}
	head := make([]byte, s.SaltLen()+CheckLen)
	if _, err := io.ReadFull(r, head); err != nil {
		return nil, ErrTruncated
	}
	salt, check := head[:s.SaltLen()], head[s.SaltLen():]
	encKey, macKey, want := deriveKeys(password, salt, s)
	if subtle.ConstantTimeCompare(check, want) != 1 {
		return nil, ErrWrongPassword
	}
	ctr, err := newCTR(encKey)
	if err != nil {
		return nil, err
	}
	return &Reader{r: r, remaining: size - int64(s.Overhead()), ctr: ctr, mac: hmac.New(sha1.New, macKey)}, nil
}

// Read implements io.Reader.
func (z *Reader) Read(p []byte) (int, error) {
	if z.err != nil {
		return 0, z.err
	}
	if z.remaining == 0 {
		if !z.done {
			z.done = true
			if _, err := io.ReadFull(z.r, z.auth[:]); err != nil {
				z.err = ErrTruncated
				return 0, z.err
			}
			if subtle.ConstantTimeCompare(z.mac.Sum(nil)[:AuthLen], z.auth[:]) != 1 {
				z.err = ErrAuthFailed
				return 0, z.err
			}
		}
		z.err = io.EOF
		return 0, io.EOF
	}
	if int64(len(p)) > z.remaining {
		p = p[:z.remaining]
	}
	n, err := z.r.Read(p)
	if n > 0 {
		_, _ = z.mac.Write(p[:n])
		z.ctr.XORKeyStream(p[:n], p[:n])
		z.remaining -= int64(n)
	}
	if err == io.EOF && z.remaining > 0 {
		z.err = ErrTruncated
		return n, z.err
	}
	if err != nil && err != io.EOF {
		z.err = err
		return n, err
	}
	return n, nil
}

// Writer encrypts one entry: salt + check are written on creation, the
// auth code on Close. The ciphertext length equals the plaintext length.
type Writer struct {
	w      io.Writer
	ctr    *ctrStream
	mac    hashWriter
	closed bool
	buf    []byte
}

// NewWriter starts an encrypted payload with a random salt.
func NewWriter(w io.Writer, password string, s Strength) (*Writer, error) {
	if !s.Valid() {
		return nil, ErrBadExtra
	}
	salt := make([]byte, s.SaltLen())
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	encKey, macKey, check := deriveKeys(password, salt, s)
	ctr, err := newCTR(encKey)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(salt); err != nil {
		return nil, err
	}
	if _, err := w.Write(check); err != nil {
		return nil, err
	}
	return &Writer{w: w, ctr: ctr, mac: hmac.New(sha1.New, macKey), buf: make([]byte, 0, 32<<10)}, nil
}

// Write encrypts p.
func (z *Writer) Write(p []byte) (int, error) {
	if z.closed {
		return 0, errors.New("zipaes: write after close")
	}
	total := 0
	for len(p) > 0 {
		n := min(len(p), cap(z.buf))
		z.buf = z.buf[:n]
		z.ctr.XORKeyStream(z.buf, p[:n])
		_, _ = z.mac.Write(z.buf)
		if _, err := z.w.Write(z.buf); err != nil {
			return total, err
		}
		p = p[n:]
		total += n
	}
	return total, nil
}

// Close writes the authentication code.
func (z *Writer) Close() error {
	if z.closed {
		return nil
	}
	z.closed = true
	_, err := z.w.Write(z.mac.Sum(nil)[:AuthLen])
	return err
}

// ---- traditional PKWARE encryption (decrypt only) ----

var crcTable = crc32.MakeTable(crc32.IEEE)

type zipCrypto struct {
	k0, k1, k2 uint32
}

// update follows APPNOTE 6.1.5; the byte truncations are the algorithm.
func (z *zipCrypto) update(c byte) {
	z.k0 = crcTable[byte(z.k0)^c] ^ (z.k0 >> 8) //nolint:gosec // low byte by design
	z.k1 = (z.k1+(z.k0&0xff))*134775813 + 1
	z.k2 = crcTable[byte(z.k2)^byte(z.k1>>24)] ^ (z.k2 >> 8) //nolint:gosec // low byte by design
}

func (z *zipCrypto) decryptByte(c byte) byte {
	t := uint16(z.k2 | 2) //nolint:gosec // low 16 bits by design
	c ^= byte((t * (t ^ 1)) >> 8)
	z.update(c)
	return c
}

// ZipCryptoReader decrypts a ZipCrypto entry.
type ZipCryptoReader struct {
	r  io.Reader
	zc zipCrypto
}

// NewZipCryptoReader verifies the 12-byte encryption header against check
// (the high byte of the CRC-32, or of the DOS time when general purpose
// flag bit 3 is set) and returns a reader of the still-compressed bytes.
func NewZipCryptoReader(r io.Reader, password string, check byte) (*ZipCryptoReader, error) {
	z := &ZipCryptoReader{r: r, zc: zipCrypto{k0: 0x12345678, k1: 0x23456789, k2: 0x34567890}}
	for i := 0; i < len(password); i++ {
		z.zc.update(password[i])
	}
	var head [12]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return nil, ErrTruncated
	}
	for i := range head {
		head[i] = z.zc.decryptByte(head[i])
	}
	if head[11] != check {
		return nil, ErrWrongPassword
	}
	return z, nil
}

// Read implements io.Reader.
func (z *ZipCryptoReader) Read(p []byte) (int, error) {
	n, err := z.r.Read(p)
	for i := 0; i < n; i++ {
		p[i] = z.zc.decryptByte(p[i])
	}
	return n, err
}
