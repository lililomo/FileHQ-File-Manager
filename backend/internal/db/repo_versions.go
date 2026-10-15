package db

import (
	"context"

	"kppbwi.app/filemanager/internal/db/sqlc"
)

// VersionRepo reads/deletes file_versions rows (SPEC §4.2 file_versions,
// §8.13). InsertFileVersion/ListFileVersions live in RefRepo (Step 9a).
type VersionRepo interface {
	GetFileVersion(ctx context.Context, userID, id string) (sqlc.FileVersion, error)
	ListFileVersionsByPath(ctx context.Context, userID, path string) ([]sqlc.FileVersion, error)
	LatestFileVersion(ctx context.Context, userID, path string) (sqlc.FileVersion, error)
	DeleteFileVersion(ctx context.Context, userID, id string) (int64, error)
	// ListFileVersionsPrefix lists the versions of path and everything below it (Step 16b: blob cleanup on delete).
	ListFileVersionsPrefix(ctx context.Context, userID, path string) ([]sqlc.FileVersion, error)
}

func (s *Store) GetFileVersion(ctx context.Context, userID, id string) (sqlc.FileVersion, error) {
	return s.r.GetFileVersion(ctx, sqlc.GetFileVersionParams{ID: id, UserID: userID})
}

func (s *Store) ListFileVersionsByPath(ctx context.Context, userID, path string) ([]sqlc.FileVersion, error) {
	return s.r.ListFileVersionsByPath(ctx, sqlc.ListFileVersionsByPathParams{UserID: userID, Path: path})
}

func (s *Store) LatestFileVersion(ctx context.Context, userID, path string) (sqlc.FileVersion, error) {
	return s.r.LatestFileVersion(ctx, sqlc.LatestFileVersionParams{UserID: userID, Path: path})
}

func (s *Store) DeleteFileVersion(ctx context.Context, userID, id string) (int64, error) {
	return s.w.DeleteFileVersion(ctx, sqlc.DeleteFileVersionParams{ID: id, UserID: userID})
}

func (s *Store) ListFileVersionsPrefix(ctx context.Context, userID, path string) ([]sqlc.FileVersion, error) {
	prefix, plen, _ := refPrefix(path)
	return s.r.ListFileVersionsPrefix(ctx, sqlc.ListFileVersionsPrefixParams{UserID: userID, Path: path, PrefixLen: plen, Prefix: prefix})
}
