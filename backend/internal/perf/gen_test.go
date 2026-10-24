package perf

import (
	"archive/zip"
	"bytes"
	"compress/gzip"
	"io"
	"path/filepath"
	"testing"

	"kppbwi.app/filemanager/internal/fsjail"
)

func TestGenerateSmall(t *testing.T) {
	home := t.TempDir()
	spec := Spec{Files: 400, Folders: 12, Images: 5, Seed: 3}
	st, err := Generate(home, spec)
	if err != nil {
		t.Fatal(err)
	}
	if st.Files != 400 || st.Folders != 12 || st.Images != 5 || st.Bytes == 0 {
		t.Fatalf("stats %+v", st)
	}
	j, err := fsjail.Open(home)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = j.Close() }()
	big, err := j.ReadDir(BigDir)
	if err != nil {
		t.Fatal(err)
	}
	dirs, unicode := 0, 0
	for _, e := range big {
		if e.IsDir {
			dirs++
		}
		if !isASCII(e.Name) {
			unicode++
		}
	}
	if len(big) != 412 || dirs != 12 || unicode == 0 {
		t.Fatalf("Besar: %d entries, %d dirs, %d unicode names", len(big), dirs, unicode)
	}
	imgs, err := j.ReadDir(ImageDir)
	if err != nil || len(imgs) != 5 {
		t.Fatalf("Gambar: %d %v", len(imgs), err)
	}
	// deterministic for a seed
	home2 := t.TempDir()
	if _, err := Generate(home2, spec); err != nil {
		t.Fatal(err)
	}
	j2, _ := fsjail.Open(home2)
	defer func() { _ = j2.Close() }()
	again, _ := j2.ReadDir(BigDir)
	names := map[string]int64{}
	for _, e := range big {
		names[e.Name] = e.Size
	}
	for _, e := range again {
		if size, ok := names[e.Name]; !ok || size != e.Size {
			t.Fatalf("not deterministic: %s", e.Name)
		}
	}
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func TestStreamIsIncompressible(t *testing.T) {
	data, err := io.ReadAll(Stream(1<<20+3, 9))
	if err != nil || len(data) != 1<<20+3 {
		t.Fatalf("len %d err %v", len(data), err)
	}
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	_, _ = zw.Write(data)
	_ = zw.Close()
	if b.Len() < len(data) {
		t.Fatalf("stream compresses to %d of %d bytes", b.Len(), len(data))
	}
}

func TestWriteStoreZip(t *testing.T) {
	home := t.TempDir()
	if err := WriteStoreZip(home, "/a.zip", 3<<20, 3); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.OpenReader(filepath.Join(home, "a.zip"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	if len(zr.File) != 3 || zr.File[0].Method != zip.Store || zr.File[0].UncompressedSize64 != 1<<20 {
		t.Fatalf("zip %d members, method %d, size %d", len(zr.File), zr.File[0].Method, zr.File[0].UncompressedSize64)
	}
}
