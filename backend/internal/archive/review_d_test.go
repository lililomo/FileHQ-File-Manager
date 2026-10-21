package archive

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/bodgit/sevenzip"
)

// Review D (Step R-D): deterministic 7z password detection. bodgit derives
// an AES key even from the empty password, so an archive opened without
// one decrypts to garbage whose failure mode depends on the random IV —
// the old probe read a single byte and accepted "no error" (1/256 of the
// time for LZMA, always for an empty first member) as "not encrypted".

func openSevenZip(t *testing.T, b []byte, password string) (*sevenZipSource, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x.7z")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return newSevenZipSource(f, int64(len(b)), password)
}

// TestReviewDSevenZipEmptyFirstMember: a password-protected 7z whose first
// member is empty was reported as not encrypted (empty members carry no
// stream, so the probe read EOF and returned false); extraction without a
// password then failed as ARCHIVE_CORRUPT instead of PASSWORD_REQUIRED and
// the archive browser never issued a session.
func TestReviewDSevenZipEmptyFirstMember(t *testing.T) {
	have7z(t)
	files := map[string][]byte{"0empty.txt": {}, "z.txt": []byte("hello world"), "sub/1empty": {}}
	b := sevenZipFixture(t, "x.7z", "pw-7", false, files)
	src, err := openSevenZip(t, b, "")
	if err != nil {
		t.Fatalf("open without password: %v", err)
	}
	if !src.Encrypted() || src.HeaderEncrypted() {
		t.Fatalf("Encrypted=%v HeaderEncrypted=%v, want true/false", src.Encrypted(), src.HeaderEncrypted())
	}
	src, err = openSevenZip(t, b, "pw-7")
	if err != nil || !src.Encrypted() {
		t.Fatalf("open with password: %v enc=%v", err, src != nil && src.Encrypted())
	}
	e := newEnv(t)
	e.write(t, "x.7z", b)
	failedWith(t, e.extract(t, ExtractPayload{Archive: "/x.7z", Dest: "/o1"}, ""), CodePasswordReq)
	failedWith(t, e.extract(t, ExtractPayload{Archive: "/x.7z", Dest: "/o2"}, "nope"), CodePasswordWrong)
	xr := resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/x.7z", Dest: "/o3"}, "pw-7"))
	if xr.Files != 3 || string(e.read(t, "o3/z.txt")) != "hello world" {
		t.Fatalf("extract with password: %+v", xr)
	}
	// an archive of only empty members cannot be told apart: not encrypted,
	// and extracting it without a password yields the empty files
	only := sevenZipFixture(t, "e.7z", "pw-7", false, map[string][]byte{"a": {}, "b/c": {}})
	src, err = openSevenZip(t, only, "")
	if err != nil || src.Encrypted() {
		t.Fatalf("empty-only archive: %v enc=%v", err, src != nil && src.Encrypted())
	}
}

// TestReviewDSevenZipDetectionIsDeterministic: fresh fixtures (new IV each
// time) classify identically — header encryption, body encryption with
// LZMA and with the Copy method (no decompressor to reject garbage), a big
// first member and a plain archive.
func TestReviewDSevenZipDetectionIsDeterministic(t *testing.T) {
	have7z(t)
	big := bytes.Repeat([]byte("0123456789abcdef"), 20<<10) // 320 KiB > probe head
	type shape struct {
		name     string
		encNames bool
		extra    []string
		files    map[string][]byte
		wantHdr  bool
	}
	shapes := []shape{
		{"mhe", true, nil, map[string][]byte{"a.txt": []byte("hello"), "b.txt": {}}, true},
		{"lzma-small", false, nil, map[string][]byte{"a.txt": []byte("hello"), "b.txt": []byte("world")}, false},
		{"lzma-big", false, nil, map[string][]byte{"a.bin": big, "b.txt": []byte("x")}, false},
		{"copy-small", false, []string{"-m0=Copy"}, map[string][]byte{"a.txt": []byte("hello"), "b.txt": []byte("world")}, false},
		{"copy-big", false, []string{"-m0=Copy"}, map[string][]byte{"a.bin": big, "b.txt": []byte("x")}, false},
	}
	for _, sh := range shapes {
		for i := 0; i < 12; i++ {
			b := sevenZipFixtureArgs(t, "x.7z", "pw-7", sh.encNames, sh.extra, sh.files)
			src, err := openSevenZip(t, b, "")
			switch {
			case sh.wantHdr:
				if !errors.Is(err, ErrPasswordRequired) {
					t.Fatalf("%s #%d: open without password: %v", sh.name, i, err)
				}
			case err != nil:
				t.Fatalf("%s #%d: open without password: %v", sh.name, i, err)
			case !src.Encrypted() || src.HeaderEncrypted():
				t.Fatalf("%s #%d: Encrypted=%v HeaderEncrypted=%v", sh.name, i, src.Encrypted(), src.HeaderEncrypted())
			}
			src, err = openSevenZip(t, b, "pw-7")
			if err != nil || !src.Encrypted() || src.HeaderEncrypted() != sh.wantHdr {
				t.Fatalf("%s #%d: with password: %v enc=%v hdr=%v", sh.name, i, err, src != nil && src.Encrypted(), src != nil && src.HeaderEncrypted())
			}
			if _, err := openSevenZip(t, b, "wrong"); sh.wantHdr && !errors.Is(err, ErrPasswordWrong) {
				t.Fatalf("%s #%d: wrong password on encrypted header: %v", sh.name, i, err)
			}
		}
	}
	// plain archives never ask for a password
	for i := 0; i < 5; i++ {
		b := sevenZipFixtureArgs(t, "p.7z", "", false, []string{"-m0=Copy"}, map[string][]byte{"a.bin": big})
		src, err := openSevenZip(t, b, "")
		if err != nil || src.Encrypted() || src.HeaderEncrypted() {
			t.Fatalf("plain #%d: %v enc=%v", i, err, src != nil && src.Encrypted())
		}
	}
	// the Copy method: a wrong password decrypts to garbage without a
	// decompressor to notice — the member CRC check refuses it instead of
	// extracting garbage silently
	e := newEnv(t)
	b := sevenZipFixtureArgs(t, "c.7z", "pw-7", false, []string{"-m0=Copy"}, map[string][]byte{"a.bin": big, "b.txt": []byte("x")})
	e.write(t, "c.7z", b)
	failedWith(t, e.extract(t, ExtractPayload{Archive: "/c.7z", Dest: "/o1"}, "wrong"), CodePasswordWrong)
	failedWith(t, e.extract(t, ExtractPayload{Archive: "/c.7z", Dest: "/o2"}, ""), CodePasswordReq)
	xr := resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/c.7z", Dest: "/o3"}, "pw-7"))
	if xr.Files != 2 || !bytes.Equal(e.read(t, "o3/a.bin"), big) {
		t.Fatalf("copy extract: %+v", xr)
	}
	if _, err := os.Stat(filepath.Join(e.home, "o1")); !os.IsNotExist(err) {
		t.Fatalf("garbage extracted on wrong password: %v", err)
	}
}

// TestReviewDSevenZipErrorClassification: the error mapping relies on
// bodgit's encryption hint, not on the (IV-dependent) message text.
func TestReviewDSevenZipErrorClassification(t *testing.T) {
	for i, err := range []error{
		&sevenzip.ReadError{Encrypted: true, Err: errors.New("lzma: error creating reader: newRangeDecoder: first byte not zero")},
		&sevenzip.ReadError{Encrypted: true, Err: errors.New("sevenzip: unexpected id")},
		&sevenzip.ReadError{Encrypted: true, Err: errors.New("anything at all")},
	} {
		if !isEncryptionErr(err) {
			t.Fatalf("#%d not classified as encryption", i)
		}
	}
	if isEncryptionErr(&sevenzip.ReadError{Err: errors.New("lzma: data error")}) || isEncryptionErr(errors.New("checksum")) {
		t.Fatal("plain errors classified as encryption")
	}
}
