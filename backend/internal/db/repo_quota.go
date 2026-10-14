package db

import (
	"context"

	"kppbwi.app/filemanager/internal/db/sqlc"
)

// QuotaRepo persists the incremental per-user usage counters (SPEC §4.2
// quota_usage, ADR-23). Deltas are applied inside the caller's transaction
// when one is open; counters are clamped at zero by the SQL itself.
type QuotaRepo interface {
	GetQuotaUsage(ctx context.Context, userID string) (sqlc.QuotaUsage, error)
	ApplyQuotaDelta(ctx context.Context, userID string, dUsed, dTrash, dVersions int64, at string) error
	SetQuotaUsage(ctx context.Context, p sqlc.SetQuotaUsageParams) error
	SetQuotaDirty(ctx context.Context, userID, at string) error
	ListDirtyQuotaUsers(ctx context.Context) ([]string, error)
	SumQuotaUsed(ctx context.Context) (int64, error)
}

func (s *Store) GetQuotaUsage(ctx context.Context, userID string) (sqlc.QuotaUsage, error) {
	return s.r.GetQuotaUsage(ctx, userID)
}

func (s *Store) ApplyQuotaDelta(ctx context.Context, userID string, dUsed, dTrash, dVersions int64, at string) error {
	return s.w.ApplyQuotaDelta(ctx, sqlc.ApplyQuotaDeltaParams{UserID: userID, MAX: dUsed, MAX_2: dTrash, MAX_3: dVersions, UpdatedAt: at})
}

func (s *Store) SetQuotaUsage(ctx context.Context, p sqlc.SetQuotaUsageParams) error {
	return s.w.SetQuotaUsage(ctx, p)
}

func (s *Store) SetQuotaDirty(ctx context.Context, userID, at string) error {
	return s.w.SetQuotaDirty(ctx, sqlc.SetQuotaDirtyParams{UserID: userID, UpdatedAt: at})
}

func (s *Store) ListDirtyQuotaUsers(ctx context.Context) ([]string, error) {
	return s.r.ListDirtyQuotaUsers(ctx)
}

func (s *Store) SumQuotaUsed(ctx context.Context) (int64, error) {
	v, err := s.r.SumQuotaUsed(ctx)
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
