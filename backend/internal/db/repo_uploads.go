package db

import (
	"context"

	"kppbwi.app/filemanager/internal/db/sqlc"
)

// UploadRepo persists resumable upload sessions (SPEC §4.2 upload_sessions,
// §6.5). The quota reservation of a user is the sum of active rows.
type UploadRepo interface {
	InsertUploadSession(ctx context.Context, p sqlc.InsertUploadSessionParams) error
	GetUploadSession(ctx context.Context, id string) (sqlc.UploadSession, error)
	GetUserUploadSession(ctx context.Context, userID, id string) (sqlc.UploadSession, error)
	// SetUploadSessionState moves an active row to its final state; it
	// returns 0 rows when the row was not active any more.
	SetUploadSessionState(ctx context.Context, id, state string, finalizedPath *string, at string) (int64, error)
	TouchUploadSession(ctx context.Context, id, at, expiresAt string) error
	SumActiveUploadSizes(ctx context.Context, userID string) (int64, error)
	CountActiveUploadSessions(ctx context.Context, userID string) (int64, error)
	ListExpiredUploadSessions(ctx context.Context, now string, limit int64) ([]sqlc.UploadSession, error)
	ListActiveUploadIDs(ctx context.Context) ([]string, error)
	DeleteUploadSessionsBefore(ctx context.Context, before string) (int64, error)
}

func (s *Store) InsertUploadSession(ctx context.Context, p sqlc.InsertUploadSessionParams) error {
	return s.w.InsertUploadSession(ctx, p)
}

func (s *Store) GetUploadSession(ctx context.Context, id string) (sqlc.UploadSession, error) {
	return s.r.GetUploadSession(ctx, id)
}

func (s *Store) GetUserUploadSession(ctx context.Context, userID, id string) (sqlc.UploadSession, error) {
	return s.r.GetUserUploadSession(ctx, sqlc.GetUserUploadSessionParams{ID: id, UserID: userID})
}

func (s *Store) SetUploadSessionState(ctx context.Context, id, state string, finalizedPath *string, at string) (int64, error) {
	return s.w.SetUploadSessionState(ctx, sqlc.SetUploadSessionStateParams{State: state, FinalizedPath: finalizedPath, UpdatedAt: at, ID: id})
}

func (s *Store) TouchUploadSession(ctx context.Context, id, at, expiresAt string) error {
	return s.w.TouchUploadSession(ctx, sqlc.TouchUploadSessionParams{UpdatedAt: at, ExpiresAt: expiresAt, ID: id})
}

func (s *Store) SumActiveUploadSizes(ctx context.Context, userID string) (int64, error) {
	v, err := s.r.SumActiveUploadSizes(ctx, userID)
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

func (s *Store) CountActiveUploadSessions(ctx context.Context, userID string) (int64, error) {
	return s.r.CountActiveUploadSessions(ctx, userID)
}

func (s *Store) ListExpiredUploadSessions(ctx context.Context, now string, limit int64) ([]sqlc.UploadSession, error) {
	return s.r.ListExpiredUploadSessions(ctx, sqlc.ListExpiredUploadSessionsParams{Now: now, MaxRows: limit})
}

func (s *Store) ListActiveUploadIDs(ctx context.Context) ([]string, error) {
	return s.r.ListActiveUploadIDs(ctx)
}

func (s *Store) DeleteUploadSessionsBefore(ctx context.Context, before string) (int64, error) {
	return s.w.DeleteUploadSessionsBefore(ctx, before)
}
