// Package trash moves items into the per-user Recycle Bin under
// DATA_DIR/trash/<user_id>/<trash_item_id> and back (SPEC §8.6, ADR-17).
// Only the move/restore mechanism lives here; listing, purge and the UI
// arrive in Step 10a.
package trash

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"sync"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/fsjail"
)

// Kinds accepted by trash_items.kind (SPEC §4.2).
var Kinds = map[string]bool{"file": true, "folder": true, "image": true, "document": true, "video": true, "audio": true, "archive": true, "other": true}

// SizeWalkBudget bounds the size/count walk of a folder being deleted; when
// exceeded the partial numbers are stored (SPEC §8.5 "batas 20 s").
const SizeWalkBudget = 20 * time.Second

// Errors.
var (
	ErrNotFound = errors.New("trash: item not found")
	ErrGone     = errors.New("trash: item already restored or purged")
)

// Store moves items to and from the Recycle Bin.
type Store struct {
	dataDir   string
	crossFS   bool
	retention time.Duration // 0 = never expires
	repo      db.Repository
	logger    *slog.Logger
	now       func() time.Time

	mu  sync.Mutex
	sys map[string]*fsjail.SysDir
}

// New builds a Store. retentionDays 0 disables expiry; crossFS only
// changes logging (fsjail.MoveOut handles EXDEV itself).
func New(dataDir string, crossFS bool, retentionDays int, repo db.Repository, logger *slog.Logger, now func() time.Time) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Store{dataDir: dataDir, crossFS: crossFS, retention: time.Duration(retentionDays) * 24 * time.Hour,
		repo: repo, logger: logger, now: now, sys: map[string]*fsjail.SysDir{}}
}

// Sys returns the user's trash directory handle (created on first use).
func (s *Store) Sys(userID string) (*fsjail.SysDir, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.sys[userID]; ok {
		return d, nil
	}
	d, err := fsjail.OpenSys(s.dataDir, path.Join("trash", userID))
	if err != nil {
		return nil, err
	}
	s.sys[userID] = d
	return d, nil
}

// Close releases every cached handle.
func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, d := range s.sys {
		_ = d.Close()
		delete(s.sys, id)
	}
}

// Measure walks p (a folder) with SizeWalkBudget and returns bytes and the
// number of entries below it; a file returns its own size and 0 entries.
func (s *Store) Measure(ctx context.Context, j *fsjail.Jail, info fsjail.Info) (bytes, count int64) {
	if !info.IsDir {
		return info.Size, 0
	}
	wctx, cancel := context.WithTimeout(ctx, SizeWalkBudget)
	defer cancel()
	var last fsjail.DirStats
	st, err := j.DirSize(wctx, info.Path, func(files, dirs, b int64) { last = fsjail.DirStats{Files: files, Dirs: dirs, Bytes: b} })
	if err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			s.logger.Warn("trash size walk failed", slog.String("path", string(info.Path)), slog.String("error", err.Error()))
		}
		st = last
	}
	return st.Bytes, st.Files + st.Dirs
}

// MoveToTrash moves p into the user's Recycle Bin: the trash_items row is
// written first, then the item is renamed into DATA_DIR/trash/<uid>/<id>
// (SPEC §4.3 ordering: DB soft, then FS; the row is removed if the move
// fails). kind must be one of Kinds; batchID groups one request.
func (s *Store) MoveToTrash(ctx context.Context, j *fsjail.Jail, userID string, p fsjail.VPath, kind, batchID string) (sqlc.TrashItem, error) {
	if !Kinds[kind] {
		kind = "other"
	}
	info, err := j.Lstat(p)
	if err != nil {
		return sqlc.TrashItem{}, err
	}
	sys, err := s.Sys(userID)
	if err != nil {
		return sqlc.TrashItem{}, err
	}
	bytes, count := s.Measure(ctx, j, info)
	now := s.now()
	row := sqlc.TrashItem{
		ID: db.NewID(), UserID: userID, OriginalPath: string(p), Name: p.Base(), SizeBytes: bytes, ItemCount: count,
		Kind: kind, DeletedAt: db.FormatTime(now),
	}
	if info.IsDir {
		row.IsDir = 1
		if kind != "folder" {
			row.Kind = "folder"
		}
	}
	if s.retention > 0 {
		row.ExpiresAt = db.Ptr(db.FormatTime(now.Add(s.retention)))
	}
	if batchID != "" {
		row.BatchID = &batchID
	}
	if err := s.repo.InsertTrashItem(ctx, sqlc.InsertTrashItemParams{
		ID: row.ID, UserID: row.UserID, OriginalPath: row.OriginalPath, Name: row.Name, IsDir: row.IsDir,
		SizeBytes: row.SizeBytes, ItemCount: row.ItemCount, Kind: row.Kind, DeletedAt: row.DeletedAt,
		ExpiresAt: row.ExpiresAt, BatchID: row.BatchID,
	}); err != nil {
		return sqlc.TrashItem{}, fmt.Errorf("trash: insert row: %w", err)
	}
	if s.crossFS {
		s.logger.Warn("trash is on another filesystem; copying instead of renaming", slog.String("path", string(p)))
	}
	if err := j.MoveOut(ctx, p, sys, row.ID); err != nil {
		if derr := s.repo.DeleteTrashItemRow(ctx, userID, row.ID); derr != nil {
			s.logger.Error("trash row cleanup failed", slog.String("id", row.ID), slog.String("error", derr.Error()))
		}
		return sqlc.TrashItem{}, err
	}
	return row, nil
}

// Restore moves trash item id back into the jail at dst ("" = original
// path). Missing parent folders are created; an occupied destination fails
// with fsjail.ErrExists so the caller can ask for a conflict policy.
func (s *Store) Restore(ctx context.Context, j *fsjail.Jail, userID, id string, dst fsjail.VPath) (sqlc.TrashItem, error) {
	row, err := s.repo.GetTrashItem(ctx, userID, id)
	if err != nil {
		if db.IsNotFound(err) {
			return sqlc.TrashItem{}, ErrNotFound
		}
		return sqlc.TrashItem{}, err
	}
	if row.RestoredAt != nil || row.PurgedAt != nil {
		return row, ErrGone
	}
	if dst == "" {
		dst, err = fsjail.Clean(row.OriginalPath)
		if err != nil {
			return row, err
		}
	}
	sys, err := s.Sys(userID)
	if err != nil {
		return row, err
	}
	if dir := dst.Dir(); !dir.IsRoot() {
		if err := j.MkdirAll(dir); err != nil {
			return row, err
		}
	}
	if err := j.MoveIn(ctx, sys, id, dst); err != nil {
		return row, err
	}
	if _, err := s.repo.SetTrashRestored(ctx, userID, id, db.FormatTime(s.now())); err != nil {
		s.logger.Error("trash restored flag failed", slog.String("id", id), slog.String("error", err.Error()))
	}
	return row, nil
}
