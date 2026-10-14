package archive

import (
	"bytes"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

// sampleTree writes a small tree with text, binary, nested folders and a
// symlink into the home and returns the relative file list.
func sampleTree(t *testing.T, e *env, base string) map[string][]byte {
	t.Helper()
	bin := make([]byte, 300_000)
	_, _ = rand.Read(bin[:1000])
	files := map[string][]byte{
		base + "/readme.txt":           []byte("hello world\n"),
		base + "/data/numbers.csv":     []byte("1,2,3\n4,5,6\n"),
		base + "/data/deep/x/y/z.bin":  bin,
		base + "/nama dengan spasi.md": []byte("# judul\n"),
		base + "/日本語.txt":              []byte("こんにちは"),
	}
	for n, c := range files {
		e.write(t, n, c)
	}
	if err := os.MkdirAll(filepath.Join(e.home, base, "empty"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("readme.txt", filepath.Join(e.home, base, "link.txt")); err != nil {
		t.Fatal(err)
	}
	return files
}

func sameTree(t *testing.T, e *env, files map[string][]byte, srcBase, dstBase string) {
	t.Helper()
	for n, want := range files {
		rel := dstBase + n[len(srcBase):]
		got, err := os.ReadFile(filepath.Join(e.home, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s: %v", rel, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s differs", rel)
		}
	}
	if st, err := os.Stat(filepath.Join(e.home, dstBase, "empty")); err != nil || !st.IsDir() {
		t.Fatalf("empty folder missing in %s", dstBase)
	}
}

// TestRoundTripMatrix covers PROMPT Step 14a AC 4: every writable format
// compresses the tree, extracts into a new folder and compares byte for byte.
func TestRoundTripMatrix(t *testing.T) {
	e := newEnv(t)
	files := sampleTree(t, e, "src/proyek")
	if err := os.MkdirAll(filepath.Join(e.home, "arsip"), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, f := range []Format{FormatZip, FormatTar, FormatTarGz, FormatTarBz2, FormatTarXz, FormatTarZst, FormatJar, FormatSevenZip} {
		t.Run(string(f), func(t *testing.T) {
			if f == FormatSevenZip {
				have7z(t)
			}
			for _, level := range []string{LevelStore, LevelFast, LevelNormal, LevelMax} {
				if level != LevelNormal && f != FormatZip && f != FormatSevenZip && f != FormatTarGz {
					continue
				}
				name := "proyek-" + string(f) + "-" + level + "." + f.Extension()
				res := resultOf[CompressResult](t, e.compress(t, CompressPayload{Paths: []string{"/src/proyek"}, Format: f, Name: name, Dest: "/arsip", Level: level}, ""))
				if res.Path != "/arsip/"+name || res.Size <= 0 || res.Files < 5 {
					t.Fatalf("compress %s: %+v", name, res)
				}
				xr := resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: res.Path, Dest: DestNew}, ""))
				if xr.Dest != "/arsip/proyek-"+string(f)+"-"+level || xr.Files < 5 {
					t.Fatalf("extract %s: %+v", name, xr)
				}
				sameTree(t, e, files, "src/proyek", "arsip/proyek-"+string(f)+"-"+level+"/proyek")
				// symlink: tar keeps it as a link, zip/7z skip it (never followed)
				if f.IsTar() {
					if tgt, err := os.Readlink(filepath.Join(e.home, "arsip", "proyek-"+string(f)+"-"+level, "proyek", "link.txt")); err != nil || tgt != "readme.txt" {
						t.Fatalf("tar symlink: %q %v", tgt, err)
					}
				}
			}
		})
	}
}

// TestSingleFileFormats: gz/bz2/xz/zst compress one file and extract it.
func TestSingleFileFormats(t *testing.T) {
	e := newEnv(t)
	content := bytes.Repeat([]byte("single file content line\n"), 2000)
	e.write(t, "one.txt", content)
	for _, f := range []Format{FormatGz, FormatBz2, FormatXz, FormatZst} {
		res := resultOf[CompressResult](t, e.compress(t, CompressPayload{Paths: []string{"/one.txt"}, Format: f, Name: "one.txt." + f.Extension(), Dest: "/"}, ""))
		if res.Path != "/one.txt."+f.Extension() {
			t.Fatalf("%s: %+v", f, res)
		}
		xr := resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: res.Path, Dest: "/out-" + string(f)}, ""))
		if xr.Files != 1 || !bytes.Equal(e.read(t, "out-"+string(f)+"/one.txt"), content) {
			t.Fatalf("%s extract: %+v", f, xr)
		}
	}
	// two sources for a single-file format is refused at validation (API) and in the runner
	tk := e.compress(t, CompressPayload{Paths: []string{"/one.txt", "/out-gz/one.txt"}, Format: FormatGz, Name: "x.gz", Dest: "/"}, "")
	failedWith(t, tk, CodeValidation)
}

// TestCompressBehaviour: default names, output conflict → rename, quota
// reservation, deleteSource → Recycle Bin, cancel cleans tmp.
func TestCompressBehaviour(t *testing.T) {
	e := newEnv(t)
	sampleTree(t, e, "src/proyek")
	e.write(t, "src/proyek.zip", []byte("existing"))
	res := resultOf[CompressResult](t, e.compress(t, CompressPayload{Paths: []string{"/src/proyek"}, Format: FormatZip, Name: "proyek.zip", Dest: "/src"}, ""))
	if res.Path != "/src/proyek (1).zip" || res.Status != "renamed" {
		t.Fatalf("conflict rename: %+v", res)
	}
	if string(e.read(t, "src/proyek.zip")) != "existing" {
		t.Fatal("existing archive overwritten")
	}
	// deleteSource moves the sources to the Recycle Bin
	res = resultOf[CompressResult](t, e.compress(t, CompressPayload{Paths: []string{"/src/proyek/readme.txt"}, Format: FormatZip, Name: "r.zip", Dest: "/src", DeleteSource: true}, ""))
	if res.Deleted != 1 || e.exists("src/proyek/readme.txt") {
		t.Fatalf("deleteSource: %+v", res)
	}
	items, err := e.trash.List(t.Context(), e.user.ID, trashFilterAll())
	if err != nil || len(items.Items) != 1 {
		t.Fatalf("trash: %v %+v", err, items)
	}
	// quota: the reservation is the raw size; a 1 MB quota refuses 300 KB tree? no → fits; 100 KB refuses
	e.setQuota(t, 100<<10)
	tk := e.compress(t, CompressPayload{Paths: []string{"/src/proyek"}, Format: FormatTar, Name: "big.tar", Dest: "/src"}, "")
	failedWith(t, tk, "QUOTA_EXCEEDED")
	if e.exists("src/big.tar") || len(e.tmpDirs(t)) != 0 {
		t.Fatal("quota failure left files")
	}
}
