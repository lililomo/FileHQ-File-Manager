package db

import (
	"context"

	"kppbwi.app/filemanager/internal/db/sqlc"
)

// SearchRepo stores the per-user search history (SPEC §4.2 search_history,
// §8.12): the newest MaxSearchHistory rows per user are kept.
type SearchRepo interface {
	// RecordSearch replaces an identical (kind, query, scope) row, inserts
	// the new one and prunes the user's history to keep rows.
	RecordSearch(ctx context.Context, p sqlc.InsertSearchHistoryParams, keep int64) (sqlc.SearchHistory, error)
	ListSearchHistory(ctx context.Context, userID, kind string, limit int64) ([]sqlc.SearchHistory, error)
	DeleteSearchHistory(ctx context.Context, userID string) (int64, error)
}

func (s *Store) RecordSearch(ctx context.Context, p sqlc.InsertSearchHistoryParams, keep int64) (sqlc.SearchHistory, error) {
	var row sqlc.SearchHistory
	err := s.WithTx(ctx, func(tx Repository) error {
		st := tx.(*Store) //nolint:forcetypeassert // WithTx always hands out a *Store
		if _, err := st.w.DeleteSearchHistoryDuplicate(ctx, sqlc.DeleteSearchHistoryDuplicateParams{UserID: p.UserID, Kind: p.Kind, Query: p.Query, ScopePath: p.ScopePath}); err != nil {
			return err
		}
		r, err := st.w.InsertSearchHistory(ctx, p)
		if err != nil {
			return err
		}
		row = r
		if keep > 0 {
			if _, err := st.w.PruneSearchHistory(ctx, sqlc.PruneSearchHistoryParams{UserID: p.UserID, Offset: keep}); err != nil {
				return err
			}
		}
		return nil
	})
	return row, err
}

func (s *Store) ListSearchHistory(ctx context.Context, userID, kind string, limit int64) ([]sqlc.SearchHistory, error) {
	return s.r.ListSearchHistory(ctx, sqlc.ListSearchHistoryParams{UserID: userID, Column2: kind, Limit: limit})
}

func (s *Store) DeleteSearchHistory(ctx context.Context, userID string) (int64, error) {
	return s.w.DeleteSearchHistory(ctx, userID)
}
