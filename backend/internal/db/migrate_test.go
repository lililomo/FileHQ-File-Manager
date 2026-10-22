package db

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T) *DB {
	t.Helper()
	d, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = d.Close() })
	return d
}

func openMigrated(t *testing.T) *DB {
	t.Helper()
	d := openTemp(t)
	if _, err := d.MigrateUp(context.Background()); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	return d
}

func TestOpenAppliesPragmas(t *testing.T) {
	d := openTemp(t)
	var mode string
	if err := d.Reader().QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal_mode = %q, err=%v", mode, err)
	}
	var fk, busy int
	if err := d.Writer().QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys = %d, err=%v", fk, err)
	}
	if err := d.Writer().QueryRow("PRAGMA busy_timeout").Scan(&busy); err != nil || busy != defaultBusyMS {
		t.Fatalf("busy_timeout = %d, err=%v", busy, err)
	}
	var sync int
	if err := d.Writer().QueryRow("PRAGMA synchronous").Scan(&sync); err != nil || sync != 1 { // 1 = NORMAL
		t.Fatalf("synchronous = %d, err=%v", sync, err)
	}
}

func TestMigrateUpDownUp(t *testing.T) {
	ctx := context.Background()
	d := openTemp(t)

	st, err := d.MigrateStatus(ctx)
	if err != nil {
		t.Fatal(err)
	}
	const migrations = 13 // 0001..0013 (0013_share_users, Step 18b)
	if len(st) != migrations {
		t.Fatalf("want %d embedded migrations, got %d", migrations, len(st))
	}
	for _, m := range st {
		if m.Applied {
			t.Fatalf("fresh db: %d must be pending", m.Version)
		}
	}
	if pending, _ := d.HasPendingMigrations(ctx); !pending {
		t.Fatal("fresh db must report pending migrations")
	}
	if err := d.Ready(ctx); err == nil {
		t.Fatal("Ready must fail while migrations are pending")
	}

	applied, err := d.MigrateUp(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != migrations || applied[0] != 1 || applied[migrations-1] != int64(migrations) {
		t.Fatalf("applied = %v", applied)
	}
	if err := d.Ready(ctx); err != nil {
		t.Fatalf("Ready after up: %v", err)
	}
	for _, tbl := range []string{"users", "sessions", "settings", "user_preferences", "folder_view_prefs", "audit_logs", "login_attempts", "media_metadata", "trash_items", "activity_logs", "favorites", "pins", "file_notes", "file_tags", "file_versions", "quota_usage", "editor_sessions", "media_positions"} {
		var n int
		if err := d.Reader().QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name=?", tbl).Scan(&n); err != nil || n != 1 {
			t.Fatalf("table %s missing (n=%d, err=%v)", tbl, n, err)
		}
	}
	// Second up is a no-op.
	if again, err := d.MigrateUp(ctx); err != nil || len(again) != 0 {
		t.Fatalf("second up: %v %v", again, err)
	}

	for want := int64(migrations); want >= 1; want-- {
		v, err := d.MigrateDown(ctx)
		if err != nil || v != want {
			t.Fatalf("down: got %d err=%v, want %d", v, err, want)
		}
	}
	if v, err := d.MigrateDown(ctx); err != nil || v != 0 {
		t.Fatalf("down on empty: %d %v", v, err)
	}
	var n int
	if err := d.Reader().QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'goose_%' AND name NOT LIKE 'sqlite_%'").Scan(&n); err != nil || n != 0 {
		t.Fatalf("tables left after down: %d (%v)", n, err)
	}
	if applied, err := d.MigrateUp(ctx); err != nil || len(applied) != migrations {
		t.Fatalf("re-up: %v %v", applied, err)
	}
}

func TestWALFileAppearsOnWrite(t *testing.T) {
	d := openMigrated(t)
	ctx := context.Background()
	s := NewStore(d)
	if err := s.UpsertSetting(ctx, settingParams("app_name", `"X"`)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(d.Path() + "-wal"); err != nil {
		t.Fatalf("wal file: %v", err)
	}
}

func TestOpenRejectsUnwritableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := filepath.Join(t.TempDir(), "ro")
	if err := os.Mkdir(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(filepath.Join(dir, "app.db")); err == nil {
		t.Fatal("expected error")
	}
}
