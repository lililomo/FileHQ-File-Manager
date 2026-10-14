package files

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/fsjail"
)

// Review B probes (PROMPT Step R-B checklist b, d, e, f). Kept as
// regression tests.

// failingTxRepo makes every write transaction fail: it simulates a database
// error after the filesystem operation already succeeded (checklist b).
type failingTxRepo struct {
	db.Repository
	fail bool
}

func (f *failingTxRepo) WithTx(ctx context.Context, fn func(tx db.Repository) error) error {
	if f.fail {
		return errors.New("simulated transaction failure")
	}
	return f.Repository.WithTx(ctx, fn)
}

func (e *env) serviceWithRepo(repo db.Repository) *Service {
	return New(Options{Repo: repo, Trash: e.trash, Now: e.clock.Now, Quota: e.quota, Locks: e.svc.locks})
}

func (e *env) favorite(rel string) {
	e.t.Helper()
	if err := e.store.UpsertFavorite(context.Background(), e.uid, rel, db.FormatTime(e.clock.Now())); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) favoritePaths() []string {
	e.t.Helper()
	rows, err := e.store.ListFavorites(context.Background(), e.uid)
	if err != nil {
		e.t.Fatal(err)
	}
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Path)
	}
	return out
}

// (b) rename / move / batch rename roll the filesystem back when the
// reference transaction fails, and the metadata rows stay where they were.
func TestReviewBReferenceTxFailureRollsBackFilesystem(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	e.write("a.txt", "a")
	e.write("x1.txt", "1")
	e.write("x2.txt", "2")
	e.mkdir("d")
	e.favorite("/a.txt")
	e.favorite("/x1.txt")
	broken := e.serviceWithRepo(&failingTxRepo{Repository: e.store, fail: true})

	if _, err := broken.Rename(ctx, e.j, e.uid, vp("/a.txt"), "b.txt"); err == nil {
		t.Fatal("rename must report the transaction failure")
	}
	if !e.exists("a.txt") || e.exists("b.txt") {
		t.Fatal("rename was not rolled back on the filesystem")
	}
	if _, err := broken.Move(ctx, e.j, e.uid, vps("/a.txt"), vp("/d"), ConflictPolicy{}); err == nil {
		t.Fatal("move must report the transaction failure")
	}
	if !e.exists("a.txt") || e.exists("d/a.txt") {
		t.Fatal("move was not rolled back on the filesystem")
	}
	rule := RenameRule{Numbering: &Numbering{Pattern: "n{n}"}}
	if _, _, err := broken.BatchRename(ctx, e.j, e.uid, vps("/x1.txt", "/x2.txt"), rule); err == nil {
		t.Fatal("batch rename must report the transaction failure")
	}
	if !e.exists("x1.txt") || !e.exists("x2.txt") || e.exists("n1.txt") || e.exists("n2.txt") {
		t.Fatal("batch rename was not rolled back on the filesystem")
	}
	entries, err := e.j.ReadDir(fsjail.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, en := range entries {
		if strings.HasSuffix(en.Name, ".fmtmp") {
			t.Fatalf("temporary name left behind: %s", en.Name)
		}
	}
	if got := strings.Join(e.favoritePaths(), ","); got != "/a.txt,/x1.txt" {
		t.Fatalf("favorites changed although every transaction failed: %s", got)
	}
	// the healthy service still works afterwards (locks were released)
	if _, err := e.svc.Rename(ctx, e.j, e.uid, vp("/a.txt"), "b.txt"); err != nil {
		t.Fatalf("rename after rollback: %v", err)
	}
	if got := strings.Join(e.favoritePaths(), ","); got != "/b.txt,/x1.txt" {
		t.Fatalf("favorites after real rename: %s", got)
	}
}

// (e) a copy in flight holds a read lock on its source: renaming or moving
// an ancestor of the source (or the destination folder) must fail fast with
// ErrLocked, and the reverse order behaves the same.
func TestReviewBLockAncestorRenameVersusCopy(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	e.mkdir("a/b")
	e.write("a/b/c.txt", "c")
	e.mkdir("d")
	e.mkdir("e")
	started := make(chan struct{})
	proceed := make(chan struct{})
	var first atomic.Bool
	// only the first copy parks in the hook (sync.Once would block the
	// second, compatible copy below until the first one is released)
	e.svc.TestHook = func(op string) {
		if op == "copy" && first.CompareAndSwap(false, true) {
			close(started)
			<-proceed
		}
	}
	done := make(chan error, 1)
	go func() {
		_, err := e.svc.Copy(ctx, e.j, e.uid, vps("/a/b/c.txt"), vp("/d"), ConflictPolicy{})
		done <- err
	}()
	<-started
	if _, err := e.svc.Rename(ctx, e.j, e.uid, vp("/a"), "z"); !errors.Is(err, fsjail.ErrLocked) {
		t.Fatalf("rename of the source ancestor during copy: %v", err)
	}
	if _, err := e.svc.Move(ctx, e.j, e.uid, vps("/a/b"), vp("/e"), ConflictPolicy{}); !errors.Is(err, fsjail.ErrLocked) {
		t.Fatalf("move of the source parent during copy: %v", err)
	}
	if _, err := e.svc.Rename(ctx, e.j, e.uid, vp("/d"), "dd"); !errors.Is(err, fsjail.ErrLocked) {
		t.Fatalf("rename of the destination folder during copy: %v", err)
	}
	// a second copy of the same source is compatible (read locks)
	if _, err := e.svc.Copy(ctx, e.j, e.uid, vps("/a/b/c.txt"), vp("/e"), ConflictPolicy{}); err != nil {
		t.Fatalf("parallel read-locked copy: %v", err)
	}
	close(proceed)
	if err := <-done; err != nil {
		t.Fatalf("copy: %v", err)
	}
	if !e.exists("d/c.txt") || !e.exists("a/b/c.txt") {
		t.Fatal("copy result missing")
	}
	// reverse: rename holds the write lock, copy from below it fails fast
	started2 := make(chan struct{})
	proceed2 := make(chan struct{})
	e.svc.TestHook = func(op string) {
		if op == "rename" {
			close(started2)
			<-proceed2
		}
	}
	go func() {
		_, err := e.svc.Rename(ctx, e.j, e.uid, vp("/a"), "z")
		done <- err
	}()
	<-started2
	if _, err := e.svc.Copy(ctx, e.j, e.uid, vps("/a/b/c.txt"), vp("/e"), ConflictPolicy{Default: PolicyRename}); !errors.Is(err, fsjail.ErrLocked) {
		t.Fatalf("copy during ancestor rename: %v", err)
	}
	close(proceed2)
	if err := <-done; err != nil {
		t.Fatalf("rename: %v", err)
	}
	if !e.exists("z/b/c.txt") {
		t.Fatal("rename result missing")
	}
}

// (f) batch rename: RE2 only (no catastrophic backtracking), results with a
// slash, dot names or control characters are reported invalid and never
// applied; the find pattern is bounded.
func TestReviewBBatchRenameRegexCannotProduceSlashOrHang(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	e.write("f1.txt", "1")
	e.write("f2.txt", "2")
	e.mkdir("folder")
	preview := func(rule RenameRule, paths ...string) []PreviewItem {
		t.Helper()
		items, err := e.svc.BatchRenamePreview(ctx, e.j, vps(paths...), rule)
		if err != nil {
			t.Fatalf("preview %+v: %v", rule, err)
		}
		return items
	}
	cases := []struct {
		name string
		rule RenameRule
		want string
	}{
		{"slash via $1", RenameRule{Find: "(.*)", Replace: "$1/x", Regex: true}, "slash"},
		{"slash literal", RenameRule{Find: "f", Replace: "a/b"}, "slash"},
		{"dot-dot name", RenameRule{Find: ".*", Replace: "..", Regex: true, ExtMode: "include"}, ""},
		{"numbering with slash", RenameRule{Numbering: &Numbering{Pattern: "../{n}"}}, "slash"},
		{"control char", RenameRule{Find: "f", Replace: "a\x01b"}, ""},
		{"nul byte", RenameRule{Find: "f", Replace: "a\x00b"}, ""},
	}
	for _, c := range cases {
		items := preview(c.rule, "/f1.txt", "/f2.txt", "/folder")
		for _, it := range items {
			if it.Status != "invalid" {
				t.Fatalf("%s: %+v must be invalid", c.name, it)
			}
			if c.want != "" && it.Reason != c.want {
				t.Fatalf("%s: reason %q, want %q", c.name, it.Reason, c.want)
			}
		}
		if _, _, err := e.svc.BatchRename(ctx, e.j, e.uid, vps("/f1.txt", "/f2.txt", "/folder"), c.rule); err == nil {
			t.Fatalf("%s: apply must be refused", c.name)
		}
		var be *BatchRenameError
		if _, _, err := e.svc.BatchRename(ctx, e.j, e.uid, vps("/f1.txt"), c.rule); !errors.As(err, &be) {
			t.Fatalf("%s: apply error %v, want BatchRenameError", c.name, err)
		}
	}
	if !e.exists("f1.txt") || !e.exists("f2.txt") || !e.exists("folder") {
		t.Fatal("an invalid preview must not touch the filesystem")
	}
	// ReDoS: a pattern that backtracks exponentially in PCRE is linear in RE2
	e.write(strings.Repeat("a", 60)+"!.txt", "x")
	start := time.Now()
	items := preview(RenameRule{Find: "^(a+)+$", Replace: "b", Regex: true}, "/"+strings.Repeat("a", 60)+"!.txt")
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("regex took %v", d)
	}
	if items[0].Status != "ok" || items[0].To != items[0].From {
		t.Fatalf("non-matching regex must leave the name unchanged: %+v", items[0])
	}
	// nested repetition counts are rejected by RE2, never expanded
	_, err := e.svc.BatchRenamePreview(ctx, e.j, vps("/f1.txt"), RenameRule{Find: "((a{1000}){1000}){1000}", Regex: true})
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("nested repeat: %v", err)
	}
	_, err = e.svc.BatchRenamePreview(ctx, e.j, vps("/f1.txt"), RenameRule{Find: strings.Repeat("a", MaxRegexLength+1), Regex: true})
	if !errors.As(err, &ve) || ve.Fields["rule.find"] != "too_long" {
		t.Fatalf("pattern length: %v", err)
	}
}

// (f, SPEC §5.2) batch rename must not produce a blocked extension: the
// single rename endpoint refuses it, so the batch path has to as well.
func TestReviewBBatchRenameBlockedExtension(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	e.write("a.txt", "a")
	e.mkdir("d.txt")
	svc := New(Options{Repo: e.store, Trash: e.trash, Now: e.clock.Now, Quota: e.quota, Locks: e.svc.locks, BlockedExtensions: []string{"exe"}})
	items, err := svc.BatchRenamePreview(ctx, e.j, vps("/a.txt", "/d.txt"), RenameRule{Find: ".txt", Replace: ".exe", ExtMode: "include"})
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Status != "invalid" || items[0].Reason != "blocked_extension" {
		t.Fatalf("file: %+v", items[0])
	}
	if items[1].Status != "ok" {
		t.Fatalf("folder names are not subject to the extension list: %+v", items[1])
	}
	var be *BatchRenameError
	if _, _, err := svc.BatchRename(ctx, e.j, e.uid, vps("/a.txt"), RenameRule{Find: ".txt", Replace: ".exe", ExtMode: "include"}); !errors.As(err, &be) {
		t.Fatalf("apply: %v", err)
	}
	if !e.exists("a.txt") || e.exists("a.exe") {
		t.Fatal("blocked rename must not be applied")
	}
	// the numbering path is covered too ({ext} comes from the stem split)
	items, err = svc.BatchRenamePreview(ctx, e.j, vps("/a.txt"), RenameRule{Numbering: &Numbering{Pattern: "{n}.exe"}, ExtMode: "include"})
	if err != nil || items[0].Reason != "blocked_extension" {
		t.Fatalf("numbering: %v %+v", err, items)
	}
}

// (d) a move that overwrites (file over file, folder merge with overwrite)
// destroys the bytes of the destination; the used counter must follow.
func TestReviewBMoveOverwriteReleasesQuota(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	e.write("big.txt", strings.Repeat("x", 1000))
	e.mkdir("d")
	e.write("d/big.txt", strings.Repeat("y", 5000))
	e.mkdir("m")
	e.write("m/f.txt", strings.Repeat("x", 100))
	e.mkdir("d/m")
	e.write("d/m/f.txt", strings.Repeat("y", 300))
	e.write("d/m/keep.txt", strings.Repeat("k", 50))
	e.recalc()
	if u := e.usage(); u.Used != 6450 {
		t.Fatalf("baseline used %d", u.Used)
	}
	if _, err := e.svc.Move(ctx, e.j, e.uid, vps("/big.txt"), vp("/d"), ConflictPolicy{Default: PolicyOverwrite}); err != nil {
		t.Fatal(err)
	}
	if u := e.usage(); u.Used != 1450 {
		t.Fatalf("used after overwrite move = %d, want 1450", u.Used)
	}
	if _, err := e.svc.Move(ctx, e.j, e.uid, vps("/m"), vp("/d"), ConflictPolicy{Default: PolicyOverwrite}); err != nil {
		t.Fatal(err)
	}
	if u := e.usage(); u.Used != 1150 {
		t.Fatalf("used after merge move = %d, want 1150", u.Used)
	}
	if got := e.recalc(); got.Used != 1150 {
		t.Fatalf("recalculation disagrees: %d", got.Used)
	}
}
