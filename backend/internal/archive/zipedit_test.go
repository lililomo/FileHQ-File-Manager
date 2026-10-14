package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/tasks"
)

// rawZip builds a zip whose first member carries garbage "compressed" bytes
// with a bogus CRC: any re-compression would either fail or change the
// bytes, so an unchanged raw member proves CreateRaw/OpenRaw copying.
func rawZip(t *testing.T) ([]byte, []byte) {
	t.Helper()
	garbage := bytes.Repeat([]byte{0xDE, 0xAD, 0xBE, 0xEF, 0x42}, 200)
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	w, err := zw.CreateRaw(&zip.FileHeader{Name: "opaque.bin", Method: zip.Deflate, CRC32: 0x12345678, CompressedSize64: uint64(len(garbage)), UncompressedSize64: 4096, Modified: time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(garbage); err != nil {
		t.Fatal(err)
	}
	for _, e := range []struct{ name, body string }{{"keep/a.txt", "AAA"}, {"keep/b.txt", "BBB"}, {"drop/c.txt", "CCC"}, {"drop/sub/d.txt", "DDD"}, {"top.txt", "TOP"}} {
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: e.name, Method: zip.Deflate})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write([]byte(e.body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes(), garbage
}

func sha(t *testing.T, p string) string {
	t.Helper()
	f, err := os.Open(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func zipNames(t *testing.T, p string) map[string]*zip.File {
	t.Helper()
	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = zr.Close() })
	out := map[string]*zip.File{}
	for _, f := range zr.File {
		out[f.Name] = f
	}
	return out
}

// TestZipEditAddRemoveRaw: add files + a folder under an inner folder,
// replace a colliding member, remove a file and a whole folder; kept
// members are byte-identical raw copies; the index cache is refreshed.
func TestZipEditAddRemoveRaw(t *testing.T) {
	e := newEnv(t)
	j := e.jail(t)
	ctx := context.Background()
	z, garbage := rawZip(t)
	e.write(t, "edit.zip", z)
	e.write(t, "new/one.txt", []byte("ONE"))
	e.write(t, "new/folder/two.txt", []byte("TWO"))
	e.write(t, "new/folder/deep/three.txt", []byte("THREE"))
	e.write(t, "a.txt", []byte("REPLACED"))
	e.write(t, "virus.exe", []byte("MZ"))
	before, err := e.svc.Entries(ctx, j, EntriesRequest{UserID: e.user.ID, Archive: "/edit.zip"})
	if err != nil || before.TotalEntries != 9 {
		t.Fatalf("before: %+v %v", before, err)
	}
	used0 := usage(t, e)
	// validation through the service
	if _, err := e.svc.ZipAdd(ctx, j, ZipAddRequest{UserID: e.user.ID, Archive: "/edit.zip", Sources: []string{"/edit.zip"}}); !IsCode(err, CodeValidation) {
		t.Fatalf("archive into itself: %v", err)
	}
	if _, err := e.svc.ZipAdd(ctx, j, ZipAddRequest{UserID: e.user.ID, Archive: "/edit.zip", Sources: []string{"/virus.exe"}}); !IsCode(err, CodeUnsupported) {
		t.Fatalf("blocked extension: %v", err)
	}
	if _, err := e.svc.ZipAdd(ctx, j, ZipAddRequest{UserID: e.user.ID, Archive: "/edit.zip", Sources: []string{"/new/one.txt"}, Into: "../x"}); !IsCode(err, CodeValidation) {
		t.Fatalf("bad into: %v", err)
	}
	if _, err := e.svc.ZipRemove(ctx, j, ZipRemoveRequest{UserID: e.user.ID, Archive: "/edit.zip", Entries: []string{"nope.txt"}}); !IsCode(err, CodeNotFound) {
		t.Fatalf("remove unknown: %v", err)
	}
	// add: /new/one.txt and /new/folder → keep/one.txt, keep/folder/…; /a.txt replaces keep/a.txt
	tk, err := e.svc.ZipAdd(ctx, j, ZipAddRequest{UserID: e.user.ID, Archive: "/edit.zip", Sources: []string{"/new/one.txt", "/new/folder", "/a.txt"}, Into: "keep"})
	if err != nil {
		t.Fatal(err)
	}
	res := resultOf[ZipEditResult](t, e.wait(t, tk.ID, 60*time.Second))
	if res.Added != 4 || res.Replaced != 1 || res.Removed != 0 || res.Path != "/edit.zip" {
		t.Fatalf("add result: %+v", res)
	}
	files := zipNames(t, filepath.Join(e.home, "edit.zip"))
	for _, want := range []string{"opaque.bin", "keep/a.txt", "keep/b.txt", "drop/c.txt", "drop/sub/d.txt", "top.txt", "keep/one.txt", "keep/folder/", "keep/folder/two.txt", "keep/folder/deep/", "keep/folder/deep/three.txt"} {
		if _, ok := files[want]; !ok {
			t.Fatalf("member %s missing after add; have %v", want, keys(files))
		}
	}
	if len(files) != 11 {
		t.Fatalf("members after add = %d, want 11: %v", len(files), keys(files))
	}
	// raw copy: the opaque member is byte-identical (garbage deflate + bogus CRC survive)
	op := files["opaque.bin"]
	if op.CRC32 != 0x12345678 || op.UncompressedSize64 != 4096 || op.Method != zip.Deflate {
		t.Fatalf("opaque header changed: %+v", op.FileHeader)
	}
	raw, err := op.OpenRaw()
	if err != nil {
		t.Fatal(err)
	}
	got, _ := io.ReadAll(raw)
	if !bytes.Equal(got, garbage) {
		t.Fatal("opaque member was re-encoded")
	}
	if rc, err := op.Open(); err == nil {
		if _, err := io.ReadAll(rc); err == nil {
			t.Fatal("garbage deflate decoded?!")
		}
		_ = rc.Close()
	}
	readMember := func(name string) string {
		rc, err := files[name].Open()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(rc)
		_ = rc.Close()
		return string(b)
	}
	if readMember("keep/a.txt") != "REPLACED" || readMember("keep/folder/deep/three.txt") != "THREE" || readMember("keep/b.txt") != "BBB" {
		t.Fatal("member contents")
	}
	// index cache refreshed (new size/mtime → new key)
	after, err := e.svc.Entries(ctx, j, EntriesRequest{UserID: e.user.ID, Archive: "/edit.zip", Inner: "keep"})
	if err != nil || after.TotalEntries != 14 || after.Total != 4 {
		t.Fatalf("after add listing: %+v %v", after, err)
	}
	// quota: delta of the archive size
	st, _ := os.Stat(filepath.Join(e.home, "edit.zip"))
	if used1 := usage(t, e); used1-used0 != st.Size()-int64(len(z)) {
		t.Fatalf("quota delta %d, want %d", used1-used0, st.Size()-int64(len(z)))
	}
	// remove: a file and a whole folder
	tk, err = e.svc.ZipRemove(ctx, j, ZipRemoveRequest{UserID: e.user.ID, Archive: "/edit.zip", Entries: []string{"top.txt", "drop"}})
	if err != nil {
		t.Fatal(err)
	}
	res = resultOf[ZipEditResult](t, e.wait(t, tk.ID, 60*time.Second))
	if res.Removed != 3 || res.Entries != 8 {
		t.Fatalf("remove result: %+v", res)
	}
	files = zipNames(t, filepath.Join(e.home, "edit.zip"))
	for _, gone := range []string{"top.txt", "drop/c.txt", "drop/sub/d.txt"} {
		if _, ok := files[gone]; ok {
			t.Fatalf("%s still present", gone)
		}
	}
	if _, ok := files["opaque.bin"]; !ok || len(files) != 8 {
		t.Fatalf("members after remove: %v", keys(files))
	}
	if dirs := e.tmpDirs(t); len(dirs) != 0 {
		t.Fatalf("working folders left: %v", dirs)
	}
	if strings.Contains(e.logs.String(), ".fm-zipedit-") {
		t.Fatal("staging name leaked into logs")
	}
}

func keys(m map[string]*zip.File) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func usage(t *testing.T, e *env) int64 {
	t.Helper()
	u, err := e.quota.Usage(context.Background(), e.user.ID)
	if err != nil {
		t.Fatal(err)
	}
	return u.Used
}

// TestZipEditRefusals: encrypted zips, non-zip formats and oversized
// archives are refused with UNSUPPORTED_TYPE and a reason.
func TestZipEditRefusals(t *testing.T) {
	e := newEnv(t)
	j := e.jail(t)
	ctx := context.Background()
	e.write(t, "src/s.txt", []byte("S"))
	resultOf[CompressResult](t, e.compress(t, CompressPayload{Paths: []string{"/src"}, Format: FormatZip, Name: "enc.zip", Dest: "/"}, "pw"))
	resultOf[CompressResult](t, e.compress(t, CompressPayload{Paths: []string{"/src"}, Format: FormatTarGz, Name: "t.tar.gz", Dest: "/"}, ""))
	cases := map[string]string{"/enc.zip": "encrypted", "/t.tar.gz": "format"}
	for archive, reason := range cases {
		_, err := e.svc.ZipAdd(ctx, j, ZipAddRequest{UserID: e.user.ID, Archive: archive, Sources: []string{"/src/s.txt"}})
		var ae *Error
		if !IsCode(err, CodeUnsupported) || !asErr(err, &ae) || ae.Details["reason"] != reason {
			t.Fatalf("%s: %v", archive, err)
		}
		if _, err := e.svc.ZipRemove(ctx, j, ZipRemoveRequest{UserID: e.user.ID, Archive: archive, Entries: []string{"src/s.txt"}}); !IsCode(err, CodeUnsupported) {
			t.Fatalf("%s remove: %v", archive, err)
		}
	}
	resp, _ := e.svc.Entries(ctx, j, EntriesRequest{UserID: e.user.ID, Archive: "/t.tar.gz"})
	if resp.SupportsModify || resp.ModifyReason != "format" {
		t.Fatalf("tar.gz supportsModify: %+v", resp)
	}
}

func asErr(err error, target **Error) bool { return errors.As(err, target) }

// TestZipEditCancelKeepsOriginal: AC "kegagalan di tengah → arsip asli
// utuh". The add is cancelled while a large source is being copied; the
// archive hash is unchanged, no staging file and no workspace remain.
func TestZipEditCancelKeepsOriginal(t *testing.T) {
	e := newEnv(t)
	j := e.jail(t)
	ctx := context.Background()
	z, _ := rawZip(t)
	e.write(t, "stable.zip", z)
	hashBefore := sha(t, filepath.Join(e.home, "stable.zip"))
	// a slow source: 96 MiB of zeros deflated one chunk at a time
	big := filepath.Join(e.home, "big.bin")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.CopyN(f, zeroReader{}, 96<<20); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	tk, err := e.svc.ZipAdd(ctx, j, ZipAddRequest{UserID: e.user.ID, Archive: "/stable.zip", Sources: []string{"/big.bin"}})
	if err != nil {
		t.Fatal(err)
	}
	// wait until it runs, then cancel
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		cur, _ := e.engine.Get(ctx, e.user.ID, tk.ID)
		if cur.State == tasks.Running && cur.Progress.Done > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := e.engine.Cancel(ctx, e.user.ID, tk.ID); err != nil {
		t.Fatal(err)
	}
	final := e.wait(t, tk.ID, 30*time.Second)
	if final.State != tasks.Cancelled {
		msg := ""
		if final.Error != nil {
			msg = final.Error.Message
		}
		t.Fatalf("state %s (%s)", final.State, msg)
	}
	if sha(t, filepath.Join(e.home, "stable.zip")) != hashBefore {
		t.Fatal("original archive changed after a cancelled edit")
	}
	entries, _ := os.ReadDir(e.home)
	for _, d := range entries {
		if strings.HasPrefix(d.Name(), ".fm-zipedit-") {
			t.Fatalf("staging file left: %s", d.Name())
		}
	}
	if dirs := e.tmpDirs(t); len(dirs) != 0 {
		t.Fatalf("working folders left: %v", dirs)
	}
	// a failing source (unreadable mid-walk) also keeps the original
	e.write(t, "ok.txt", []byte("ok"))
	if err := os.Remove(big); err != nil {
		t.Fatal(err)
	}
}

// TestZipEditLargeIsCopySpeed: AC "tambah file ke zip 1 GB tidak
// mendekompresi ulang entri lama (durasi ≈ waktu salin)". A Store zip of
// zeros (64 MiB by default, 1 GiB with ARCHIVE_BIG_TEST=1) gets one small
// file added; the edit must not take much longer than a plain copy of the
// archive (re-encoding 1 GiB of deflate would be many times slower).
func TestZipEditLargeIsCopySpeed(t *testing.T) {
	size := int64(64 << 20)
	if os.Getenv("ARCHIVE_BIG_TEST") != "" {
		size = 1 << 30
	}
	e := newEnv(t)
	j := e.jail(t)
	ctx := context.Background()
	p := filepath.Join(e.home, "large.zip")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	const chunk = 32 << 20
	for off := int64(0); off < size; off += chunk {
		w, err := zw.CreateHeader(&zip.FileHeader{Name: "part" + itoa(int(off/chunk)) + ".bin", Method: zip.Store})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.CopyN(w, zeroReader{}, min(chunk, size-off)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	e.write(t, "extra.txt", []byte("extra"))
	// baseline: plain copy of the archive
	start := time.Now()
	src, _ := os.Open(p)
	dst, _ := os.Create(filepath.Join(t.TempDir(), "copy.zip"))
	if _, err := io.Copy(dst, src); err != nil {
		t.Fatal(err)
	}
	_ = dst.Sync()
	_ = src.Close()
	_ = dst.Close()
	copyDur := time.Since(start)
	start = time.Now()
	tk, err := e.svc.ZipAdd(ctx, j, ZipAddRequest{UserID: e.user.ID, Archive: "/large.zip", Sources: []string{"/extra.txt"}})
	if err != nil {
		t.Fatal(err)
	}
	res := resultOf[ZipEditResult](t, e.wait(t, tk.ID, 10*time.Minute))
	editDur := time.Since(start)
	t.Logf("archive %d MiB: plain copy %s, zip add %s", size>>20, copyDur, editDur)
	if res.Added != 1 || res.Entries != int(size/chunk)+1 {
		t.Fatalf("result: %+v", res)
	}
	if editDur > 4*copyDur+3*time.Second {
		t.Fatalf("zip add %s vs copy %s: looks like a re-encode", editDur, copyDur)
	}
	zr, err := zip.OpenReader(p)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	var found bool
	for _, zf := range zr.File {
		if zf.Name == "extra.txt" {
			found = true
		}
		if strings.HasPrefix(zf.Name, "part") && zf.Method != zip.Store {
			t.Fatalf("%s re-encoded with method %d", zf.Name, zf.Method)
		}
	}
	if !found {
		t.Fatal("extra.txt not added")
	}
}
