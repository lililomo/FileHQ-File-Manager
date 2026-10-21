package db

import (
	"context"

	"kppbwi.app/filemanager/internal/db/sqlc"
)

// ShareRepo persists public links and their access journal (SPEC §4.2
// shares, share_access_logs; Step 18a). share_recipients arrives in 18b.
type ShareRepo interface {
	InsertShare(ctx context.Context, p sqlc.InsertShareParams) (sqlc.Share, error)
	GetShare(ctx context.Context, ownerID, id string) (sqlc.Share, error)
	GetShareByID(ctx context.Context, id string) (sqlc.Share, error)
	GetShareByTokenHash(ctx context.Context, hash string) (sqlc.Share, error)
	GetShareBySlug(ctx context.Context, slug string) (sqlc.Share, error)
	ListSharesByOwner(ctx context.Context, p sqlc.ListSharesByOwnerParams) ([]sqlc.Share, error)
	ListSharesByOwnerPath(ctx context.Context, ownerID, path string) ([]sqlc.Share, error)
	ListSharesInDir(ctx context.Context, ownerID, dir string) ([]sqlc.ListSharesInDirRow, error)
	ListSharesUnderPath(ctx context.Context, ownerID, path string) ([]sqlc.Share, error)
	UpdateShare(ctx context.Context, p sqlc.UpdateShareParams) (sqlc.Share, error)
	SetShareToken(ctx context.Context, p sqlc.SetShareTokenParams) (int64, error)
	SetShareDisabled(ctx context.Context, p sqlc.SetShareDisabledParams) (int64, error)
	SetShareEnabled(ctx context.Context, p sqlc.SetShareEnabledParams) (int64, error)
	SetShareBroken(ctx context.Context, id string, brokenAt *string) error
	DeleteShare(ctx context.Context, ownerID, id string) (int64, error)
	IncrementShareView(ctx context.Context, id, at string) error
	TouchShareAccess(ctx context.Context, id, at string) error
	IncrementShareDownload(ctx context.Context, id, at string) (int64, error)
	RewriteSharesPath(ctx context.Context, ownerID, oldPath, newPath, at string) (int64, error)
	DeleteSharesPrefix(ctx context.Context, ownerID, path string) (int64, error)
	ListSharesForCheck(ctx context.Context, afterID string, limit int64) ([]sqlc.ListSharesForCheckRow, error)
	InsertShareAccessLog(ctx context.Context, p sqlc.InsertShareAccessLogParams) error
	ListShareAccessLogs(ctx context.Context, shareID string, beforeID, limit int64) ([]sqlc.ShareAccessLog, error)
	CountShareAccessLogs(ctx context.Context, shareID, action, since string) (int64, error)
	DeleteShareAccessLogsBefore(ctx context.Context, before string) (int64, error)
}

func (s *Store) InsertShare(ctx context.Context, p sqlc.InsertShareParams) (sqlc.Share, error) {
	return s.w.InsertShare(ctx, p)
}

func (s *Store) GetShare(ctx context.Context, ownerID, id string) (sqlc.Share, error) {
	return s.r.GetShare(ctx, sqlc.GetShareParams{ID: id, OwnerUserID: ownerID})
}

func (s *Store) GetShareByID(ctx context.Context, id string) (sqlc.Share, error) {
	return s.r.GetShareByID(ctx, id)
}

func (s *Store) GetShareByTokenHash(ctx context.Context, hash string) (sqlc.Share, error) {
	return s.r.GetShareByTokenHash(ctx, &hash)
}

func (s *Store) GetShareBySlug(ctx context.Context, slug string) (sqlc.Share, error) {
	return s.r.GetShareBySlug(ctx, &slug)
}

func (s *Store) ListSharesByOwner(ctx context.Context, p sqlc.ListSharesByOwnerParams) ([]sqlc.Share, error) {
	return s.r.ListSharesByOwner(ctx, p)
}

func (s *Store) ListSharesByOwnerPath(ctx context.Context, ownerID, path string) ([]sqlc.Share, error) {
	return s.r.ListSharesByOwnerPath(ctx, sqlc.ListSharesByOwnerPathParams{OwnerUserID: ownerID, Path: path})
}

func (s *Store) ListSharesInDir(ctx context.Context, ownerID, dir string) ([]sqlc.ListSharesInDirRow, error) {
	p, l, f := dirPrefix(dir)
	return s.r.ListSharesInDir(ctx, sqlc.ListSharesInDirParams{OwnerUserID: ownerID, PrefixLen: l, Prefix: p, FromPos: f})
}

func (s *Store) ListSharesUnderPath(ctx context.Context, ownerID, path string) ([]sqlc.Share, error) {
	prefix, plen, _ := refPrefix(path)
	return s.r.ListSharesUnderPath(ctx, sqlc.ListSharesUnderPathParams{OwnerUserID: ownerID, Path: path, PrefixLen: plen, Prefix: prefix})
}

func (s *Store) UpdateShare(ctx context.Context, p sqlc.UpdateShareParams) (sqlc.Share, error) {
	return s.w.UpdateShare(ctx, p)
}

func (s *Store) SetShareToken(ctx context.Context, p sqlc.SetShareTokenParams) (int64, error) {
	return s.w.SetShareToken(ctx, p)
}

func (s *Store) SetShareDisabled(ctx context.Context, p sqlc.SetShareDisabledParams) (int64, error) {
	return s.w.SetShareDisabled(ctx, p)
}

func (s *Store) SetShareEnabled(ctx context.Context, p sqlc.SetShareEnabledParams) (int64, error) {
	return s.w.SetShareEnabled(ctx, p)
}

func (s *Store) SetShareBroken(ctx context.Context, id string, brokenAt *string) error {
	return s.w.SetShareBroken(ctx, sqlc.SetShareBrokenParams{BrokenAt: brokenAt, ID: id})
}

func (s *Store) DeleteShare(ctx context.Context, ownerID, id string) (int64, error) {
	return s.w.DeleteShare(ctx, sqlc.DeleteShareParams{ID: id, OwnerUserID: ownerID})
}

func (s *Store) IncrementShareView(ctx context.Context, id, at string) error {
	return s.w.IncrementShareView(ctx, sqlc.IncrementShareViewParams{LastAccessAt: &at, ID: id})
}

func (s *Store) TouchShareAccess(ctx context.Context, id, at string) error {
	return s.w.TouchShareAccess(ctx, sqlc.TouchShareAccessParams{LastAccessAt: &at, ID: id})
}

func (s *Store) IncrementShareDownload(ctx context.Context, id, at string) (int64, error) {
	return s.w.IncrementShareDownload(ctx, sqlc.IncrementShareDownloadParams{At: &at, ID: id})
}

func (s *Store) RewriteSharesPath(ctx context.Context, ownerID, oldPath, newPath, at string) (int64, error) {
	prefix, plen, from := refPrefix(oldPath)
	return s.w.RewriteSharesPath(ctx, sqlc.RewriteSharesPathParams{
		NewPath: newPath, FromPos: from, UpdatedAt: at, OwnerUserID: ownerID, OldPath: oldPath, PrefixLen: plen, Prefix: prefix,
	})
}

func (s *Store) DeleteSharesPrefix(ctx context.Context, ownerID, path string) (int64, error) {
	prefix, plen, _ := refPrefix(path)
	return s.w.DeleteSharesPrefix(ctx, sqlc.DeleteSharesPrefixParams{OwnerUserID: ownerID, Path: path, PrefixLen: plen, Prefix: prefix})
}

func (s *Store) ListSharesForCheck(ctx context.Context, afterID string, limit int64) ([]sqlc.ListSharesForCheckRow, error) {
	return s.r.ListSharesForCheck(ctx, sqlc.ListSharesForCheckParams{AfterID: afterID, Lim: limit})
}

func (s *Store) InsertShareAccessLog(ctx context.Context, p sqlc.InsertShareAccessLogParams) error {
	return s.w.InsertShareAccessLog(ctx, p)
}

func (s *Store) ListShareAccessLogs(ctx context.Context, shareID string, beforeID, limit int64) ([]sqlc.ShareAccessLog, error) {
	return s.r.ListShareAccessLogs(ctx, sqlc.ListShareAccessLogsParams{ShareID: shareID, BeforeID: beforeID, Lim: limit})
}

func (s *Store) CountShareAccessLogs(ctx context.Context, shareID, action, since string) (int64, error) {
	return s.r.CountShareAccessLogs(ctx, sqlc.CountShareAccessLogsParams{ShareID: shareID, Action: action, At: since})
}

func (s *Store) DeleteShareAccessLogsBefore(ctx context.Context, before string) (int64, error) {
	return s.w.DeleteShareAccessLogsBefore(ctx, before)
}
