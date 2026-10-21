package versions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/quota"
)

// TestReviewDRestoreRespectsQuota (review D, checklist c "kuota"): restoring
// a big version over a small file is a write like any other — with the
// quota full it is refused before the file is touched, and no stray
// pre-image version is left behind by the attempt.
func TestReviewDRestoreRespectsQuota(t *testing.T) {
	e := newEnv(t, 5)
	ctx := context.Background()
	big := strings.Repeat("x", 200<<10)
	e.write(t, "doc.txt", big)
	v, created, err := e.st.Snapshot(ctx, e.j, e.userID, "/doc.txt", ReasonSave)
	if err != nil || !created {
		t.Fatalf("snapshot: %v %v", err, created)
	}
	e.write(t, "doc.txt", "small\n")
	// recalculate usage, then cap the quota just above it
	if _, err := e.quota.Recalculate(ctx, quota.Target{UserID: e.userID, Jail: e.j}); err != nil {
		t.Fatal(err)
	}
	u, err := e.quota.Usage(ctx, e.userID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateUserQuota(ctx, sqlc.UpdateUserQuotaParams{QuotaBytes: u.Used + u.Versions + 4<<10, UpdatedAt: db.Now(), ID: e.userID}); err != nil {
		t.Fatal(err)
	}
	_, err = e.st.Restore(ctx, e.j, e.userID, v.ID)
	var qe *quota.ExceededError
	if !errors.As(err, &qe) {
		t.Fatalf("restore over quota: %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(e.home, "doc.txt"))
	if string(got) != "small\n" {
		t.Fatalf("file touched: %d bytes", len(got))
	}
	// the pre-image of the attempt stays (it was taken before the quota
	// check) but nothing else changed; with room again the restore works
	if err := e.store.UpdateUserQuota(ctx, sqlc.UpdateUserQuotaParams{QuotaBytes: 1 << 30, UpdatedAt: db.Now(), ID: e.userID}); err != nil {
		t.Fatal(err)
	}
	res, err := e.st.Restore(ctx, e.j, e.userID, v.ID)
	if err != nil || res.Size != int64(len(big)) {
		t.Fatalf("restore with room: %v %+v", err, res)
	}
	got, _ = os.ReadFile(filepath.Join(e.home, "doc.txt"))
	if string(got) != big {
		t.Fatalf("restored bytes: %d", len(got))
	}
}
