package files

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"kppbwi.app/filemanager/internal/fsjail"
)

func TestIsBinary(t *testing.T) {
	if IsBinary([]byte("plain text\nwith lines\t\x1b[0m and escapes\n")) {
		t.Fatal("text flagged binary")
	}
	if !IsBinary([]byte("PNG\x00\x00\x00\rIHDR")) {
		t.Fatal("NUL not detected")
	}
	ctl := bytes.Repeat([]byte{0x01, 0x02, 'a'}, 100)
	if !IsBinary(ctl) {
		t.Fatal("66 % control bytes must be binary")
	}
	if IsBinary(nil) {
		t.Fatal("empty is text")
	}
	// a NUL after the first 8000 bytes does not count (git heuristic)
	late := append(bytes.Repeat([]byte("x"), BinarySample), 0)
	if IsBinary(late) {
		t.Fatal("NUL beyond the sample must not count")
	}
}

func contentJail(t *testing.T) (*fsjail.Jail, string) {
	t.Helper()
	home := t.TempDir()
	j, err := fsjail.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	return j, home
}

// TestReadContent: a small file is decoded with its EOL and line count; a
// CRLF file keeps its bytes; a binary file is refused; a directory is a
// validation error; a file beyond MaxSize is refused without tail and
// served read-only in tail mode with the exact last lines.
func TestReadContent(t *testing.T) {
	j, home := contentJail(t)
	write := func(name string, data []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(home, name), data, 0o640); err != nil {
			t.Fatal(err)
		}
	}
	write("a.txt", []byte("one\r\ntwo\r\n"))
	c, err := ReadContent(j, "/a.txt", ContentOptions{MaxSize: 1 << 20})
	if err != nil || c.Content != "one\r\ntwo\r\n" || c.EOL != EOLCRLF || c.Lines != 2 || c.Encoding != EncUTF8 || c.ReadOnly || c.Truncated {
		t.Fatalf("crlf: %+v %v", c, err)
	}
	if c.Etag == "" || c.Language != "plaintext" || c.Mode != "0640" {
		t.Fatalf("meta: %+v", c)
	}
	write("img.png", []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR"))
	if _, err := ReadContent(j, "/img.png", ContentOptions{MaxSize: 1 << 20}); err == nil {
		t.Fatal("binary accepted")
	} else if be := (*BinaryError)(nil); !errors.As(err, &be) {
		t.Fatalf("want BinaryError, got %T", err)
	}
	if err := os.Mkdir(filepath.Join(home, "d"), 0o750); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadContent(j, "/d", ContentOptions{}); err == nil {
		t.Fatal("dir accepted")
	}
	// 3000 lines of 20 bytes = 60 KB > MaxSize 16 KB
	var big strings.Builder
	for i := 0; i < 3000; i++ {
		big.WriteString("line " + strings.Repeat("x", 9) + " " + pad(i) + "\n")
	}
	write("big.log", []byte(big.String()))
	if _, err := ReadContent(j, "/big.log", ContentOptions{MaxSize: 16 << 10}); err == nil {
		t.Fatal("large file accepted without tail")
	} else if fe := (*FileTooLargeError)(nil); !errors.As(err, &fe) {
		t.Fatalf("want FileTooLargeError, got %T", err)
	}
	c, err = ReadContent(j, "/big.log", ContentOptions{MaxSize: 16 << 10, Tail: 100})
	if err != nil {
		t.Fatal(err)
	}
	if !c.Truncated || !c.ReadOnly || c.TailLines != 100 || c.Lines != 100 {
		t.Fatalf("tail: %+v", c)
	}
	if !strings.HasPrefix(c.Content, "line xxxxxxxxx 2900\n") || !strings.HasSuffix(c.Content, "line xxxxxxxxx 2999\n") {
		t.Fatalf("tail window: %q…%q", c.Content[:40], c.Content[len(c.Content)-40:])
	}
	if c.Offset != int64(len(big.String())-100*20) {
		t.Fatalf("offset %d", c.Offset)
	}
	// more lines than the file has: whole file, offset 0
	c, err = ReadContent(j, "/big.log", ContentOptions{MaxSize: 16 << 10, Tail: 5000})
	if err != nil || c.Offset != 0 || c.Lines != 3000 {
		t.Fatalf("tail beyond file: %+v %v", c, err)
	}
	// a tail larger than TailMaxBytes is capped at a line boundary
	huge := strings.Repeat(strings.Repeat("y", 99)+"\n", 40000) // 4 MB
	write("huge.log", []byte(huge))
	c, err = ReadContent(j, "/huge.log", ContentOptions{MaxSize: 16 << 10, Tail: 100000})
	if err != nil || len(c.Content) > TailMaxBytes || !strings.HasPrefix(c.Content, "yyy") || c.Offset%100 != 0 {
		t.Fatalf("capped tail: len=%d offset=%d err=%v", len(c.Content), c.Offset, err)
	}
}

func pad(i int) string {
	s := "0000" + strconv.Itoa(i)
	return s[len(s)-4:]
}

func TestLanguageHint(t *testing.T) {
	for name, want := range map[string]string{
		"a.js": "javascript", "Dockerfile": "dockerfile", "Makefile": "makefile", ".env.local": "ini", "nginx.conf": "nginx",
		".htaccess": "apache", "x.blade.php": "php", "composer.json": "json", "notes.txt": "plaintext", "weird.xyz": "plaintext", "app.conf": "ini",
	} {
		if got := LanguageHint(name); got != want {
			t.Fatalf("%s: %s want %s", name, got, want)
		}
	}
}
