package db

import (
	"context"

	"kppbwi.app/filemanager/internal/db/sqlc"
)

// TaskFilter narrows ListUserTasks. State "" means any; Before/BeforeID is
// the (created_at, id) cursor of the last row seen.
type TaskFilter struct {
	State    string
	Before   string
	BeforeID string
	Limit    int64
}

// TaskRepo persists the task engine rows (SPEC §4.2 tasks/task_logs, §7.3).
type TaskRepo interface {
	InsertTask(ctx context.Context, p sqlc.InsertTaskParams) error
	GetTask(ctx context.Context, id string) (sqlc.Task, error)
	GetUserTask(ctx context.Context, userID, id string) (sqlc.Task, error)
	ListUserTasks(ctx context.Context, userID string, f TaskFilter) ([]sqlc.Task, error)
	ListTasksByState(ctx context.Context, state string) ([]sqlc.Task, error)
	CountUserTasksByState(ctx context.Context, userID, state string) (int64, error)
	CountTasksByState(ctx context.Context) (map[string]int64, error)
	SetTaskRunning(ctx context.Context, id, startedAt string) error
	SetTaskFinal(ctx context.Context, p sqlc.SetTaskFinalParams) error
	UpdateTaskProgress(ctx context.Context, p sqlc.UpdateTaskProgressParams) error
	SetTaskCancelRequested(ctx context.Context, id, at string) (int64, error)
	MarkRunningTasksInterrupted(ctx context.Context, at string) (int64, error)
	DeleteUserTask(ctx context.Context, userID, id string) (int64, error)
	DeleteFinishedTasksBefore(ctx context.Context, before string) (int64, error)
	InsertTaskLog(ctx context.Context, p sqlc.InsertTaskLogParams) error
	ListTaskLogs(ctx context.Context, taskID string, afterID, limit int64) ([]sqlc.TaskLog, error)
	CountTaskLogs(ctx context.Context, taskID string) (int64, error)
}

// NotificationRepo persists in-app notifications (SPEC §4.2, §8.16).
type NotificationRepo interface {
	InsertNotification(ctx context.Context, p sqlc.InsertNotificationParams) error
	ListNotifications(ctx context.Context, userID string, unreadOnly bool, before, beforeID string, limit int64) ([]sqlc.Notification, error)
	CountUnreadNotifications(ctx context.Context, userID string) (int64, error)
	MarkNotificationRead(ctx context.Context, userID, id, at string) (int64, error)
	MarkAllNotificationsRead(ctx context.Context, userID, at string) (int64, error)
	DeleteNotification(ctx context.Context, userID, id string) (int64, error)
	PruneNotifications(ctx context.Context, userID string, keep int64) (int64, error)
}

func (s *Store) InsertTask(ctx context.Context, p sqlc.InsertTaskParams) error {
	return s.w.InsertTask(ctx, p)
}

func (s *Store) GetTask(ctx context.Context, id string) (sqlc.Task, error) {
	return s.r.GetTask(ctx, id)
}

func (s *Store) GetUserTask(ctx context.Context, userID, id string) (sqlc.Task, error) {
	return s.r.GetUserTask(ctx, sqlc.GetUserTaskParams{ID: id, UserID: &userID})
}

func (s *Store) ListUserTasks(ctx context.Context, userID string, f TaskFilter) ([]sqlc.Task, error) {
	anyState := int64(1)
	if f.State != "" {
		anyState = 0
	}
	if f.Limit <= 0 {
		f.Limit = 50
	}
	return s.r.ListUserTasks(ctx, sqlc.ListUserTasksParams{UserID: &userID, AnyState: anyState, State: f.State, Before: f.Before, BeforeID: f.BeforeID, MaxRows: f.Limit})
}

func (s *Store) ListTasksByState(ctx context.Context, state string) ([]sqlc.Task, error) {
	return s.r.ListTasksByState(ctx, state)
}

func (s *Store) CountUserTasksByState(ctx context.Context, userID, state string) (int64, error) {
	return s.r.CountUserTasksByState(ctx, sqlc.CountUserTasksByStateParams{UserID: &userID, State: state})
}

func (s *Store) CountTasksByState(ctx context.Context) (map[string]int64, error) {
	rows, err := s.r.CountTasksByState(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, r := range rows {
		out[r.State] = r.N
	}
	return out, nil
}

func (s *Store) SetTaskRunning(ctx context.Context, id, startedAt string) error {
	return s.w.SetTaskRunning(ctx, sqlc.SetTaskRunningParams{StartedAt: &startedAt, UpdatedAt: startedAt, ID: id})
}

func (s *Store) SetTaskFinal(ctx context.Context, p sqlc.SetTaskFinalParams) error {
	return s.w.SetTaskFinal(ctx, p)
}

func (s *Store) UpdateTaskProgress(ctx context.Context, p sqlc.UpdateTaskProgressParams) error {
	return s.w.UpdateTaskProgress(ctx, p)
}

func (s *Store) SetTaskCancelRequested(ctx context.Context, id, at string) (int64, error) {
	return s.w.SetTaskCancelRequested(ctx, sqlc.SetTaskCancelRequestedParams{UpdatedAt: at, ID: id})
}

func (s *Store) MarkRunningTasksInterrupted(ctx context.Context, at string) (int64, error) {
	return s.w.MarkRunningTasksInterrupted(ctx, sqlc.MarkRunningTasksInterruptedParams{FinishedAt: &at, UpdatedAt: at})
}

func (s *Store) DeleteUserTask(ctx context.Context, userID, id string) (int64, error) {
	return s.w.DeleteUserTask(ctx, sqlc.DeleteUserTaskParams{ID: id, UserID: &userID})
}

func (s *Store) DeleteFinishedTasksBefore(ctx context.Context, before string) (int64, error) {
	return s.w.DeleteFinishedTasksBefore(ctx, &before)
}

func (s *Store) InsertTaskLog(ctx context.Context, p sqlc.InsertTaskLogParams) error {
	return s.w.InsertTaskLog(ctx, p)
}

func (s *Store) ListTaskLogs(ctx context.Context, taskID string, afterID, limit int64) ([]sqlc.TaskLog, error) {
	return s.r.ListTaskLogs(ctx, sqlc.ListTaskLogsParams{TaskID: taskID, AfterID: afterID, MaxRows: limit})
}

func (s *Store) CountTaskLogs(ctx context.Context, taskID string) (int64, error) {
	return s.r.CountTaskLogs(ctx, taskID)
}

func (s *Store) InsertNotification(ctx context.Context, p sqlc.InsertNotificationParams) error {
	return s.w.InsertNotification(ctx, p)
}

func (s *Store) ListNotifications(ctx context.Context, userID string, unreadOnly bool, before, beforeID string, limit int64) ([]sqlc.Notification, error) {
	u := int64(0)
	if unreadOnly {
		u = 1
	}
	if limit <= 0 {
		limit = 20
	}
	return s.r.ListNotifications(ctx, sqlc.ListNotificationsParams{UserID: userID, UnreadOnly: u, Before: before, BeforeID: beforeID, MaxRows: limit})
}

func (s *Store) CountUnreadNotifications(ctx context.Context, userID string) (int64, error) {
	return s.r.CountUnreadNotifications(ctx, userID)
}

func (s *Store) MarkNotificationRead(ctx context.Context, userID, id, at string) (int64, error) {
	return s.w.MarkNotificationRead(ctx, sqlc.MarkNotificationReadParams{ReadAt: &at, UserID: userID, ID: id})
}

func (s *Store) MarkAllNotificationsRead(ctx context.Context, userID, at string) (int64, error) {
	return s.w.MarkAllNotificationsRead(ctx, sqlc.MarkAllNotificationsReadParams{ReadAt: &at, UserID: userID})
}

func (s *Store) DeleteNotification(ctx context.Context, userID, id string) (int64, error) {
	return s.w.DeleteNotification(ctx, sqlc.DeleteNotificationParams{UserID: userID, ID: id})
}

func (s *Store) PruneNotifications(ctx context.Context, userID string, keep int64) (int64, error) {
	return s.w.PruneNotifications(ctx, sqlc.PruneNotificationsParams{UserID: userID, Keep: keep})
}
