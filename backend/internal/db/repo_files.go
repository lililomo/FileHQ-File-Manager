package db

import (
	"context"

	"kppbwi.app/filemanager/internal/db/sqlc"
)

// RefTable names a path-keyed metadata table whose rows follow rename/move
// (SPEC §5.7). shares joins in Step 18a through files.RegisterReferenceUpdater.
type RefTable string

// Path-keyed tables managed by RefRepo.
const (
	RefFavorites    RefTable = "favorites"
	RefPins         RefTable = "pins"
	RefFileNotes    RefTable = "file_notes"
	RefFileTags     RefTable = "file_tags"
	RefFileVersions RefTable = "file_versions"
)

// RefTables lists every table RefRepo rewrites, in a fixed order.
var RefTables = []RefTable{RefFavorites, RefPins, RefFileNotes, RefFileTags, RefFileVersions}

// TrashRepo persists Recycle Bin rows (SPEC §4.2 trash_items).
type TrashRepo interface {
	InsertTrashItem(ctx context.Context, p sqlc.InsertTrashItemParams) error
	GetTrashItem(ctx context.Context, userID, id string) (sqlc.TrashItem, error)
	ListTrashByBatch(ctx context.Context, userID, batchID string) ([]sqlc.TrashItem, error)
	ListTrashItems(ctx context.Context, userID string) ([]sqlc.TrashItem, error)
	SetTrashRestored(ctx context.Context, userID, id, at string) (int64, error)
	SetTrashPurged(ctx context.Context, userID, id, at string) (int64, error)
	DeleteTrashItemRow(ctx context.Context, userID, id string) error
	SumTrashBytes(ctx context.Context, userID string) (int64, error)
	ListExpiredTrash(ctx context.Context, now string, maxRows int64) ([]sqlc.TrashItem, error)
	ListTrashItemsByIDs(ctx context.Context, userID string, ids []string) ([]sqlc.TrashItem, error)
	CountTrashActive(ctx context.Context, userID string) (int64, error)
	DeletePurgedTrashRowsBefore(ctx context.Context, before string) (int64, error)
	UpdateTrashItemSize(ctx context.Context, userID, id string, sizeBytes, itemCount int64) error
}

// ActivityRepo is the per-user activity journal (SPEC §4.2 activity_logs).
type ActivityRepo interface {
	InsertActivity(ctx context.Context, p sqlc.InsertActivityParams) (int64, error)
	ListActivity(ctx context.Context, userID string, beforeID, limit int64) ([]sqlc.ActivityLog, error)
	DeleteActivityOlderThan(ctx context.Context, before string) (int64, error)
}

// RefRepo rewrites and deletes path references in the metadata tables.
// RewriteRefs moves path and everything below it from old to new;
// DeleteRefs removes path and everything below it.
type RefRepo interface {
	RewriteRefs(ctx context.Context, table RefTable, userID, oldPath, newPath string) (int64, error)
	DeleteRefs(ctx context.Context, table RefTable, userID, path string) (int64, error)
	UpsertFavorite(ctx context.Context, userID, path, at string) error
	ListFavorites(ctx context.Context, userID string) ([]sqlc.Favorite, error)
	UpsertPin(ctx context.Context, userID, path, at string) error
	ListPins(ctx context.Context, userID string) ([]sqlc.Pin, error)
	UpsertFileNote(ctx context.Context, userID, path, note, at string) error
	ListFileNotes(ctx context.Context, userID string) ([]sqlc.FileNote, error)
	UpsertFileTag(ctx context.Context, userID, path, color, at string) error
	ListFileTags(ctx context.Context, userID string) ([]sqlc.FileTag, error)
	InsertFileVersion(ctx context.Context, p sqlc.InsertFileVersionParams) error
	ListFileVersions(ctx context.Context, userID string) ([]sqlc.FileVersion, error)
}

// --- trash_items ---

func (s *Store) InsertTrashItem(ctx context.Context, p sqlc.InsertTrashItemParams) error {
	return s.w.InsertTrashItem(ctx, p)
}

func (s *Store) GetTrashItem(ctx context.Context, userID, id string) (sqlc.TrashItem, error) {
	return s.r.GetTrashItem(ctx, sqlc.GetTrashItemParams{UserID: userID, ID: id})
}

func (s *Store) ListTrashByBatch(ctx context.Context, userID, batchID string) ([]sqlc.TrashItem, error) {
	return s.r.ListTrashByBatch(ctx, sqlc.ListTrashByBatchParams{UserID: userID, BatchID: &batchID})
}

func (s *Store) ListTrashItems(ctx context.Context, userID string) ([]sqlc.TrashItem, error) {
	return s.r.ListTrashItems(ctx, userID)
}

func (s *Store) SetTrashRestored(ctx context.Context, userID, id, at string) (int64, error) {
	return s.w.SetTrashRestored(ctx, sqlc.SetTrashRestoredParams{RestoredAt: &at, UserID: userID, ID: id})
}

func (s *Store) SetTrashPurged(ctx context.Context, userID, id, at string) (int64, error) {
	return s.w.SetTrashPurged(ctx, sqlc.SetTrashPurgedParams{PurgedAt: &at, UserID: userID, ID: id})
}

func (s *Store) DeleteTrashItemRow(ctx context.Context, userID, id string) error {
	return s.w.DeleteTrashItemRow(ctx, sqlc.DeleteTrashItemRowParams{UserID: userID, ID: id})
}

func (s *Store) SumTrashBytes(ctx context.Context, userID string) (int64, error) {
	v, err := s.r.SumTrashBytes(ctx, userID)
	if err != nil {
		return 0, err
	}
	switch n := v.(type) {
	case int64:
		return n, nil
	case float64:
		return int64(n), nil
	}
	return 0, nil
}

func (s *Store) ListExpiredTrash(ctx context.Context, now string, maxRows int64) ([]sqlc.TrashItem, error) {
	return s.r.ListExpiredTrash(ctx, sqlc.ListExpiredTrashParams{Now: &now, MaxRows: maxRows})
}

func (s *Store) ListTrashItemsByIDs(ctx context.Context, userID string, ids []string) ([]sqlc.TrashItem, error) {
	if len(ids) == 0 {
		return []sqlc.TrashItem{}, nil
	}
	return s.r.ListTrashItemsByIDs(ctx, sqlc.ListTrashItemsByIDsParams{UserID: userID, Ids: ids})
}

func (s *Store) CountTrashActive(ctx context.Context, userID string) (int64, error) {
	return s.r.CountTrashActive(ctx, userID)
}

func (s *Store) DeletePurgedTrashRowsBefore(ctx context.Context, before string) (int64, error) {
	return s.w.DeletePurgedTrashRowsBefore(ctx, &before)
}

func (s *Store) UpdateTrashItemSize(ctx context.Context, userID, id string, sizeBytes, itemCount int64) error {
	return s.w.UpdateTrashItemSize(ctx, sqlc.UpdateTrashItemSizeParams{SizeBytes: sizeBytes, ItemCount: itemCount, UserID: userID, ID: id})
}

// --- activity_logs ---

func (s *Store) InsertActivity(ctx context.Context, p sqlc.InsertActivityParams) (int64, error) {
	return s.w.InsertActivity(ctx, p)
}

func (s *Store) ListActivity(ctx context.Context, userID string, beforeID, limit int64) ([]sqlc.ActivityLog, error) {
	if beforeID <= 0 {
		beforeID = 1<<62 - 1
	}
	return s.r.ListActivity(ctx, sqlc.ListActivityParams{UserID: userID, ID: beforeID, Limit: limit})
}

func (s *Store) DeleteActivityOlderThan(ctx context.Context, before string) (int64, error) {
	return s.w.DeleteActivityOlderThan(ctx, before)
}

// --- path references ---

// refPrefix returns the (prefix, prefixLen, fromPos) triple for path.
func refPrefix(p string) (prefix string, prefixLen, fromPos int64) {
	prefix = p + "/"
	return prefix, int64(len(prefix)), int64(len(p) + 1)
}

func (s *Store) RewriteRefs(ctx context.Context, table RefTable, userID, oldPath, newPath string) (int64, error) {
	prefix, plen, from := refPrefix(oldPath)
	switch table {
	case RefFavorites:
		return s.w.RewriteFavorites(ctx, sqlc.RewriteFavoritesParams{NewPath: newPath, FromPos: from, UserID: userID, OldPath: oldPath, PrefixLen: plen, Prefix: prefix})
	case RefPins:
		return s.w.RewritePins(ctx, sqlc.RewritePinsParams{NewPath: newPath, FromPos: from, UserID: userID, OldPath: oldPath, PrefixLen: plen, Prefix: prefix})
	case RefFileNotes:
		return s.w.RewriteFileNotes(ctx, sqlc.RewriteFileNotesParams{NewPath: newPath, FromPos: from, UserID: userID, OldPath: oldPath, PrefixLen: plen, Prefix: prefix})
	case RefFileTags:
		return s.w.RewriteFileTags(ctx, sqlc.RewriteFileTagsParams{NewPath: newPath, FromPos: from, UserID: userID, OldPath: oldPath, PrefixLen: plen, Prefix: prefix})
	case RefFileVersions:
		return s.w.RewriteFileVersions(ctx, sqlc.RewriteFileVersionsParams{NewPath: newPath, FromPos: from, UserID: userID, OldPath: oldPath, PrefixLen: plen, Prefix: prefix})
	}
	return 0, nil
}

func (s *Store) DeleteRefs(ctx context.Context, table RefTable, userID, path string) (int64, error) {
	prefix, plen, _ := refPrefix(path)
	switch table {
	case RefFavorites:
		return s.w.DeleteFavoritesPrefix(ctx, sqlc.DeleteFavoritesPrefixParams{UserID: userID, Path: path, PrefixLen: plen, Prefix: prefix})
	case RefPins:
		return s.w.DeletePinsPrefix(ctx, sqlc.DeletePinsPrefixParams{UserID: userID, Path: path, PrefixLen: plen, Prefix: prefix})
	case RefFileNotes:
		return s.w.DeleteFileNotesPrefix(ctx, sqlc.DeleteFileNotesPrefixParams{UserID: userID, Path: path, PrefixLen: plen, Prefix: prefix})
	case RefFileTags:
		return s.w.DeleteFileTagsPrefix(ctx, sqlc.DeleteFileTagsPrefixParams{UserID: userID, Path: path, PrefixLen: plen, Prefix: prefix})
	case RefFileVersions:
		return s.w.DeleteFileVersionsPrefix(ctx, sqlc.DeleteFileVersionsPrefixParams{UserID: userID, Path: path, PrefixLen: plen, Prefix: prefix})
	}
	return 0, nil
}

func (s *Store) UpsertFavorite(ctx context.Context, userID, path, at string) error {
	return s.w.UpsertFavorite(ctx, sqlc.UpsertFavoriteParams{UserID: userID, Path: path, CreatedAt: at})
}
func (s *Store) ListFavorites(ctx context.Context, userID string) ([]sqlc.Favorite, error) {
	return s.r.ListFavorites(ctx, userID)
}
func (s *Store) UpsertPin(ctx context.Context, userID, path, at string) error {
	return s.w.UpsertPin(ctx, sqlc.UpsertPinParams{UserID: userID, Path: path, CreatedAt: at})
}
func (s *Store) ListPins(ctx context.Context, userID string) ([]sqlc.Pin, error) {
	return s.r.ListPins(ctx, userID)
}
func (s *Store) UpsertFileNote(ctx context.Context, userID, path, note, at string) error {
	return s.w.UpsertFileNote(ctx, sqlc.UpsertFileNoteParams{UserID: userID, Path: path, Note: note, UpdatedAt: at})
}
func (s *Store) ListFileNotes(ctx context.Context, userID string) ([]sqlc.FileNote, error) {
	return s.r.ListFileNotes(ctx, userID)
}
func (s *Store) UpsertFileTag(ctx context.Context, userID, path, color, at string) error {
	return s.w.UpsertFileTag(ctx, sqlc.UpsertFileTagParams{UserID: userID, Path: path, Color: color, UpdatedAt: at})
}
func (s *Store) ListFileTags(ctx context.Context, userID string) ([]sqlc.FileTag, error) {
	return s.r.ListFileTags(ctx, userID)
}
func (s *Store) InsertFileVersion(ctx context.Context, p sqlc.InsertFileVersionParams) error {
	return s.w.InsertFileVersion(ctx, p)
}
func (s *Store) ListFileVersions(ctx context.Context, userID string) ([]sqlc.FileVersion, error) {
	return s.r.ListFileVersions(ctx, userID)
}
