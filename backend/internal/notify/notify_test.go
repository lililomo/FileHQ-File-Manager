package notify

import (
	"context"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

type pub struct{ events []string }

func (p *pub) Publish(userID, name string, _ any) { p.events = append(p.events, userID+":"+name) }

func newStore(t *testing.T) *db.Store {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.Close() })
	if _, err := d.MigrateUp(context.Background()); err != nil {
		t.Fatal(err)
	}
	store := db.NewStore(d)
	now := db.Now()
	for _, u := range []string{"a", "b"} {
		if _, err := store.CreateUser(context.Background(), sqlc.CreateUserParams{ID: "user-" + u, Username: u, DisplayName: u, Role: "user", Status: "active",
			PasswordHash: "x", QuotaBytes: 1 << 30, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

func TestNotifications(t *testing.T) {
	store := newStore(t)
	p := &pub{}
	clock := time.Date(2026, 10, 14, 10, 0, 0, 0, time.UTC)
	svc := New(store, p, nil, func() time.Time { clock = clock.Add(time.Millisecond); return clock })
	ctx := context.Background()
	n, err := svc.Create(ctx, "user-a", TypeTaskDone, "Salin 3 item", "", "/files/dest")
	if err != nil || n.ID == "" || n.Link != "/files/dest" {
		t.Fatalf("create: %v %+v", err, n)
	}
	if len(p.events) != 1 || p.events[0] != "user-a:"+EventName {
		t.Fatalf("events: %v", p.events)
	}
	for i := 0; i < MaxPerUser+10; i++ {
		if _, err := svc.Create(ctx, "user-a", TypeTaskFailed, "t"+strconv.Itoa(i), "boom", ""); err != nil {
			t.Fatal(err)
		}
	}
	page, err := svc.List(ctx, "user-a", false, "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if page.Unread != MaxPerUser || len(page.Items) != 100 || page.NextCursor == "" {
		t.Fatalf("list: unread=%d items=%d cursor=%q", page.Unread, len(page.Items), page.NextCursor)
	}
	// newest first; the very first one was pruned
	if page.Items[0].Title != "t"+strconv.Itoa(MaxPerUser+9) {
		t.Fatalf("order: %s", page.Items[0].Title)
	}
	page2, err := svc.List(ctx, "user-a", false, page.NextCursor, 100)
	if err != nil || len(page2.Items) != 100 || page2.NextCursor != "" {
		t.Fatalf("page2: %v %d %q", err, len(page2.Items), page2.NextCursor)
	}
	if page.Items[99].ID == page2.Items[0].ID {
		t.Fatal("cursor overlap")
	}
	// mark some read, then all
	read, err := svc.MarkRead(ctx, "user-a", []string{page.Items[0].ID, page.Items[1].ID, "nope"}, false)
	if err != nil || read != 2 {
		t.Fatalf("mark read: %v %d", err, read)
	}
	un, err := svc.List(ctx, "user-a", true, "", 10)
	if err != nil || un.Unread != MaxPerUser-2 {
		t.Fatalf("unread: %v %d", err, un.Unread)
	}
	for _, it := range un.Items {
		if it.ReadAt != nil {
			t.Fatal("unread filter returned read item")
		}
	}
	if _, err := svc.MarkRead(ctx, "user-a", nil, true); err != nil {
		t.Fatal(err)
	}
	if c, _ := svc.Unread(ctx, "user-a"); c != 0 {
		t.Fatalf("unread after all: %d", c)
	}
	// user b cannot read/delete a's notifications
	if ok, err := svc.Delete(ctx, "user-b", page.Items[0].ID); err != nil || ok {
		t.Fatalf("cross-user delete: %v %v", err, ok)
	}
	if read, _ := svc.MarkRead(ctx, "user-b", []string{page.Items[0].ID}, false); read != 0 {
		t.Fatal("cross-user mark read")
	}
	if pb, _ := svc.List(ctx, "user-b", false, "", 10); len(pb.Items) != 0 || pb.Unread != 0 {
		t.Fatal("user b sees a's notifications")
	}
	if ok, err := svc.Delete(ctx, "user-a", page.Items[0].ID); err != nil || !ok {
		t.Fatalf("delete: %v %v", err, ok)
	}
}
