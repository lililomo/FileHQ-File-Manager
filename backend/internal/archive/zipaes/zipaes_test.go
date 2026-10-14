package zipaes

import (
	"archive/zip"
	"bytes"
	"compress/flate"
	"crypto/rand"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

const sevenZip = "/usr/lib/7zip/7z"

func have7z(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(sevenZip); err != nil {
		t.Skip("7z not installed")
	}
}

func run7z(t *testing.T, dir string, stdin string, args ...string) ([]byte, error) {
	t.Helper()
	cmd := exec.Command(sevenZip, args...)
	cmd.Dir = dir
	if stdin != "" {
		cmd.Stdin = bytes.NewBufferString(stdin)
	}
	return cmd.CombinedOutput()
}

// readEntry decrypts + decompresses one entry of a zip written by 7-Zip.
func readEntry(t *testing.T, f *zip.File, password string) ([]byte, error) {
	t.Helper()
	raw, err := f.OpenRaw()
	if err != nil {
		t.Fatal(err)
	}
	var (
		r      io.Reader
		method = f.Method
	)
	if f.Method == MethodAES {
		ex, ok := ParseExtra(f.Extra)
		if !ok {
			t.Fatalf("no AES extra on %s", f.Name)
		}
		method = ex.Method
		ar, err := NewReader(raw, int64(f.CompressedSize64), password, ex.Strength)
		if err != nil {
			return nil, err
		}
		r = ar
	} else {
		check := byte(f.CRC32 >> 24)
		if f.Flags&0x8 != 0 {
			check = byte(f.ModifiedTime >> 8)
		}
		zr, err := NewZipCryptoReader(raw, password, check)
		if err != nil {
			return nil, err
		}
		r = zr
	}
	switch method {
	case zip.Store:
	case zip.Deflate:
		fr := flate.NewReader(r)
		defer func() { _ = fr.Close() }()
		r = fr
	default:
		t.Fatalf("unexpected method %d", method)
	}
	return io.ReadAll(r)
}

func TestReadArchivesMadeBy7Zip(t *testing.T) {
	have7z(t)
	dir := t.TempDir()
	plain := make([]byte, 70_000)
	_, _ = rand.Read(plain[:1000])
	for i := 1000; i < len(plain); i++ {
		plain[i] = byte(i % 7)
	}
	if err := os.WriteFile(filepath.Join(dir, "data.bin"), plain, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tiny.txt"), []byte("hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, mem string }{{"aes256", "AES256"}, {"aes128", "AES128"}, {"zipcrypto", "ZipCrypto"}} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := run7z(t, dir, "", "a", "-tzip", "-mem="+tc.mem, "-pS3cret!", "-y", "-bd", "--", tc.name+".zip", "data.bin", "tiny.txt")
			if err != nil {
				t.Fatalf("7z: %v\n%s", err, out)
			}
			zr, err := zip.OpenReader(filepath.Join(dir, tc.name+".zip"))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = zr.Close() }()
			for _, f := range zr.File {
				if f.Flags&0x1 == 0 {
					t.Fatalf("%s not flagged encrypted", f.Name)
				}
				got, err := readEntry(t, f, "S3cret!")
				if err != nil {
					t.Fatalf("%s: %v", f.Name, err)
				}
				want := plain
				if f.Name == "tiny.txt" {
					want = []byte("hi")
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("%s: content mismatch (%d vs %d bytes)", f.Name, len(got), len(want))
				}
				if _, err := readEntry(t, f, "wrong"); !errors.Is(err, ErrWrongPassword) && !errors.Is(err, ErrAuthFailed) {
					t.Fatalf("%s wrong password: got %v", f.Name, err)
				}
			}
		})
	}
}

func TestWriteArchiveVerifiedBy7Zip(t *testing.T) {
	have7z(t)
	dir := t.TempDir()
	plain := bytes.Repeat([]byte("WinZip AES round trip 0123456789\n"), 4000)
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	for _, name := range []string{"a.txt", "b/c.txt"} {
		fh := &zip.FileHeader{Name: name, Method: MethodAES, Flags: 0x1 | 0x8 | 0x800, CreatorVersion: 20, ReaderVersion: 51, Extra: Extra{Version: VersionAE2, Strength: AES256, Method: zip.Deflate}.Bytes()}
		w, err := zw.CreateRaw(fh)
		if err != nil {
			t.Fatal(err)
		}
		cw := &countWriter{w: w}
		enc, err := NewWriter(cw, "p@ss word", AES256)
		if err != nil {
			t.Fatal(err)
		}
		fw, err := flate.NewWriter(enc, flate.BestSpeed)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(plain); err != nil {
			t.Fatal(err)
		}
		if err := fw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := enc.Close(); err != nil {
			t.Fatal(err)
		}
		fh.UncompressedSize64 = uint64(len(plain))
		fh.CompressedSize64 = uint64(cw.n)
		fh.CRC32 = 0 // AE-2
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ours.zip")
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	// password via stdin: 7-Zip prompts when the archive is encrypted and -p is absent
	out, err := run7z(t, dir, "p@ss word\n", "t", "-y", "-bd", "--", "ours.zip")
	if err != nil {
		t.Fatalf("7z t: %v\n%s", err, out)
	}
	if !bytes.Contains(out, []byte("Everything is Ok")) {
		t.Fatalf("7z t output: %s", out)
	}
	if out, err := run7z(t, dir, "wrong\n", "t", "-y", "-bd", "--", "ours.zip"); err == nil {
		t.Fatalf("7z accepted a wrong password: %s", out)
	}
	if out, err := run7z(t, dir, "p@ss word\n", "x", "-y", "-bd", "-oout", "--", "ours.zip"); err != nil {
		t.Fatalf("7z x: %v\n%s", err, out)
	}
	got, err := os.ReadFile(filepath.Join(dir, "out", "b", "c.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatal("extracted content differs")
	}
	// our own reader agrees (AE-2: no CRC in the header)
	zr, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	got, err = readEntry(t, zr.File[0], "p@ss word")
	if err != nil || !bytes.Equal(got, plain) {
		t.Fatalf("self read: %v", err)
	}
	if uint32(len(plain)) != uint32(zr.File[0].UncompressedSize64) || crc32.ChecksumIEEE(plain) == 0 {
		t.Fatalf("sizes not recorded: %d", zr.File[0].UncompressedSize64)
	}
}

func TestExtraRoundTrip(t *testing.T) {
	e := Extra{Version: VersionAE1, Strength: AES192, Method: 8}
	b := append([]byte{0x55, 0x55, 2, 0, 0xaa, 0xbb}, e.Bytes()...)
	got, ok := ParseExtra(b)
	if !ok || got != e {
		t.Fatalf("parse: %+v %v", got, ok)
	}
	if _, ok := ParseExtra([]byte{1, 0x99, 7, 0}); ok {
		t.Fatal("truncated extra accepted")
	}
	if AES256.Overhead() != 16+2+10 || AES128.SaltLen() != 8 {
		t.Fatal("lengths")
	}
}

func TestTruncatedAndTamperedPayload(t *testing.T) {
	buf := &bytes.Buffer{}
	w, err := NewWriter(buf, "pw", AES128)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = w.Write([]byte("hello world"))
	_ = w.Close()
	full := buf.Bytes()
	if _, err := NewReader(bytes.NewReader(full[:5]), int64(len(full)), "pw", AES128); !errors.Is(err, ErrTruncated) {
		t.Fatalf("truncated head: %v", err)
	}
	tampered := append([]byte(nil), full...)
	tampered[len(tampered)-AuthLen-1] ^= 0x01
	r, err := NewReader(bytes.NewReader(tampered), int64(len(tampered)), "pw", AES128)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(r); !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("tampered: %v", err)
	}
	r, _ = NewReader(bytes.NewReader(full), int64(len(full)), "pw", AES128)
	got, err := io.ReadAll(r)
	if err != nil || string(got) != "hello world" {
		t.Fatalf("round trip: %q %v", got, err)
	}
}

type countWriter struct {
	w io.Writer
	n int64
}

func (c *countWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}
