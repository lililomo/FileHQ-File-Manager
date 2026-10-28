package files

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/tasks"
)

type fakeShares struct{ paths []string }

func (f fakeShares) SharedPathsRelated(context.Context, string, fsjail.VPath) ([]string, error) {
	return f.paths, nil
}

func (e *env) writeBytes(rel string, b []byte, mtime time.Time) {
	e.t.Helper()
	p := filepath.Join(e.home, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0o640); err != nil {
		e.t.Fatal(err)
	}
	if !mtime.IsZero() {
		if err := os.Chtimes(p, mtime, mtime); err != nil {
			e.t.Fatal(err)
		}
	}
}

// big returns n bytes derived from seed (distinct seeds differ everywhere).
func big(seed string, n int) []byte {
	b := bytes.Repeat([]byte(seed+"|"), n/(len(seed)+1)+1)
	return b[:n]
}

// groupKey is the sorted path list of a group (order-independent compare).
func groupKey(paths []string) string {
	c := append([]string(nil), paths...)
	sort.Strings(c)
	return strings.Join(c, ",")
}

func reportKeys(rep *DuplicateReport) map[string]DuplicateGroup {
	out := map[string]DuplicateGroup{}
	for _, g := range rep.Groups {
		ps := make([]string, 0, len(g.Paths))
		for _, f := range g.Paths {
			ps = append(ps, f.Path)
		}
		out[groupKey(ps)] = g
	}
	return out
}

// TestFindDuplicatesExact: 1000 files of which 100 are extra copies (50
// originals × 2 copies), plus traps; exactly the 50 groups come back.
func TestFindDuplicatesExact(t *testing.T) {
	e := newEnv(t, envOpts{})
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	want := map[string]int64{} // group key → file size
	files := 0
	// 850 unique files; many share a size (same length, different content)
	for i := 0; i < 850; i++ {
		e.writeBytes(fmt.Sprintf("u/%02d/f%04d.txt", i%17, i), []byte(fmt.Sprintf("unique-%04d", i)), time.Time{})
		files++
	}
	// 50 originals with 2 copies each = 150 files; 5 groups above 128 KiB
	for g := 0; g < 50; g++ {
		var content []byte
		if g < 5 {
			content = big(fmt.Sprintf("big-%d", g), 200<<10+g)
		} else {
			content = []byte(fmt.Sprintf("dup-content-%03d", g)) // same length as other groups
		}
		ps := []string{fmt.Sprintf("/d/orig%02d.bin", g), fmt.Sprintf("/d/copy/a%02d.bin", g), fmt.Sprintf("/other/deep/b%02d.bin", g)}
		for i, p := range ps {
			e.writeBytes(p[1:], content, base.Add(time.Duration(i)*time.Hour))
			files++
		}
		want[groupKey(ps)] = int64(len(content))
	}
	if files != 1000 {
		t.Fatalf("fixture has %d files", files)
	}
	// traps
	e.writeBytes("trap/samesize1.bin", big("A", 1024), time.Time{})
	e.writeBytes("trap/samesize2.bin", big("B", 1024), time.Time{})
	mid1 := big("edge", 300<<10)
	mid2 := append([]byte(nil), mid1...)
	mid2[150<<10] ^= 0xff // same first/last 64 KiB, different middle
	e.writeBytes("trap/mid1.bin", mid1, time.Time{})
	e.writeBytes("trap/mid2.bin", mid2, time.Time{})
	for i := 0; i < 5; i++ {
		e.writeBytes(fmt.Sprintf("trap/empty%d", i), nil, time.Time{})
	}
	if err := os.Symlink("../d/orig07.bin", filepath.Join(e.home, "trap", "link-to-dup")); err != nil {
		t.Fatal(err)
	}
	// a hard link pair alone is one physical file: no group
	e.writeBytes("trap/hard1.bin", []byte("hard-link-content"), time.Time{})
	if err := os.Link(filepath.Join(e.home, "trap", "hard1.bin"), filepath.Join(e.home, "trap", "hard2.bin")); err != nil {
		t.Fatal(err)
	}
	// a hard link of a group member is listed once per inode
	if err := os.Link(filepath.Join(e.home, "d", "orig09.bin"), filepath.Join(e.home, "trap", "orig09-hardlink.bin")); err != nil {
		t.Fatal(err)
	}
	// staging names with duplicate content are ignored
	e.writeBytes(".fm-upload-abc", []byte("dup-content-010"), time.Time{})
	e.writeBytes("d/.orig10.bin.fm-tmp-123", []byte("dup-content-010"), time.Time{})
	e.writeBytes("d/x.bin.fm-partial-1234", []byte("dup-content-010"), time.Time{})

	rep, err := e.svc.FindDuplicates(context.Background(), e.j, e.uid, fsjail.Root)
	if err != nil {
		t.Fatal(err)
	}
	got := reportKeys(rep)
	var fp, fn []string
	for k := range got {
		if _, ok := want[k]; !ok {
			fp = append(fp, k)
		}
	}
	for k := range want {
		if _, ok := got[k]; !ok {
			fn = append(fn, k)
		}
	}
	if len(fp) > 0 || len(fn) > 0 {
		t.Fatalf("false positives %v, false negatives %v", fp, fn)
	}
	var reclaim int64
	for k, size := range want {
		g := got[k]
		if g.Size != size || g.Reclaimable != size*2 || len(g.Hash) != 64 {
			t.Errorf("group %s: size %d reclaimable %d hash %q", k, g.Size, g.Reclaimable, g.Hash)
		}
		reclaim += size * 2
	}
	if rep.Reclaimable != reclaim {
		t.Errorf("reclaimable %d, want %d", rep.Reclaimable, reclaim)
	}
	// 850 + 150 + 2 samesize + 2 mid + 3 hard (2 + 1 extra link) + 0 staging/empty/symlinks
	if rep.Scanned != 1007 {
		t.Errorf("scanned %d, want 1007", rep.Scanned)
	}
	for i := 1; i < len(rep.Groups); i++ {
		if rep.Groups[i-1].Reclaimable < rep.Groups[i].Reclaimable {
			t.Fatalf("groups not sorted by reclaimable desc at %d", i)
		}
	}
	// ordering inside a group: oldest first
	for _, g := range rep.Groups {
		if !strings.HasPrefix(g.Paths[0].Path, "/d/orig") {
			t.Errorf("paths[0] = %s, want the oldest /d/orig*", g.Paths[0].Path)
		}
	}
}

func TestDuplicatePathOrderAndShared(t *testing.T) {
	e := newEnv(t, envOpts{})
	same := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	e.writeBytes("zz/longer-name.txt", []byte("same"), same)
	e.writeBytes("a/b.txt", []byte("same"), same)
	e.writeBytes("a/a.txt", []byte("same"), same)
	e.writeBytes("new.txt", []byte("same"), same.Add(time.Hour))
	e.svc.Shares = fakeShares{paths: []string{"/zz"}}
	rep, err := e.svc.FindDuplicates(context.Background(), e.j, e.uid, fsjail.Root)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Groups) != 1 {
		t.Fatalf("groups %+v", rep.Groups)
	}
	var order []string
	shared := map[string]bool{}
	for _, f := range rep.Groups[0].Paths {
		order = append(order, f.Path)
		shared[f.Path] = f.Shared
	}
	if strings.Join(order, " ") != "/a/a.txt /a/b.txt /zz/longer-name.txt /new.txt" {
		t.Errorf("order %v", order)
	}
	if !shared["/zz/longer-name.txt"] || shared["/a/a.txt"] {
		t.Errorf("shared flags %v", shared)
	}
	if rep.Reclaimable != 3*4 {
		t.Errorf("reclaimable %d", rep.Reclaimable)
	}
	// scoped scan
	rep, err = e.svc.FindDuplicates(context.Background(), e.j, e.uid, "/a")
	if err != nil || len(rep.Groups) != 1 || len(rep.Groups[0].Paths) != 2 || rep.Path != "/a" {
		t.Fatalf("scoped: %v %+v", err, rep)
	}
	if _, err := e.svc.FindDuplicates(context.Background(), e.j, e.uid, "/new.txt"); err == nil {
		t.Fatal("file root accepted")
	}
}

func TestDuplicateDeadlineDirect(t *testing.T) {
	e := newEnv(t, envOpts{})
	for i := 0; i < 20; i++ {
		e.writeBytes(fmt.Sprintf("f%d.txt", i), []byte("x"), time.Time{})
	}
	e.svc.DuplicateDeadline = 30 * time.Millisecond
	ctx := WithTask(context.Background(), &TaskContext{ItemDelay: 10 * time.Millisecond})
	_, err := e.svc.FindDuplicates(ctx, e.j, e.uid, fsjail.Root)
	var te *DuplicateTimeoutError
	if !errors.As(err, &te) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err %v", err)
	}
	if code, msg := TaskErrorCode(err); code != "TIMEOUT" || !strings.HasPrefix(msg, "timeout") {
		t.Errorf("task code %s %q", code, msg)
	}
	if te.Total != 20 || te.Hashed >= 20 {
		t.Errorf("progress %+v", te)
	}
}

func newDupEngine(t *testing.T, e *env, delay time.Duration) *tasks.Engine {
	t.Helper()
	eng := tasks.New(tasks.Options{Repo: e.store, MaxPerUser: 2, MaxGlobal: 4, ErrorCode: TaskErrorCode, ItemDelay: delay,
		OpenJail: func(context.Context, string) (*fsjail.Jail, func(), error) { return e.j, func() {}, nil }})
	RegisterTasks(eng, e.svc)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { eng.Shutdown(time.Second); cancel() })
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return eng
}

func TestDuplicateTaskCancelAndTimeout(t *testing.T) {
	e := newEnv(t, envOpts{})
	for i := 0; i < 200; i++ {
		e.writeBytes(fmt.Sprintf("f%03d.txt", i), []byte(fmt.Sprintf("%03d", i%10)), time.Time{})
	}
	eng := newDupEngine(t, e, 20*time.Millisecond)
	ctx := context.Background()
	// cancel mid-run
	tk, err := eng.Enqueue(ctx, tasks.EnqueueRequest{UserID: e.uid, Type: TaskDuplicates, Payload: DuplicatesPayload{Path: "/"}})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, err := eng.Get(ctx, e.uid, tk.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State == tasks.Running && got.Progress.Done > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task never progressed: %+v", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
	start := time.Now()
	if _, err := eng.Cancel(ctx, e.uid, tk.ID); err != nil {
		t.Fatal(err)
	}
	wctx, wcancel := context.WithTimeout(ctx, 2*time.Second)
	defer wcancel()
	if err := eng.Wait(wctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > time.Second {
		t.Errorf("cancel took %s", d)
	}
	got, _ := eng.Get(ctx, e.uid, tk.ID)
	if got.State != tasks.Cancelled || got.Progress.Done >= got.Progress.Total {
		t.Fatalf("after cancel: %+v", got)
	}
	// deadline → failed TIMEOUT with a log line saying how far it got
	e.svc.DuplicateDeadline = 100 * time.Millisecond
	tk, err = eng.Enqueue(ctx, tasks.EnqueueRequest{UserID: e.uid, Type: TaskDuplicates, Payload: DuplicatesPayload{Path: "/"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Wait(wctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = eng.Get(ctx, e.uid, tk.ID)
	if got.State != tasks.Failed || got.Error == nil || got.Error.Code != "TIMEOUT" || len(got.Result) != 0 {
		t.Fatalf("after timeout: %+v %+v", got, got.Error)
	}
	logs, _ := eng.Logs(ctx, e.uid, tk.ID, 0)
	found := false
	for _, l := range logs {
		if l.Level == "error" && strings.Contains(l.Message, "timeout after") && strings.Contains(l.Message, "files scanned") {
			found = true
		}
	}
	if !found {
		t.Errorf("no timeout log: %+v", logs)
	}
	// a full run succeeds with a result
	e.svc.DuplicateDeadline = 0
	eng2 := newDupEngine(t, e, 0)
	tk, err = eng2.Enqueue(ctx, tasks.EnqueueRequest{UserID: e.uid, Type: TaskDuplicates, Payload: DuplicatesPayload{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := eng2.Wait(wctx, tk.ID); err != nil {
		t.Fatal(err)
	}
	got, _ = eng2.Get(ctx, e.uid, tk.ID)
	if got.State != tasks.Done || !bytes.Contains(got.Result, []byte(`"groups"`)) || got.Progress.Done != 200 {
		t.Fatalf("done: %+v %s", got, got.Result)
	}
}

func TestReplaceWithSymlinksAndUndo(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.write("keep/a.txt", "same content")
	e.write("x/b.txt", "same content")
	e.write("x/y/c.txt", "same content")
	e.write("other.txt", "diff content")
	e.write("small.txt", "same")
	if err := os.Link(filepath.Join(e.home, "keep", "a.txt"), filepath.Join(e.home, "hl.txt")); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	cases := map[string][]fsjail.VPath{
		"hash_mismatch": {"/x/b.txt", "/other.txt"},
		"size_mismatch": {"/small.txt"},
		"same_file":     {"/hl.txt"},
		"contains_keep": {"/keep/a.txt"},
		"not_a_file":    {"/x"},
	}
	for reason, paths := range cases {
		_, err := e.svc.ReplaceWithSymlinks(ctx, e.j, e.uid, []LinkGroup{{Keep: "/keep/a.txt", Paths: paths}})
		var ve *ValidationError
		if !errors.As(err, &ve) || ve.Reason != reason {
			t.Errorf("%s: got %v", reason, err)
		}
	}
	if e.read("x/b.txt") != "same content" {
		t.Fatal("a refused request changed files")
	}
	res, err := e.svc.ReplaceWithSymlinks(ctx, e.j, e.uid, []LinkGroup{{Keep: "/keep/a.txt", Paths: []fsjail.VPath{"/x/b.txt"}}, {Keep: "/keep/a.txt", Paths: []fsjail.VPath{"/x/y/c.txt"}}})
	if err != nil {
		t.Fatal(err)
	}
	if res.UndoToken == "" || res.Undo == nil || len(res.Undo.Symlinks) != 2 {
		t.Fatalf("result %+v", res)
	}
	for p, target := range map[string]string{"x/b.txt": "../keep/a.txt", "x/y/c.txt": "../../keep/a.txt"} {
		got, err := os.Readlink(filepath.Join(e.home, p))
		if err != nil || got != target {
			t.Errorf("%s -> %q (%v), want %s", p, got, err, target)
		}
		if e.read(p) != "same content" {
			t.Errorf("%s does not read through", p)
		}
	}
	rows, err := e.store.ListTrashByBatch(ctx, e.uid, res.BatchID)
	if err != nil || len(rows) != 2 {
		t.Errorf("trash rows %d (%v)", len(rows), err)
	}
	// undo: links removed, originals back as regular files
	if _, err := e.svc.Undo(ctx, e.j, e.uid, res.UndoToken); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"x/b.txt", "x/y/c.txt"} {
		fi, err := os.Lstat(filepath.Join(e.home, p))
		if err != nil || !fi.Mode().IsRegular() || e.read(p) != "same content" {
			t.Errorf("%s after undo: %v %v", p, fi, err)
		}
	}
}

func TestPruneDuplicateReport(t *testing.T) {
	e := newEnv(t, envOpts{})
	e.write("a.txt", "abc")
	e.write("b.txt", "abc")
	e.write("c.txt", "abc")
	rep, err := e.svc.FindDuplicates(context.Background(), e.j, e.uid, fsjail.Root)
	if err != nil || len(rep.Groups) != 1 || rep.Reclaimable != 6 {
		t.Fatalf("%v %+v", err, rep)
	}
	if err := os.Remove(filepath.Join(e.home, "c.txt")); err != nil {
		t.Fatal(err)
	}
	PruneDuplicateReport(e.j, rep)
	if len(rep.Groups) != 1 || len(rep.Groups[0].Paths) != 2 || rep.Reclaimable != 3 {
		t.Fatalf("after one removal: %+v", rep)
	}
	e.write("b.txt", "abcd") // resized
	PruneDuplicateReport(e.j, rep)
	if len(rep.Groups) != 0 || rep.Reclaimable != 0 {
		t.Fatalf("after resize: %+v", rep)
	}
}
