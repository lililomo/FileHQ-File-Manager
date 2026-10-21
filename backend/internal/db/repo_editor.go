package db

import (
	"context"

	"kppbwi.app/filemanager/internal/db/sqlc"
)

// EditorRepo stores the per-user editor workspace (SPEC §4.2
// editor_sessions, §8.13 "Tab & sesi"): one row per user.
type EditorRepo interface {
	GetEditorSession(ctx context.Context, userID string) (sqlc.EditorSession, error)
	UpsertEditorSession(ctx context.Context, p sqlc.UpsertEditorSessionParams) (sqlc.EditorSession, error)
	DeleteEditorSession(ctx context.Context, userID string) (int64, error)
}

func (s *Store) GetEditorSession(ctx context.Context, userID string) (sqlc.EditorSession, error) {
	return s.r.GetEditorSession(ctx, userID)
}

func (s *Store) UpsertEditorSession(ctx context.Context, p sqlc.UpsertEditorSessionParams) (sqlc.EditorSession, error) {
	return s.w.UpsertEditorSession(ctx, p)
}

func (s *Store) DeleteEditorSession(ctx context.Context, userID string) (int64, error) {
	return s.w.DeleteEditorSession(ctx, userID)
}

// MediaPositionRepo stores the last playback position per video (SPEC §4.2
// media_positions, §8.14 VideoPlayer resume; Step 17b).
type MediaPositionRepo interface {
	GetMediaPosition(ctx context.Context, p sqlc.GetMediaPositionParams) (sqlc.MediaPosition, error)
	UpsertMediaPosition(ctx context.Context, p sqlc.UpsertMediaPositionParams) (sqlc.MediaPosition, error)
	DeleteMediaPosition(ctx context.Context, p sqlc.DeleteMediaPositionParams) (int64, error)
	PruneMediaPositions(ctx context.Context, p sqlc.PruneMediaPositionsParams) (int64, error)
}

func (s *Store) GetMediaPosition(ctx context.Context, p sqlc.GetMediaPositionParams) (sqlc.MediaPosition, error) {
	return s.r.GetMediaPosition(ctx, p)
}

func (s *Store) UpsertMediaPosition(ctx context.Context, p sqlc.UpsertMediaPositionParams) (sqlc.MediaPosition, error) {
	return s.w.UpsertMediaPosition(ctx, p)
}

func (s *Store) DeleteMediaPosition(ctx context.Context, p sqlc.DeleteMediaPositionParams) (int64, error) {
	return s.w.DeleteMediaPosition(ctx, p)
}

func (s *Store) PruneMediaPositions(ctx context.Context, p sqlc.PruneMediaPositionsParams) (int64, error) {
	return s.w.PruneMediaPositions(ctx, p)
}
