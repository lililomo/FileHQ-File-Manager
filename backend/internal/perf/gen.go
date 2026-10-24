// Package perf holds the Step 21 performance fixture (SPEC §11.5) and, in
// its test files, the harness behind `make perf`: it starts the real binary
// on a scratch STORAGE_ROOT/DATA_DIR and measures listing latency, repeated
// navigation, parallel thumbnails, a 1 GiB upload, a 1 GiB zip stream and a
// 1 GiB extract against the resource targets of SPEC §1.6 / PRD §8.
//
// Everything is written through fsjail (no lint exception needed).
package perf

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math/rand/v2"
	"strconv"

	"kppbwi.app/filemanager/internal/fsjail"
)

// Spec sizes the fixture. Defaults are PROMPT Step 21 task 1.
type Spec struct {
	Files   int // files in /Besar (mixed names, types and sizes)
	Folders int // folders in /Besar, each with a few files
	Images  int // small PNGs in /Gambar (thumbnail throughput)
	Seed    uint64
}

// DefaultSpec is 10 000 files + 500 folders + 200 images.
var DefaultSpec = Spec{Files: 10_000, Folders: 500, Images: 200, Seed: 21}

// Stats reports what Generate wrote.
type Stats struct {
	Files, Folders, Images int
	Bytes                  int64
}

// Paths of the fixture inside the user's home.
const (
	BigDir   = "/Besar"
	ImageDir = "/Gambar"
)

const letters = "abcdefghijklmnopqrstuvwxyz"

var exts = []string{"txt", "jpg", "png", "go", "md", "pdf", "zip", "mp4", "mp3", "csv", "json", "docx", "xlsx", "tar.gz", "html", "svg"}

// name returns the i-th fixture file name: numbers of different widths,
// mixed case, Unicode (NFC), spaces, dotfiles and multi-dot names so natural
// sort and folding are exercised the way real folders are.
func name(i int, r *rand.Rand) string {
	ext := exts[r.IntN(len(exts))]
	switch {
	case i%97 == 0:
		return "Laporan Keuangan " + strconv.Itoa(i) + " (final)." + ext
	case i%89 == 0:
		return "ZETA_" + strconv.Itoa(i) + "." + ext
	case i%83 == 0:
		return "é-unicode-ñ-" + strconv.Itoa(i) + "." + ext
	case i%79 == 0:
		return "日本語ファイル" + strconv.Itoa(i) + "." + ext
	case i%101 == 0:
		return ".hidden-" + strconv.Itoa(i)
	case i%7 == 0:
		return "IMG_" + fmt.Sprintf("%04d", i) + "." + ext
	case i%5 == 0:
		return "file" + strconv.Itoa(i) + ".v" + strconv.Itoa(i%3+1) + "." + ext
	}
	return "file" + strconv.Itoa(i) + "." + ext
}

// Generate writes the fixture under the folder home (a user's home inside
// STORAGE_ROOT). It is deterministic for a given Spec.Seed.
func Generate(home string, spec Spec) (Stats, error) {
	var st Stats
	j, err := fsjail.Open(home)
	if err != nil {
		return st, err
	}
	defer func() { _ = j.Close() }()
	r := rand.New(rand.NewPCG(spec.Seed, spec.Seed^0x9e3779b97f4a7c15)) //nolint:gosec // fixture data, not security
	for _, d := range []string{BigDir, ImageDir} {
		if err := j.Mkdir(fsjail.VPath(d)); err != nil {
			return st, fmt.Errorf("perf: mkdir %s: %w", d, err)
		}
	}
	buf := make([]byte, 1<<15)
	for i := 1; i <= spec.Files; i++ {
		size := r.IntN(4096)
		if i%50 == 0 {
			size = 1 << 15
		}
		for k := range buf[:size] {
			buf[k] = letters[r.IntN(len(letters))]
		}
		if err := writeFile(j, fsjail.VPath(BigDir+"/"+name(i, r)), buf[:size]); err != nil {
			return st, err
		}
		st.Files++
		st.Bytes += int64(size)
	}
	for i := 1; i <= spec.Folders; i++ {
		dir := fsjail.VPath(BigDir + "/folder-" + strconv.Itoa(i))
		if err := j.Mkdir(dir); err != nil {
			return st, fmt.Errorf("perf: mkdir %s: %w", dir, err)
		}
		for k := range i % 4 {
			if err := writeFile(j, fsjail.VPath(string(dir)+"/catatan-"+strconv.Itoa(k)+".txt"), []byte("isi "+strconv.Itoa(i))); err != nil {
				return st, err
			}
		}
		st.Folders++
	}
	for i := 1; i <= spec.Images; i++ {
		var b bytes.Buffer
		if err := png.Encode(&b, gradient(64+r.IntN(256), 64+r.IntN(256), r)); err != nil {
			return st, err
		}
		if err := writeFile(j, fsjail.VPath(fmt.Sprintf("%s/foto-%03d.png", ImageDir, i)), b.Bytes()); err != nil {
			return st, err
		}
		st.Images++
		st.Bytes += int64(b.Len())
	}
	return st, nil
}

func gradient(w, h int, r *rand.Rand) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	c0 := color.NRGBA{uint8(r.IntN(256)), uint8(r.IntN(256)), uint8(r.IntN(256)), 255} //nolint:gosec // < 256
	for y := range h {
		for x := range w {
			img.SetNRGBA(x, y, color.NRGBA{c0.R + uint8(x), c0.G + uint8(y), c0.B ^ uint8(x*y), 255}) //nolint:gosec // wraps on purpose
		}
	}
	return img
}

func writeFile(j *fsjail.Jail, p fsjail.VPath, data []byte) error {
	f, err := j.Create(p, true)
	if err != nil {
		return fmt.Errorf("perf: create %s: %w", p, err)
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return fmt.Errorf("perf: write %s: %w", p, err)
	}
	return f.Close()
}

// Stream returns size pseudo-random (incompressible) bytes without holding
// them in memory: the source of the 1 GiB upload and archive fixtures.
func Stream(size int64, seed uint64) io.Reader {
	return io.LimitReader(&prng{r: rand.New(rand.NewPCG(seed, ^seed))}, size) //nolint:gosec // fixture data
}

type prng struct{ r *rand.Rand }

func (p *prng) Read(b []byte) (int, error) {
	n := len(b) &^ 7
	for i := 0; i < n; i += 8 {
		binary.LittleEndian.PutUint64(b[i:], p.r.Uint64())
	}
	if n == 0 && len(b) > 0 {
		var t [8]byte
		binary.LittleEndian.PutUint64(t[:], p.r.Uint64())
		b[0] = t[0]
		return 1, nil
	}
	return n, nil
}

// WriteStoreZip writes a zip (method Store) of `parts` members holding
// total bytes of random data to p inside home: the 1 GiB extract fixture.
func WriteStoreZip(home string, p fsjail.VPath, total int64, parts int) error {
	j, err := fsjail.Open(home)
	if err != nil {
		return err
	}
	defer func() { _ = j.Close() }()
	f, err := j.Create(p, true)
	if err != nil {
		return err
	}
	zw := zip.NewWriter(f)
	per := total / int64(parts)
	for i := range parts {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("bagian-%02d.bin", i+1), Method: zip.Store})
		if err != nil {
			_ = f.Close()
			return err
		}
		if _, err := io.CopyBuffer(w, Stream(per, uint64(i)+100), make([]byte, 1<<20)); err != nil { //nolint:gosec // small index
			_ = f.Close()
			return err
		}
	}
	if err := zw.Close(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}
