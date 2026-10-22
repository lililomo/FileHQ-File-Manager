package tasks

import (
	"context"
	"strings"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Admin view of the engine (SPEC §6.3 GET /admin/tasks, §8.17 "Tasks"):
// every user's tasks plus system tasks (user_id NULL, maintenance).

// AdminTask is a task with its owner (empty for system tasks).
type AdminTask struct {
	Task
	UserID   string `json:"userId,omitempty"`
	Username string `json:"username,omitempty"`
}

// AdminPage is an admin task listing.
type AdminPage struct {
	Items      []AdminTask `json:"items"`
	NextCursor string      `json:"nextCursor,omitempty"`
}

// AdminFilter narrows ListAll; UserID "" = all users, System = only system
// tasks.
type AdminFilter struct {
	State  string
	UserID string
	System bool
	Cursor string
	Limit  int64
}

func (e *Engine) fromAny(r sqlc.Task) Task {
	uid := ""
	if r.UserID != nil {
		uid = *r.UserID
	}
	if t, ok := e.live(r.ID, uid); ok {
		return t
	}
	e.mu.Lock()
	spec, known := e.specs[r.Type]
	e.mu.Unlock()
	return *fromRow(r, spec, known)
}

// ListAll returns visible tasks of every user newest first; usernames come
// from lookup (nil = ids only).
func (e *Engine) ListAll(ctx context.Context, f AdminFilter, lookup func(ctx context.Context, userID string) string) (AdminPage, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	q := db.TaskQuery{State: f.State, UserID: f.UserID, System: f.System, Limit: f.Limit + 1}
	if f.Cursor != "" {
		if i := strings.LastIndex(f.Cursor, "|"); i > 0 {
			q.Before, q.BeforeID = f.Cursor[:i], f.Cursor[i+1:]
		}
	}
	rows, err := e.o.Repo.ListAllTasks(ctx, q)
	if err != nil {
		return AdminPage{}, err
	}
	page := AdminPage{Items: make([]AdminTask, 0, len(rows))}
	if int64(len(rows)) > f.Limit {
		rows = rows[:f.Limit]
		last := rows[len(rows)-1]
		page.NextCursor = last.CreatedAt + "|" + last.ID
	}
	names := map[string]string{}
	for _, r := range rows {
		at := AdminTask{Task: e.fromAny(r)}
		if r.UserID != nil {
			at.UserID = *r.UserID
			if lookup != nil {
				if _, seen := names[at.UserID]; !seen {
					names[at.UserID] = lookup(ctx, at.UserID)
				}
				at.Username = names[at.UserID]
			}
		}
		page.Items = append(page.Items, at)
	}
	return page, nil
}

// GetAny returns a task of any user (admin).
func (e *Engine) GetAny(ctx context.Context, id string) (Task, error) {
	row, err := e.o.Repo.GetTask(ctx, id)
	if err != nil {
		if db.IsNotFound(err) {
			return Task{}, ErrNotFound
		}
		return Task{}, err
	}
	return e.fromAny(row), nil
}

// CancelAny cancels a task of any user (admin; audit by the caller).
func (e *Engine) CancelAny(ctx context.Context, id string) (Task, error) {
	t, err := e.GetAny(ctx, id)
	if err != nil {
		return Task{}, err
	}
	return e.cancel(ctx, t)
}
