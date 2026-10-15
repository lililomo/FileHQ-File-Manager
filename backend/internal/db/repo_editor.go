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
