package db

import (
	"context"
	"testing"
)

// Review E (19a note 2): every table with a foreign key to users must be
// covered by PurgeUserData (userTables) unless the row is kept on purpose
// (ON DELETE SET NULL: audit_logs keeps the trail) or follows a cascade of a
// purged parent. A new migration with a user_id column fails this test
// until the list is extended.
func TestReviewEPurgeUserDataCoversSchema(t *testing.T) {
	d := openMigrated(t)
	defer func() { _ = d.Close() }()
	ctx := context.Background()
	covered := map[string]string{}
	for _, ut := range userTables {
		covered[ut.table] = ut.column
	}
	rows, err := d.Reader().QueryContext(ctx, "SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' AND name NOT LIKE 'goose_%'")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, n)
	}
	_ = rows.Close()
	if len(tables) < 20 {
		t.Fatalf("only %d tables", len(tables))
	}
	seen := 0
	for _, tbl := range tables {
		fks, err := d.Reader().QueryContext(ctx, "PRAGMA foreign_key_list("+tbl+")") //nolint:gosec // table names come from sqlite_master
		if err != nil {
			t.Fatal(err)
		}
		for fks.Next() {
			var id, seq int
			var parent, from, to, onUpdate, onDelete, match string
			if err := fks.Scan(&id, &seq, &parent, &from, &to, &onUpdate, &onDelete, &match); err != nil {
				t.Fatal(err)
			}
			if parent != "users" {
				continue
			}
			seen++
			if col, ok := covered[tbl]; ok {
				if col != from && tbl != "shares" && tbl != "share_recipients" {
					t.Errorf("%s: PurgeUserData deletes by %s but the FK column is %s", tbl, col, from)
				}
				continue
			}
			switch onDelete {
			case "SET NULL":
				// kept on purpose (audit trail); the reference is nulled by HardDeleteUser
				if tbl != "audit_logs" && tbl != "trash_items" {
					t.Errorf("%s.%s references users with SET NULL but is not a known keep-table", tbl, from)
				}
			default:
				t.Errorf("%s.%s references users (%s) but PurgeUserData does not cover it", tbl, from, onDelete)
			}
		}
		_ = fks.Close()
	}
	if seen < len(userTables) {
		t.Fatalf("found %d user foreign keys for %d purge entries", seen, len(userTables))
	}
}
