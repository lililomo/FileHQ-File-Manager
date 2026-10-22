package db

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Step 19b: admin audit search, task/share listings across users and the
// reference scan of the integrity check.

// AuditQuery narrows ListAuditAdmin (SPEC §6.3 GET /admin/audit). Empty
// fields mean "any". Action ending in "." or "*" is a prefix ("auth.*").
// Q is a case-insensitive substring of actor, target, IP or details.
type AuditQuery struct {
	Actor  string
	Action string
	From   string // inclusive, TimeLayout
	To     string // exclusive, TimeLayout
	Q      string
	// Keyset cursor: rows strictly before (BeforeAt, BeforeID) in the
	// (at DESC, id DESC) order — the order of every audit index (SQLite
	// appends the rowid), so no sort step is needed.
	BeforeAt string
	BeforeID int64
	Limit    int64
}

// TaskQuery narrows ListAllTasks. UserID "" = every task; System = only
// tasks without a user (maintenance).
type TaskQuery struct {
	State    string
	UserID   string
	System   bool
	Before   string
	BeforeID string
	Limit    int64
}

// ShareQuery narrows ListAllShares (cursor = created_at|id of the last row).
type ShareQuery struct {
	Mode          string
	OwnerUserID   string
	BeforeCreated string
	BeforeID      string
	Limit         int64
}

// PathRef is one row of a path-bearing table (integrity check).
type PathRef struct {
	Table  string
	UserID string
	Path   string
}

// AdminOpsRepo groups the Step 19b admin queries.
type AdminOpsRepo interface {
	ListAuditAdmin(ctx context.Context, q AuditQuery) ([]sqlc.AuditLog, error)
	ListAllTasks(ctx context.Context, q TaskQuery) ([]sqlc.Task, error)
	ListActiveTasksOfType(ctx context.Context, typ string) ([]sqlc.Task, error)
	LatestTaskOfType(ctx context.Context, typ string) (sqlc.Task, error)
	ListAllShares(ctx context.Context, q ShareQuery) ([]sqlc.ListAllSharesRow, error)
	// EachPathRef streams every (table, user, path) of favorites, pins,
	// file_notes, file_tags, file_versions and shares ordered by user.
	EachPathRef(ctx context.Context, fn func(PathRef) error) error
}

func (s *Store) ListAuditAdmin(ctx context.Context, q AuditQuery) ([]sqlc.AuditLog, error) {
	if s.db == nil {
		return nil, errors.New("db: ListAuditAdmin cannot run inside a transaction")
	}
	var (
		where []string
		args  []any
	)
	add := func(cond string, a ...any) { where = append(where, cond); args = append(args, a...) }
	if q.BeforeAt != "" {
		add("(at < ? OR (at = ? AND id < ?))", q.BeforeAt, q.BeforeAt, q.BeforeID)
	}
	if q.Actor != "" {
		add("actor_username = ?", q.Actor)
	}
	switch {
	case strings.HasSuffix(q.Action, "*"):
		p := strings.TrimSuffix(q.Action, "*")
		add("action >= ? AND action < ?", p, p+"\xff")
	case strings.HasSuffix(q.Action, "."):
		add("action >= ? AND action < ?", q.Action, q.Action+"\xff")
	case q.Action != "":
		add("action = ?", q.Action)
	}
	if q.From != "" {
		add("at >= ?", q.From)
	}
	if q.To != "" {
		add("at < ?", q.To)
	}
	if q.Q != "" {
		add("instr(lower(coalesce(actor_username, '') || ' ' || action || ' ' || coalesce(target_type, '') || ' ' || coalesce(target_id, '') || ' ' || ip || ' ' || coalesce(details_json, '')), ?) > 0",
			strings.ToLower(q.Q))
	}
	if q.Limit <= 0 {
		q.Limit = 100
	}
	query := "SELECT id, at, actor_user_id, actor_username, action, target_type, target_id, ip, user_agent, details_json FROM audit_logs"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ") //nolint:gosec // fixed condition fragments, values are bound parameters
	}
	query += " ORDER BY at DESC, id DESC LIMIT ?"
	args = append(args, q.Limit)
	rows, err := s.db.r.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("db: list audit: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []sqlc.AuditLog{}
	for rows.Next() {
		var a sqlc.AuditLog
		if err := rows.Scan(&a.ID, &a.At, &a.ActorUserID, &a.ActorUsername, &a.Action, &a.TargetType, &a.TargetID, &a.Ip, &a.UserAgent, &a.DetailsJson); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) ListAllTasks(ctx context.Context, q TaskQuery) ([]sqlc.Task, error) {
	p := sqlc.ListAllTasksParams{AnyState: int64(1), State: q.State, UserMode: int64(0), Before: q.Before, BeforeID: q.BeforeID, MaxRows: q.Limit}
	if q.State != "" {
		p.AnyState = int64(0)
	}
	switch {
	case q.System:
		p.UserMode = int64(2)
	case q.UserID != "":
		p.UserMode = int64(1)
		uid := q.UserID
		p.UserID = &uid
	}
	if p.MaxRows <= 0 {
		p.MaxRows = 50
	}
	return s.r.ListAllTasks(ctx, p)
}

func (s *Store) ListActiveTasksOfType(ctx context.Context, typ string) ([]sqlc.Task, error) {
	return s.r.ListActiveTasksOfType(ctx, typ)
}

func (s *Store) LatestTaskOfType(ctx context.Context, typ string) (sqlc.Task, error) {
	return s.r.LatestTaskOfType(ctx, typ)
}

func (s *Store) ListAllShares(ctx context.Context, q ShareQuery) ([]sqlc.ListAllSharesRow, error) {
	p := sqlc.ListAllSharesParams{AnyMode: int64(1), Mode: q.Mode, AnyOwner: int64(1), OwnerUserID: q.OwnerUserID,
		BeforeCreated: q.BeforeCreated, BeforeID: q.BeforeID, Lim: q.Limit}
	if q.Mode != "" {
		p.AnyMode = int64(0)
	}
	if q.OwnerUserID != "" {
		p.AnyOwner = int64(0)
	}
	if p.BeforeCreated == "" {
		p.BeforeCreated = "￿" // after every timestamp: first page
	}
	if p.Lim <= 0 {
		p.Lim = 100
	}
	return s.r.ListAllShares(ctx, p)
}

func (s *Store) EachPathRef(ctx context.Context, fn func(PathRef) error) error {
	if s.db == nil {
		return errors.New("db: EachPathRef cannot run inside a transaction")
	}
	rows, err := s.db.r.QueryContext(ctx, `
SELECT 'favorites', user_id, path FROM favorites
UNION ALL SELECT 'pins', user_id, path FROM pins
UNION ALL SELECT 'file_notes', user_id, path FROM file_notes
UNION ALL SELECT 'file_tags', user_id, path FROM file_tags
UNION ALL SELECT DISTINCT 'file_versions', user_id, path FROM file_versions
UNION ALL SELECT 'shares', owner_user_id, path FROM shares
ORDER BY 2, 1, 3`)
	if err != nil {
		return fmt.Errorf("db: path refs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var r PathRef
		if err := rows.Scan(&r.Table, &r.UserID, &r.Path); err != nil {
			return err
		}
		if err := fn(r); err != nil {
			return err
		}
	}
	return rows.Err()
}
