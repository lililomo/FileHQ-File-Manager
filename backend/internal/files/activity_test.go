package files

import (
	"context"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
)

// TestRecentAndActivity covers the Recent derivation (dedup per path,
// 30-day window, only Recent actions) and the filtered journal paging.
func TestRecentAndActivity(t *testing.T) {
	e := newEnv(t, envOpts{})
	ctx := context.Background()
	rec := func(action, p string) {
		e.svc.RecordActivity(ctx, e.uid, action, p, "", map[string]any{"k": 1})
		e.clock.Advance(time.Second)
	}
	rec(ActCreate, "/a.txt")
	rec(ActOpen, "/b.txt")
	rec(ActOpen, "/a.txt")  // newer open of a → a first, once
	rec(ActChmod, "/c.txt") // not a Recent action
	rec(ActRename, "/d.txt")
	rec(ActRestore, "/e.txt")

	recent, err := e.svc.Recent(ctx, e.uid, 10)
	if err != nil {
		t.Fatal(err)
	}
	got := ""
	for _, r := range recent {
		got += r.Path + ":" + r.Action + ","
	}
	if got != "/e.txt:restore,/a.txt:open,/b.txt:open," {
		t.Fatalf("recent: %s", got)
	}
	if r, _ := e.svc.Recent(ctx, e.uid, 2); len(r) != 2 || r[1].Path != "/a.txt" {
		t.Fatalf("recent limit: %+v", r)
	}
	// 30-day window: jump the clock forward
	e.clock.Advance(RecentWindow + time.Hour)
	rec(ActOpen, "/f.txt")
	if r, _ := e.svc.Recent(ctx, e.uid, 10); len(r) != 1 || r[0].Path != "/f.txt" {
		t.Fatalf("window: %+v", r)
	}

	// journal: newest first, filters, cursor paging
	page, err := e.svc.ListActivity(ctx, e.uid, db.ActivityFilter{Limit: 3})
	if err != nil || len(page.Items) != 3 || page.NextCursor == nil || page.Items[0].Path == nil || *page.Items[0].Path != "/f.txt" {
		t.Fatalf("page 1: %v %+v", err, page)
	}
	if page.Items[0].Details["k"] != float64(1) {
		t.Fatalf("details decoded: %+v", page.Items[0].Details)
	}
	var cursor int64
	for _, c := range *page.NextCursor {
		cursor = cursor*10 + int64(c-'0')
	}
	page2, err := e.svc.ListActivity(ctx, e.uid, db.ActivityFilter{Limit: 3, BeforeID: cursor})
	if err != nil || len(page2.Items) != 3 || page2.Items[0].ID >= page.Items[2].ID || page2.NextCursor == nil {
		t.Fatalf("page 2: %v %+v", err, page2)
	}
	page3, err := e.svc.ListActivity(ctx, e.uid, db.ActivityFilter{Limit: 3, BeforeID: page2.Items[2].ID})
	if err != nil || len(page3.Items) != 1 || page3.NextCursor != nil {
		t.Fatalf("page 3: %v %+v", err, page3)
	}
	if p, _ := e.svc.ListActivity(ctx, e.uid, db.ActivityFilter{Action: ActOpen}); len(p.Items) != 3 {
		t.Fatalf("action filter: %d", len(p.Items))
	}
	if p, _ := e.svc.ListActivity(ctx, e.uid, db.ActivityFilter{Query: "A.TXT"}); len(p.Items) != 2 {
		t.Fatalf("query filter: %d", len(p.Items))
	}
	from := db.FormatTime(e.clock.Now().Add(-time.Hour))
	if p, _ := e.svc.ListActivity(ctx, e.uid, db.ActivityFilter{From: from}); len(p.Items) != 1 {
		t.Fatalf("from filter: %d", len(p.Items))
	}
	if p, _ := e.svc.ListActivity(ctx, e.uid, db.ActivityFilter{To: from}); len(p.Items) != 6 {
		t.Fatalf("to filter: %d", len(p.Items))
	}

	// retention prune, then clear
	n, err := e.svc.PruneActivity(ctx, 24*time.Hour)
	if err != nil || n != 6 {
		t.Fatalf("prune: %v %d", err, n)
	}
	if n, _ := e.svc.ClearActivity(ctx, e.uid); n != 1 {
		t.Fatalf("clear: %d", n)
	}
	if p, _ := e.svc.ListActivity(ctx, e.uid, db.ActivityFilter{}); len(p.Items) != 0 {
		t.Fatal("journal not empty")
	}
}
