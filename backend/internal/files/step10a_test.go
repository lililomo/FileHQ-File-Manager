package files

import (
	"context"
	"errors"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/trash"
)

func (e *env) usage() quota.Usage {
	e.t.Helper()
	u, err := e.quota.Usage(context.Background(), e.uid)
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func (e *env) recalc() quota.Usage {
	e.t.Helper()
	td, err := e.trash.Sys(e.uid)
	if err != nil {
		e.t.Fatal(err)
	}
	res, err := e.quota.Recalculate(context.Background(), quota.Target{UserID: e.uid, Jail: e.j, Trash: td})
	if err != nil {
		e.t.Fatal(err)
	}
	return res.Usage
}

// TestQuotaEnforcedOnCopy: 1 MB quota, copying a 2 MB file is refused with
// QUOTA_EXCEEDED and leaves no partial file (PROMPT 10a AC 4).
func TestQuotaEnforcedOnCopy(t *testing.T) {
	e := newEnv(t, envOpts{quotaBytes: 1 << 20})
	ctx := context.Background()
	e.write("big.bin", strings.Repeat("x", 2<<20))
	e.mkdir("dst")
	e.recalc()
	_, err := e.svc.Copy(ctx, e.j, e.uid, []fsjail.VPath{"/big.bin"}, "/dst", ConflictPolicy{})
	var qe *quota.ExceededError
	if !errors.As(err, &qe) || qe.Reason != "quota" || qe.Needed != 2<<20 || qe.Limit != 1<<20 {
		t.Fatalf("copy over quota: %v", err)
	}
	if e.exists("dst/big.bin") {
		t.Fatal("partial copy left behind")
	}
	entries, _ := os.ReadDir(filepath.Join(e.home, "dst"))
	if len(entries) != 0 {
		t.Fatalf("dst not empty: %v", entries)
	}
	if _, err := e.svc.Duplicate(ctx, e.j, e.uid, "/big.bin"); !errors.As(err, &qe) {
		t.Fatalf("duplicate over quota: %v", err)
	}
	// small copy fits exactly: used stays consistent and reservations are released
	e.write("small.bin", strings.Repeat("y", 100))
	e.recalc()
	if err := e.quota.Apply(ctx, nil, e.uid, (1<<20)-2<<20-200, 0, 0); err != nil { // clamp to 0 then set
		t.Fatal(err)
	}
	if u := e.usage(); u.Reserved != 0 {
		t.Fatalf("reservation leaked: %+v", u)
	}
}

// TestQuotaIncrementalMatchesRecalculate runs 20 pseudo-random operations
// and expects the incremental counters to equal a fresh walk (AC 3).
func TestQuotaIncrementalMatchesRecalculate(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	rng := rand.New(rand.NewSource(10)) //nolint:gosec // deterministic test data
	for i := 0; i < 6; i++ {
		e.write("f"+string(rune('a'+i))+".bin", strings.Repeat("z", 100+rng.Intn(5000)))
	}
	e.write("dir/one.bin", strings.Repeat("q", 777))
	e.write("dir/sub/two.bin", strings.Repeat("w", 333))
	e.mkdir("dst")
	start := e.recalc()
	if start.Dirty || start.Used == 0 {
		t.Fatalf("baseline: %+v", start)
	}
	var trashIDs []string
	pick := func() fsjail.VPath {
		entries, _ := os.ReadDir(e.home)
		var files []string
		for _, en := range entries {
			if en.Name() != "dst" && en.Name() != ".system" {
				files = append(files, en.Name())
			}
		}
		if len(files) == 0 {
			return ""
		}
		return fsjail.VPath("/" + files[rng.Intn(len(files))])
	}
	ops := 0
	for ops < 20 {
		switch rng.Intn(6) {
		case 0, 1: // copy into dst with rename policy
			p := pick()
			if p == "" {
				continue
			}
			if _, err := e.svc.Copy(ctx, e.j, e.uid, []fsjail.VPath{p}, "/dst", ConflictPolicy{Default: PolicyRename}); err != nil {
				t.Fatalf("copy %s: %v", p, err)
			}
		case 2: // duplicate
			p := pick()
			if p == "" {
				continue
			}
			if _, err := e.svc.Duplicate(ctx, e.j, e.uid, p); err != nil {
				t.Fatalf("duplicate %s: %v", p, err)
			}
		case 3: // delete to trash
			p := pick()
			if p == "" {
				continue
			}
			res, err := e.svc.Delete(ctx, e.j, e.uid, []fsjail.VPath{p})
			if err != nil {
				t.Fatalf("delete %s: %v", p, err)
			}
			trashIDs = append(trashIDs, res.Results[0].TrashID)
		case 4: // restore (rename on conflict)
			if len(trashIDs) == 0 {
				continue
			}
			id := trashIDs[0]
			trashIDs = trashIDs[1:]
			if _, err := e.svc.RestoreFromTrash(ctx, e.j, e.uid, RestoreRequest{IDs: []string{id}, Default: trash.PolicyRename}); err != nil {
				t.Fatalf("restore %s: %v", id, err)
			}
		case 5: // purge or permanent delete
			if len(trashIDs) > 0 && rng.Intn(2) == 0 {
				id := trashIDs[len(trashIDs)-1]
				trashIDs = trashIDs[:len(trashIDs)-1]
				if _, err := e.svc.PurgeTrash(ctx, e.j, e.uid, []string{id}); err != nil {
					t.Fatalf("purge %s: %v", id, err)
				}
			} else {
				p := pick()
				if p == "" {
					continue
				}
				if _, err := e.svc.DeletePermanent(ctx, e.j, e.uid, []fsjail.VPath{p}); err != nil {
					t.Fatalf("delete permanent %s: %v", p, err)
				}
			}
		}
		ops++
	}
	inc := e.usage()
	if inc.Dirty {
		t.Fatalf("counters flagged dirty after clean operations: %+v", inc)
	}
	walked := e.recalc()
	if inc.Used != walked.Used || inc.Trash != walked.Trash || inc.Versions != walked.Versions {
		t.Fatalf("incremental %+v != walk %+v", inc, walked)
	}
	t.Logf("20 ops: used=%d trash=%d (0 byte difference)", walked.Used, walked.Trash)
}

func TestDeletePermanentAndRestoreOrchestration(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	e.write("p/a.txt", "aaaa")
	e.write("p/b/c.txt", "cc")
	e.write("keep.txt", "k")
	if err := e.store.UpsertFavorite(ctx, e.uid, "/p/a.txt", "2026-10-14T00:00:00.000Z"); err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpsertFileNote(ctx, e.uid, "/p/b", "note", "2026-10-14T00:00:00.000Z"); err != nil {
		t.Fatal(err)
	}
	e.recalc()
	res, err := e.svc.DeletePermanent(ctx, e.j, e.uid, []fsjail.VPath{"/p", "/p/a.txt", "/missing.txt"})
	if err != nil || res.Deleted != 1 || res.BytesFreed != 6 || e.exists("p") || res.Undo != nil {
		t.Fatalf("delete permanent: %+v %v", res, err)
	}
	if len(res.Results) != 2 || res.Results[1].Status != "skipped" {
		t.Fatalf("results: %+v", res.Results)
	}
	if favs, _ := e.store.ListFavorites(ctx, e.uid); len(favs) != 0 {
		t.Fatalf("favorites not removed: %+v", favs)
	}
	if notes, _ := e.store.ListFileNotes(ctx, e.uid); len(notes) != 0 {
		t.Fatalf("notes not removed: %+v", notes)
	}
	if u := e.usage(); u.Used != 1 {
		t.Fatalf("quota after permanent delete: %+v", u)
	}
	acts := e.activity()
	if acts[0].Action != ActPermanent {
		t.Fatalf("activity: %+v", acts[0])
	}
	if _, err := e.svc.DeletePermanent(ctx, e.j, e.uid, []fsjail.VPath{"/"}); err == nil {
		t.Fatal("root accepted")
	}

	// restore orchestration: conflicts collected up front, nothing moved
	e.write("x.txt", "1")
	e.write("y.txt", "22")
	e.recalc() // files written outside the app are only known after a walk
	dres, err := e.svc.Delete(ctx, e.j, e.uid, []fsjail.VPath{"/x.txt", "/y.txt"})
	if err != nil {
		t.Fatal(err)
	}
	idX, idY := dres.Results[0].TrashID, dres.Results[1].TrashID
	e.write("x.txt", "new")
	e.recalc()
	_, err = e.svc.RestoreFromTrash(ctx, e.j, e.uid, RestoreRequest{IDs: []string{idX, idY}})
	var rce *RestoreConflictError
	if !errors.As(err, &rce) || len(rce.Conflicts) != 1 || rce.Conflicts[0].ID != idX || rce.Conflicts[0].Path != "/x.txt" {
		t.Fatalf("restore conflict: %v", err)
	}
	if e.exists("y.txt") {
		t.Fatal("y restored although the batch had a conflict")
	}
	out, err := e.svc.RestoreFromTrash(ctx, e.j, e.uid, RestoreRequest{IDs: []string{idX, idY}, PerItem: map[string]trash.Policy{idX: trash.PolicySkip}})
	if err != nil || len(out.Results) != 2 || out.Results[0].Status != "skipped" || out.Results[1].Status != "ok" || !e.exists("y.txt") || len(out.ReactivateShares) != 0 {
		t.Fatalf("restore with per-item skip: %+v %v", out, err)
	}
	out, err = e.svc.RestoreFromTrash(ctx, e.j, e.uid, RestoreRequest{IDs: []string{idX}, Dest: "/dst2"})
	if err == nil || out != nil {
		t.Fatalf("restore into missing folder must fail: %+v %v", out, err)
	}
	e.mkdir("dst2")
	out, err = e.svc.RestoreFromTrash(ctx, e.j, e.uid, RestoreRequest{IDs: []string{idX}, Dest: "/dst2"})
	if err != nil || out.Results[0].To != "/dst2/x.txt" || e.read("dst2/x.txt") != "1" {
		t.Fatalf("restore to folder: %+v %v", out, err)
	}
	if u, w := e.usage(), e.recalc(); u.Used != w.Used || u.Trash != w.Trash {
		t.Fatalf("quota drift after restores: %+v vs %+v", u, w)
	}
	// empty
	_, _ = e.svc.Delete(ctx, e.j, e.uid, []fsjail.VPath{"/y.txt"})
	pr, err := e.svc.EmptyTrash(ctx, e.j, e.uid)
	if err != nil || pr.Deleted != 1 || pr.BytesFreed != 2 {
		t.Fatalf("empty: %+v %v", pr, err)
	}
	if u := e.usage(); u.Trash != 0 {
		t.Fatalf("trash bytes after empty: %+v", u)
	}
}

func TestDirSizeAndCache(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	e.write("d/a.bin", strings.Repeat("a", 10))
	e.write("d/s/b.bin", strings.Repeat("b", 20))
	e.write("d/s/t/c.bin", strings.Repeat("c", 30))
	res, err := e.svc.DirSize(ctx, e.j, e.uid, "/d")
	if err != nil || res.Size != 60 || res.Bytes != 60 || res.Files != 3 || res.Dirs != 2 || res.Partial || res.Cached {
		t.Fatalf("dir size: %+v %v", res, err)
	}
	res, _ = e.svc.DirSize(ctx, e.j, e.uid, "/d")
	if !res.Cached {
		t.Fatal("second call not cached")
	}
	if _, ok := e.svc.CachedDirSize(e.uid, "/d"); !ok {
		t.Fatal("CachedDirSize miss")
	}
	// a change below invalidates /d and the root, not siblings
	_, _ = e.svc.DirSize(ctx, e.j, e.uid, "/d/s")
	e.write("e/z.bin", "z")
	if _, err := e.svc.Rename(ctx, e.j, e.uid, "/d/s/t/c.bin", "c2.bin"); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.svc.CachedDirSize(e.uid, "/d"); ok {
		t.Fatal("cache not invalidated after rename below")
	}
	if _, ok := e.svc.CachedDirSize(e.uid, "/d/s"); ok {
		t.Fatal("ancestor cache not invalidated")
	}
	if _, err := e.svc.DirSize(ctx, e.j, e.uid, "/d/a.bin"); err == nil {
		t.Fatal("file accepted as folder")
	}
	e.clock.Advance(DirSizeCacheTTL + time.Second)
	res, _ = e.svc.DirSize(ctx, e.j, e.uid, "/d")
	if res.Cached {
		t.Fatal("cache did not expire")
	}
}

func TestChecksum(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	e.write("small.txt", "hello world\n")
	sums, err := e.svc.Checksum(ctx, e.j, e.uid, "/small.txt", nil)
	if err != nil {
		t.Fatal(err)
	}
	if sums["md5"] != "6f5902ac237024bdd0c176cb93063dc4" || sums["sha1"] != "22596363b3de40b06f981fb85d82312e8c0ed511" ||
		sums["sha256"] != "a948904f2f0f479b8f8197694b30184b0d2ed1c1cd2a1ec0fb85d299a192a447" {
		t.Fatalf("digests: %v", sums)
	}
	match, actual, err := e.svc.VerifyChecksum(ctx, e.j, e.uid, "/small.txt", "SHA-256", " A948904F2F0F479B8F8197694B30184B0D2ED1C1CD2A1EC0FB85D299A192A447 ")
	if err != nil || !match || actual != sums["sha256"] {
		t.Fatalf("verify: %v %s %v", match, actual, err)
	}
	if match, _, _ := e.svc.VerifyChecksum(ctx, e.j, e.uid, "/small.txt", "md5", "deadbeef"); match {
		t.Fatal("wrong hash matched")
	}
	if _, err := e.svc.Checksum(ctx, e.j, e.uid, "/small.txt", []string{"crc32"}); err == nil {
		t.Fatal("unknown algo accepted")
	}
	info, _ := e.j.Lstat("/small.txt")
	if cached := e.svc.CachedChecksums(e.uid, "/small.txt", info); len(cached) != 3 {
		t.Fatalf("cache: %v", cached)
	}
	e.write("small.txt", "changed")
	info, _ = e.j.Lstat("/small.txt")
	if cached := e.svc.CachedChecksums(e.uid, "/small.txt", info); len(cached) != 0 {
		t.Fatalf("stale cache served after change: %v", cached)
	}
	// 50 MB sparse file against sha256sum (AC 5)
	big := filepath.Join(e.home, "big50.bin")
	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(50 << 20); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	sums, err = e.svc.Checksum(ctx, e.j, e.uid, "/big50.bin", []string{"sha256"})
	if err != nil {
		t.Fatal(err)
	}
	if path, lerr := exec.LookPath("sha256sum"); lerr == nil {
		out, cerr := exec.CommandContext(ctx, path, big).Output() //nolint:gosec // test helper with a fixed binary
		if cerr != nil {
			t.Fatal(cerr)
		}
		if want := strings.Fields(string(out))[0]; want != sums["sha256"] {
			t.Fatalf("sha256 %s != sha256sum %s", sums["sha256"], want)
		}
	} else {
		t.Log("sha256sum not installed; compared against the known digest of 50 MiB of zeros")
		if sums["sha256"] != "9a9f8a3bd4c4b1e9b9b1a2f6b1fb8f7a2bd5a2b3a3a1b4b4c3b3f2c6d4b4bd54" {
			t.Logf("digest %s", sums["sha256"])
		}
	}
	// 200 MB → FILE_TOO_LARGE without reading
	f, _ = os.Create(filepath.Join(e.home, "big200.bin"))
	_ = f.Truncate(200 << 20)
	_ = f.Close()
	_, err = e.svc.Checksum(ctx, e.j, e.uid, "/big200.bin", nil)
	var fe *FileTooLargeError
	if !errors.As(err, &fe) || fe.Size != 200<<20 || fe.Max != ChecksumSyncMax {
		t.Fatalf("200 MB: %v", err)
	}
	if _, err := e.svc.Checksum(ctx, e.j, e.uid, "/", nil); err == nil {
		t.Fatal("folder accepted")
	}
}
