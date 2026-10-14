package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/flate"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// Fixtures of SPEC §11.2 / PROMPT Step 14a task 7 are generated here (no
// opaque binaries in the repo).

type zipEntry struct {
	name    string
	content []byte
	mode    os.FileMode
	symlink string
	method  uint16
	rawName bool // no UTF-8 flag (legacy encoding)
}

func buildZip(t *testing.T, entries []zipEntry) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	for _, e := range entries {
		fh := &zip.FileHeader{Name: e.name, Method: e.method, Modified: time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC)}
		if e.method == 0 && len(e.content) > 64 {
			fh.Method = zip.Deflate
		}
		switch {
		case e.symlink != "":
			fh.SetMode(0o777 | os.ModeSymlink)
		case e.mode != 0:
			fh.SetMode(e.mode)
		default:
			fh.SetMode(0o644)
		}
		if e.rawName {
			fh.NonUTF8 = true
		}
		w, err := zw.CreateHeader(fh)
		if err != nil {
			t.Fatal(err)
		}
		body := e.content
		if e.symlink != "" {
			body = []byte(e.symlink)
		}
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

type tarEntry struct {
	name     string
	content  []byte
	typeflag byte
	link     string
	mode     int64
}

func buildTar(t *testing.T, entries []tarEntry) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	tw := tar.NewWriter(buf)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: e.mode, ModTime: time.Date(2024, 5, 6, 7, 8, 9, 0, time.UTC), Typeflag: e.typeflag, Linkname: e.link, Size: int64(len(e.content))}
		if hdr.Mode == 0 {
			hdr.Mode = 0o644
		}
		if hdr.Typeflag == 0 {
			hdr.Typeflag = tar.TypeReg
		}
		if hdr.Typeflag != tar.TypeReg {
			hdr.Size = 0
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag == tar.TypeReg {
			if _, err := tw.Write(e.content); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// bombZip is one entry of n zero bytes (deflate ~1000:1).
func bombZip(t *testing.T, n int64) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) { return flate.NewWriter(w, flate.BestCompression) })
	w, err := zw.CreateHeader(&zip.FileHeader{Name: "zeros.bin", Method: zip.Deflate})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(w, zeroReader{}, n); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func manyEntriesZip(t *testing.T, n int) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	for i := 0; i < n; i++ {
		if _, err := zw.CreateHeader(&zip.FileHeader{Name: "e" + itoa(i) + ".txt", Method: zip.Store}); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func gbkZip(t *testing.T) []byte {
	t.Helper()
	enc := simplifiedchinese.GBK.NewEncoder()
	name, err := enc.String("测试文档.txt")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := enc.String("资料/")
	if err != nil {
		t.Fatal(err)
	}
	return buildZip(t, []zipEntry{{name: dir, rawName: true}, {name: dir + name, content: []byte("ni hao"), rawName: true}})
}

// run7zRaw drives the CLI directly for fixtures (tests only).
func run7zRaw(t *testing.T, dir, stdin string, args ...string) []byte {
	t.Helper()
	cmd := exec.Command(sevenZipBin, args...)
	cmd.Dir = dir
	if stdin != "" {
		cmd.Stdin = bytes.NewBufferString(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("7z %v: %v\n%s", args, err, out)
	}
	return out
}

// sevenZipFixture builds <dir>/<name> from files with an optional password
// (-mhe=on when encryptNames).
func sevenZipFixture(t *testing.T, name, password string, encryptNames bool, files map[string][]byte) []byte {
	t.Helper()
	have7z(t)
	dir := t.TempDir()
	args := []string{"a", "-t7z", "-bd", "-y"}
	if password != "" {
		args = append(args, "-p")
		if encryptNames {
			args = append(args, "-mhe=on")
		}
	}
	args = append(args, "--", name)
	for n, c := range files {
		p := filepath.Join(dir, filepath.FromSlash(n))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, c, 0o640); err != nil {
			t.Fatal(err)
		}
		args = append(args, n)
	}
	stdin := ""
	if password != "" {
		stdin = password + "\n"
	}
	run7zRaw(t, dir, stdin, args...)
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}
