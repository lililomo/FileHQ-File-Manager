package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/tasks"
)

// mountTasks registers /tasks and /notifications (SPEC §6.3 "Tasks &
// events", §8.16): S+P, CSRF on mutations, 404 for other users' ids.
func (s *Server) mountTasks(api chi.Router) {
	if s.tasks == nil {
		return
	}
	api.Group(func(g chi.Router) {
		g.Use(OriginCheck(s.cfg.AppBaseURL))
		g.Use(s.RequireAuth)
		g.Use(s.RequireCSRF)
		g.Use(RequirePasswordChanged)
		g.Get("/tasks", s.handleTasksList)
		g.Get("/tasks/{id}", s.handleTaskGet)
		g.Get("/tasks/{id}/logs", s.handleTaskLogs)
		g.Post("/tasks/{id}/cancel", s.handleTaskCancel)
		g.Post("/tasks/{id}/retry", s.handleTaskRetry)
		g.Delete("/tasks/{id}", s.handleTaskDelete)
		if s.notify != nil {
			g.Get("/notifications", s.handleNotificationsList)
			g.Post("/notifications/read", s.handleNotificationsRead)
			g.Delete("/notifications/{id}", s.handleNotificationDelete)
		}
	})
}

func (s *Server) writeTaskError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, tasks.ErrNotFound):
		WriteError(w, r, CodeNotFound, 0, "task not found", nil)
	case errors.Is(err, tasks.ErrFinal):
		WriteError(w, r, CodeConflict, 0, "task already finished", map[string]any{"reason": "task_final"})
	case errors.Is(err, tasks.ErrNotRetryable):
		WriteError(w, r, CodeConflict, 0, "only failed, cancelled or interrupted tasks can be retried", map[string]any{"reason": "not_retryable"})
	case errors.Is(err, tasks.ErrNeedsSecret):
		WriteError(w, r, CodeConflict, 0, "task needs its password again", map[string]any{"reason": "needs_secret"})
	case errors.Is(err, tasks.ErrLimit):
		WriteError(w, r, CodeTaskLimit, 0, "too many active tasks", map[string]any{"max": tasks.MaxQueuedPerUser})
	case errors.Is(err, tasks.ErrUnknownType):
		WriteError(w, r, CodeValidationFailed, 0, "unknown task type", nil)
	case errors.Is(err, tasks.ErrStopped):
		WriteError(w, r, CodeInternal, http.StatusServiceUnavailable, "server is shutting down", nil)
	default:
		s.logger.ErrorContext(r.Context(), "task request failed", slog.String("error", err.Error()))
		WriteError(w, r, CodeInternal, 0, "internal server error", nil)
	}
}

func (s *Server) handleTasksList(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	q := r.URL.Query()
	state := q.Get("state")
	switch tasks.State(state) {
	case "", tasks.Queued, tasks.Running, tasks.Done, tasks.Failed, tasks.Cancelled, tasks.Interrupted:
	default:
		WriteError(w, r, CodeValidationFailed, 0, "unknown state", map[string]any{"fields": map[string]any{"state": "queued, running, done, failed, cancelled or interrupted"}})
		return
	}
	var limit int64
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 200 {
			WriteError(w, r, CodeValidationFailed, 0, "limit must be 1..200", map[string]any{"fields": map[string]any{"limit": "1..200"}})
			return
		}
		limit = int64(n)
	}
	page, err := s.tasks.List(r.Context(), u.ID, state, q.Get("cursor"), limit)
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, page)
}

func (s *Server) handleTaskGet(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	t, err := s.tasks.Get(r.Context(), u.ID, chi.URLParam(r, "id"))
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"task": t})
}

func (s *Server) handleTaskLogs(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	var after int64
	if a := r.URL.Query().Get("after"); a != "" {
		n, err := strconv.ParseInt(a, 10, 64)
		if err != nil || n < 0 {
			WriteError(w, r, CodeValidationFailed, 0, "after must be a log id", map[string]any{"fields": map[string]any{"after": "integer"}})
			return
		}
		after = n
	}
	logs, err := s.tasks.Logs(r.Context(), u.ID, chi.URLParam(r, "id"), after)
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": logs})
}

func (s *Server) handleTaskCancel(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	t, err := s.tasks.Cancel(r.Context(), u.ID, chi.URLParam(r, "id"))
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"task": t})
}

func (s *Server) handleTaskRetry(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	t, err := s.tasks.Retry(r.Context(), u.ID, chi.URLParam(r, "id"))
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusAccepted, map[string]any{"task": t})
}

func (s *Server) handleTaskDelete(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	if err := s.tasks.Delete(r.Context(), u.ID, chi.URLParam(r, "id")); err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleNotificationsList(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	q := r.URL.Query()
	unread, ok := parseBoolDefault(q.Get("unread"), false)
	if !ok {
		WriteError(w, r, CodeValidationFailed, 0, "unread must be a boolean", map[string]any{"fields": map[string]any{"unread": "boolean"}})
		return
	}
	var limit int64
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 100 {
			WriteError(w, r, CodeValidationFailed, 0, "limit must be 1..100", map[string]any{"fields": map[string]any{"limit": "1..100"}})
			return
		}
		limit = int64(n)
	}
	page, err := s.notify.List(r.Context(), u.ID, unread, q.Get("cursor"), limit)
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, page)
}

type notificationsReadRequest struct {
	IDs []string `json:"ids,omitempty"`
	All bool     `json:"all,omitempty"`
}

func (s *Server) handleNotificationsRead(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	var req notificationsReadRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	if !req.All && len(req.IDs) == 0 {
		WriteError(w, r, CodeValidationFailed, 0, "ids or all is required", map[string]any{"fields": map[string]any{"ids": "required"}})
		return
	}
	if len(req.IDs) > 200 {
		WriteError(w, r, CodeValidationFailed, 0, "too many ids", map[string]any{"fields": map[string]any{"ids": "too_many"}, "max": 200})
		return
	}
	n, err := s.notify.MarkRead(r.Context(), u.ID, req.IDs, req.All)
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	unread, err := s.notify.Unread(r.Context(), u.ID)
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"read": n, "unread": unread})
}

func (s *Server) handleNotificationDelete(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	ok, err := s.notify.Delete(r.Context(), u.ID, chi.URLParam(r, "id"))
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	if !ok {
		WriteError(w, r, CodeNotFound, 0, "notification not found", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
