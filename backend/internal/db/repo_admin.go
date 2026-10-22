package db

import (
	"context"
	"errors"
	"fmt"

	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Step 19a: admin queries (user list cursor, dashboard aggregates, purge).

// TopUser is one row of the dashboard "top users" table.
type TopUser struct {
	ID          string
	Username    string
	DisplayName string
	QuotaBytes  int64
	UsedBytes   int64
}

// AdminRepo groups the admin-only queries.
type AdminRepo interface {
	// ListUsersPage lists live accounts after username `after` (keyset cursor).
	ListUsersPage(ctx context.Context, f UserFilter, after string, limit int64) ([]sqlc.User, error)
	SumUserQuota(ctx context.Context) (int64, error)
	TopQuotaUsers(ctx context.Context, limit int64) ([]TopUser, error)
	CountActivePublicShares(ctx context.Context, now string) (int64, error)
	ListDeletedUsersBefore(ctx context.Context, before string) ([]sqlc.User, error)
	// PurgeUserData deletes every row that belongs to the user in the
	// per-user tables (the users row itself is kept soft-deleted, SPEC
	// §8.17 "purge"). It returns the rows removed per table.
	PurgeUserData(ctx context.Context, userID, username string) (map[string]int64, error)
}

func (s *Store) ListUsersPage(ctx context.Context, f UserFilter, after string, limit int64) ([]sqlc.User, error) {
	return s.r.ListUsersPage(ctx, sqlc.ListUsersPageParams{After: after, Status: nullable(f.Status), Role: nullable(f.Role), Q: nullable(f.Query), Limit: limit})
}

func (s *Store) SumUserQuota(ctx context.Context) (int64, error) {
	v, err := s.r.SumUserQuota(ctx)
	if err != nil {
		return 0, err
	}
	return asInt64(v), nil
}

func (s *Store) TopQuotaUsers(ctx context.Context, limit int64) ([]TopUser, error) {
	rows, err := s.r.TopQuotaUsers(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]TopUser, 0, len(rows))
	for _, r := range rows {
		out = append(out, TopUser{ID: r.ID, Username: r.Username, DisplayName: r.DisplayName, QuotaBytes: r.QuotaBytes, UsedBytes: asInt64(r.EffectiveBytes)})
	}
	return out, nil
}

func (s *Store) CountActivePublicShares(ctx context.Context, now string) (int64, error) {
	return s.r.CountActivePublicShares(ctx, &now)
}

func (s *Store) ListDeletedUsersBefore(ctx context.Context, before string) ([]sqlc.User, error) {
	return s.r.ListDeletedUsersBefore(ctx, &before)
}

// userTables lists every table with a per-user foreign key (migrations
// 0001–0013) and its column. Tables that hang off these through their own
// cascade (task_logs, share_access_logs, share_recipients of the user's
// shares) follow automatically.
var userTables = []struct{ table, column string }{
	{"sessions", "user_id"},
	{"user_preferences", "user_id"},
	{"folder_view_prefs", "user_id"},
	{"quota_usage", "user_id"},
	{"editor_sessions", "user_id"},
	{"trash_items", "user_id"},
	{"activity_logs", "user_id"},
	{"favorites", "user_id"},
	{"pins", "user_id"},
	{"file_notes", "user_id"},
	{"file_tags", "user_id"},
	{"file_versions", "user_id"},
	{"media_metadata", "user_id"},
	{"search_history", "user_id"},
	{"tasks", "user_id"},
	{"notifications", "user_id"},
	{"upload_sessions", "user_id"},
	{"media_positions", "user_id"},
	{"shares", "owner_user_id"},
	{"share_recipients", "recipient_user_id"},
}

func (s *Store) PurgeUserData(ctx context.Context, userID, username string) (map[string]int64, error) {
	if s.db == nil {
		return nil, errors.New("db: PurgeUserData cannot run inside a transaction")
	}
	out := map[string]int64{}
	for _, t := range userTables {
		res, err := s.db.w.ExecContext(ctx, fmt.Sprintf("DELETE FROM %s WHERE %s = ?", t.table, t.column), userID) //nolint:gosec // table/column come from the fixed list above
		if err != nil {
			return out, fmt.Errorf("db: purge %s: %w", t.table, err)
		}
		n, _ := res.RowsAffected()
		out[t.table] = n
	}
	res, err := s.db.w.ExecContext(ctx, "DELETE FROM login_attempts WHERE username = ?", username)
	if err != nil {
		return out, fmt.Errorf("db: purge login_attempts: %w", err)
	}
	n, _ := res.RowsAffected()
	out["login_attempts"] = n
	return out, nil
}

func asInt64(v any) int64 {
	switch n := v.(type) {
	case int64:
		return n
	case float64:
		return int64(n)
	case int:
		return int64(n)
	}
	return 0
}
