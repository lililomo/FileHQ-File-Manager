package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
)

// Backup writes a consistent online copy of the database to dst using
// VACUUM INTO (SQLite ≥ 3.27): the copy is written to a temporary file next to
// dst and renamed into place, so a crash never leaves a half-written backup.
// dst must not exist (VACUUM INTO refuses to overwrite).
func (d *DB) Backup(ctx context.Context, dst string) error {
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("db: backup target %s already exists: %w", dst, os.ErrExist)
	}
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("db: backup dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return fmt.Errorf("db: backup temp: %w", err)
	}
	tmpName := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(tmpName) // VACUUM INTO needs a non-existent target
	cleanup := func() { _ = os.Remove(tmpName) }

	if _, err := d.w.ExecContext(ctx, "VACUUM INTO ?", tmpName); err != nil {
		cleanup()
		return fmt.Errorf("db: vacuum into: %w", err)
	}
	f, err := os.Open(tmpName) //nolint:gosec // path built by us inside dir
	if err != nil {
		cleanup()
		return fmt.Errorf("db: backup open: %w", err)
	}
	syncErr := f.Sync()
	_ = f.Close()
	if syncErr != nil {
		cleanup()
		return fmt.Errorf("db: backup sync: %w", syncErr)
	}
	if err := os.Chmod(tmpName, 0o600); err != nil {
		cleanup()
		return fmt.Errorf("db: backup chmod: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		cleanup()
		return fmt.Errorf("db: backup rename: %w", err)
	}
	return nil
}
