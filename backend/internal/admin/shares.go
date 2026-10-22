package admin

import (
	"context"
	"errors"
	"net/http"

	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/share"
	"kppbwi.app/filemanager/internal/tasks"
)

// Global tasks and shares (Step 19b, SPEC §8.17 "Tasks", §6.3 GET
// /admin/tasks, /admin/shares).

// TaskFilter is GET /admin/tasks; User is a username, "system" for tasks
// without a user, "" for all.
type TaskFilter struct {
	State  string
	User   string
	Cursor string
	Limit  int64
}

// ListTasks lists every user's tasks (and system tasks) newest first.
func (s *Service) ListTasks(ctx context.Context, f TaskFilter) (*tasks.AdminPage, error) {
	if s.tasks == nil {
		return &tasks.AdminPage{Items: []tasks.AdminTask{}}, nil
	}
	switch tasks.State(f.State) {
	case "", tasks.Queued, tasks.Running, tasks.Done, tasks.Failed, tasks.Cancelled, tasks.Interrupted:
	default:
		return nil, fieldErr("state", "invalid", "unknown task state")
	}
	af := tasks.AdminFilter{State: f.State, Cursor: f.Cursor, Limit: f.Limit}
	switch f.User {
	case "":
	case "system":
		af.System = true
	default:
		u, err := s.repo.GetUserByUsername(ctx, f.User)
		if err != nil {
			if db.IsNotFound(err) {
				return &tasks.AdminPage{Items: []tasks.AdminTask{}}, nil
			}
			return nil, internalErr(err)
		}
		af.UserID = u.ID
	}
	page, err := s.tasks.ListAll(ctx, af, func(ctx context.Context, id string) string {
		if u, err := s.repo.GetUserByID(ctx, id); err == nil {
			return u.Username
		}
		return ""
	})
	if err != nil {
		return nil, internalErr(err)
	}
	return &page, nil
}

// CancelTask cancels any user's task (audit admin.task_cancel).
func (s *Service) CancelTask(ctx context.Context, a Actor, id string) (*tasks.Task, error) {
	if s.tasks == nil {
		return nil, newErr("NOT_FOUND", http.StatusNotFound, "task not found", nil)
	}
	t, err := s.tasks.CancelAny(ctx, id)
	switch {
	case errors.Is(err, tasks.ErrNotFound):
		return nil, newErr("NOT_FOUND", http.StatusNotFound, "task not found", nil)
	case errors.Is(err, tasks.ErrFinal):
		return nil, newErr("CONFLICT", http.StatusConflict, "task already finished", map[string]any{"reason": "final", "state": t.State})
	case err != nil:
		return nil, internalErr(err)
	}
	owner := ""
	if t.UserID != "" {
		if u, uerr := s.repo.GetUserByID(ctx, t.UserID); uerr == nil {
			owner = u.Username
		}
	}
	s.record(ctx, nil, a, audit.ActionAdminTaskCancel, "task", t.ID, map[string]any{"type": t.Type, "owner": owner})
	return &t, nil
}

// ListShares is GET /admin/shares (owner = username).
func (s *Service) ListShares(ctx context.Context, cursor, owner, mode, status string) (*share.AdminPage, error) {
	if s.o.Shares == nil {
		return &share.AdminPage{Items: []share.AdminView{}}, nil
	}
	ownerID := ""
	if owner != "" {
		u, err := s.repo.GetUserByUsername(ctx, owner)
		if err != nil {
			if db.IsNotFound(err) {
				return &share.AdminPage{Items: []share.AdminView{}}, nil
			}
			return nil, internalErr(err)
		}
		ownerID = u.ID
	}
	page, err := s.o.Shares.AdminList(ctx, cursor, ownerID, mode, status)
	if err != nil {
		return nil, shareErr(err)
	}
	return page, nil
}

// RevokeShare disables a share for good (audit share.admin_revoked).
func (s *Service) RevokeShare(ctx context.Context, a Actor, id string) error {
	if s.o.Shares == nil {
		return newErr("NOT_FOUND", http.StatusNotFound, "share not found", nil)
	}
	row, changed, err := s.o.Shares.AdminRevoke(ctx, id)
	if err != nil {
		return shareErr(err)
	}
	if changed {
		s.record(ctx, nil, a, audit.ActionShareAdminRevoked, "share", row.ID, map[string]any{"owner": row.OwnerUserID, "mode": row.Mode, "path": row.Path})
	}
	return nil
}

func shareErr(err error) error {
	var se *share.Error
	if errors.As(err, &se) {
		return newErr(se.Code, se.Status, se.Message, se.Details)
	}
	return internalErr(err)
}
