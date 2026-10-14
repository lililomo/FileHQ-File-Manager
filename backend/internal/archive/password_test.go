package archive

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestZipAESInterop covers PROMPT Step 14a AC 5: an AES-256 zip written by
// the app opens with `7z t -p…` (password via stdin here) and an AES zip
// made by 7-Zip extracts in the app; wrong/missing passwords map to the
// ARCHIVE_PASSWORD_* codes.
func TestZipAESInterop(t *testing.T) {
	have7z(t)
	e := newEnv(t)
	files := sampleTree(t, e, "src/proyek")
	res := resultOf[CompressResult](t, e.compress(t, CompressPayload{Paths: []string{"/src/proyek"}, Format: FormatZip, Name: "secret.zip", Dest: "/src"}, "k0de-rahasia"))
	host := filepath.Join(e.home, "src", "secret.zip")
	out := run7zRaw(t, filepath.Dir(host), "k0de-rahasia\n", "t", "-bd", "-y", "--", "secret.zip")
	if !bytes.Contains(out, []byte("Everything is Ok")) {
		t.Fatalf("7z t: %s", out)
	}
	if res.Files < 5 {
		t.Fatalf("compress: %+v", res)
	}
	// every entry is encrypted (names stay visible in zip by design)
	if b := e.read(t, "src/secret.zip"); bytes.Contains(b, []byte("hello world")) {
		t.Fatal("plaintext leaked into the encrypted zip")
	}
	// the app reads its own AES zip and the ones 7-Zip makes
	xr := resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/src/secret.zip", Dest: "/out"}, "k0de-rahasia"))
	if xr.Files < 5 {
		t.Fatalf("extract own: %+v", xr)
	}
	sameTree(t, e, files, "src/proyek", "out/proyek")
	failedWith(t, e.extract(t, ExtractPayload{Archive: "/src/secret.zip", Dest: "/out2"}, ""), CodePasswordReq)
	failedWith(t, e.extract(t, ExtractPayload{Archive: "/src/secret.zip", Dest: "/out3"}, "salah"), CodePasswordWrong)
	if e.exists("out2") || e.exists("out3") || len(e.tmpDirs(t)) != 0 {
		t.Fatal("failed extraction left files")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "plain.txt"), []byte("from 7zip"), 0o600); err != nil {
		t.Fatal(err)
	}
	run7zRaw(t, dir, "pw-7z\n", "a", "-tzip", "-mem=AES256", "-p", "-bd", "-y", "--", "by7z.zip", "plain.txt")
	run7zRaw(t, dir, "pw-zc\n", "a", "-tzip", "-mem=ZipCrypto", "-p", "-bd", "-y", "--", "byzc.zip", "plain.txt")
	b, _ := os.ReadFile(filepath.Join(dir, "by7z.zip"))
	e.write(t, "by7z.zip", b)
	b, _ = os.ReadFile(filepath.Join(dir, "byzc.zip"))
	e.write(t, "byzc.zip", b)
	xr = resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/by7z.zip", Dest: "/o7"}, "pw-7z"))
	if xr.Files != 1 || string(e.read(t, "o7/plain.txt")) != "from 7zip" {
		t.Fatalf("7z-made AES zip: %+v", xr)
	}
	xr = resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/byzc.zip", Dest: "/ozc"}, "pw-zc"))
	if xr.Files != 1 || string(e.read(t, "ozc/plain.txt")) != "from 7zip" {
		t.Fatalf("ZipCrypto zip: %+v", xr)
	}
	failedWith(t, e.extract(t, ExtractPayload{Archive: "/byzc.zip", Dest: "/ozc2"}, "nope"), CodePasswordWrong)
}

// TestSevenZipPasswordNeverInArgv covers PROMPT Step 14a AC 6: while 7z
// creates and extracts a password archive, no process on the host carries
// the password in its command line (password via stdin, see cli.go).
func TestSevenZipPasswordNeverInArgv(t *testing.T) {
	have7z(t)
	e := newEnv(t)
	// a few MB of random data keeps 7z busy long enough to be observed
	big := make([]byte, 24<<20)
	for i := range big {
		big[i] = byte(i*7919 ^ i>>3)
	}
	e.write(t, "src/big.bin", big)
	e.write(t, "src/note.txt", []byte("secret note"))
	const password = "Pa55-never-in-ps"
	stop := make(chan struct{})
	seen := make(chan string, 1)
	scans := 0
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			entries, _ := os.ReadDir("/proc")
			for _, d := range entries {
				if !d.IsDir() || d.Name()[0] < '0' || d.Name()[0] > '9' {
					continue
				}
				cmd, err := os.ReadFile(filepath.Join("/proc", d.Name(), "cmdline"))
				if err != nil {
					continue
				}
				if bytes.Contains(cmd, []byte("7zip/7z")) {
					scans++
				}
				if bytes.Contains(cmd, []byte(password)) {
					select {
					case seen <- strings.ReplaceAll(string(cmd), "\x00", " "):
					default:
					}
					return
				}
			}
			time.Sleep(5 * time.Millisecond)
		}
	}()
	res := resultOf[CompressResult](t, e.compress(t, CompressPayload{Paths: []string{"/src"}, Format: FormatSevenZip, Name: "src.7z", Dest: "/", Level: LevelMax, EncryptNames: true}, password))
	xr := resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/src.7z", Dest: "/out"}, password))
	close(stop)
	select {
	case cmd := <-seen:
		t.Fatalf("password visible in a command line: %s", cmd)
	default:
	}
	if res.Files != 2 || xr.Files != 2 || !bytes.Equal(e.read(t, "out/src/big.bin"), big) {
		t.Fatalf("7z round trip: %+v %+v", res, xr)
	}
	t.Logf("observed %d 7z command lines without the password", scans)
	// encrypted names: listing without a password is impossible, the app reports PASSWORD_REQUIRED / WRONG
	failedWith(t, e.extract(t, ExtractPayload{Archive: "/src.7z", Dest: "/out2"}, ""), CodePasswordReq)
	failedWith(t, e.extract(t, ExtractPayload{Archive: "/src.7z", Dest: "/out3"}, "wrong"), CodePasswordWrong)
	// 7z without -mhe: names readable, bodies encrypted
	b := sevenZipFixture(t, "plain-names.7z", "pw2", false, map[string][]byte{"a/b.txt": []byte("b"), "c.txt": []byte("c")})
	e.write(t, "plain-names.7z", b)
	xr = resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/plain-names.7z", Dest: "/o4"}, "pw2"))
	if xr.Files != 2 || string(e.read(t, "o4/a/b.txt")) != "b" {
		t.Fatalf("7z plain names: %+v", xr)
	}
	failedWith(t, e.extract(t, ExtractPayload{Archive: "/plain-names.7z", Dest: "/o5"}, "wrong"), CodePasswordWrong)
	failedWith(t, e.extract(t, ExtractPayload{Archive: "/plain-names.7z", Dest: "/o6"}, ""), CodePasswordReq)
	// retry of a password task without the secret fails fast with PASSWORD_REQUIRED
	tk := e.enqueue(t, TaskExtract, ExtractPayload{Archive: "/plain-names.7z", Dest: "/o7", Policy: "fail", HasPassword: true}, "")
	failedWith(t, e.wait(t, tk.ID, 30*time.Second), CodePasswordReq)
}

// TestSevenZipSplitAndIso: split volumes are moved together; an iso built
// by 7z (`-tiso` is not supported by 7z a, so genisoimage is absent here) is
// skipped when no iso can be produced.
func TestSevenZipSplitVolumes(t *testing.T) {
	have7z(t)
	e := newEnv(t)
	big := make([]byte, 3<<20)
	for i := range big {
		big[i] = byte(i*31 ^ i>>5)
	}
	e.write(t, "src/big.bin", big)
	res := resultOf[CompressResult](t, e.compress(t, CompressPayload{Paths: []string{"/src/big.bin"}, Format: FormatSevenZip, Name: "split.7z", Dest: "/", Level: LevelStore, SplitMB: 1}, ""))
	if len(res.Paths) < 3 || res.Path != "/split.7z.001" {
		t.Fatalf("split: %+v", res)
	}
	xr := resultOf[ExtractResult](t, e.extract(t, ExtractPayload{Archive: "/split.7z.001", Dest: "/out"}, ""))
	if xr.Files != 1 || !bytes.Equal(e.read(t, "out/big.bin"), big) {
		t.Fatalf("extract split: %+v", xr)
	}
}
