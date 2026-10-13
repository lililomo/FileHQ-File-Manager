package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"kppbwi.app/filemanager/internal/db/sqlc"
)

func TestBackupVacuumInto(t *testing.T) {
	ctx := context.Background()
	src := openMigrated(t)
	s := NewStore(src)
	for _, name := range []string{"a", "b", "c"} {
		if _, err := s.CreateUser(ctx, newUser(name, "user")); err != nil {
			t.Fatal(err)
		}
	}
	for i := range 100 {
		if _, err := s.InsertAudit(ctx, sqlc.InsertAuditParams{At: Now(), Action: "x", Ip: "::1", UserAgent: "ua", TargetID: Ptr(string(rune('a' + i%26)))}); err != nil {
			t.Fatal(err)
		}
	}

	dst := filepath.Join(t.TempDir(), "backups", "app-test.db")
	if err := src.Backup(ctx, dst); err != nil {
		t.Fatalf("backup: %v", err)
	}
	st, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o", st.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(dst))
	if len(entries) != 1 {
		t.Fatalf("temp file left behind: %v", entries)
	}
	if err := src.Backup(ctx, dst); err == nil {
		t.Fatal("backup must refuse to overwrite an existing file")
	}

	copyDB, err := Open(dst)
	if err != nil {
		t.Fatalf("open copy: %v", err)
	}
	defer func() { _ = copyDB.Close() }()
	if err := copyDB.Ready(ctx); err != nil {
		t.Fatalf("copy ready: %v", err)
	}
	cs := NewStore(copyDB)
	if n, _ := cs.CountAllUsers(ctx); n != 3 {
		t.Fatalf("users in copy: %d", n)
	}
	rows, _ := cs.ListAudit(ctx, AuditFilter{Limit: 1000})
	if len(rows) != 100 {
		t.Fatalf("audit rows in copy: %d", len(rows))
	}
	// the copy is independent of the source
	if _, err := s.CreateUser(ctx, newUser("d", "user")); err != nil {
		t.Fatal(err)
	}
	if n, _ := cs.CountAllUsers(ctx); n != 3 {
		t.Fatal("copy must not change")
	}
}
