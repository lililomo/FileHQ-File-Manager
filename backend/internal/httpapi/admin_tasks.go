package httpapi

import (
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/admin"
)

// Admin tasks of every user (Step 19b, SPEC §6.3 GET /admin/tasks, POST
// /admin/tasks/{id}/cancel).

func (s *Server) handleAdminTasksList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := admin.TaskFilter{State: q.Get("state"), User: q.Get("user"), Cursor: q.Get("cursor")}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			WriteError(w, r, CodeValidationFailed, 0, "invalid limit", map[string]any{"fields": map[string]any{"limit": "invalid"}})
			return
		}
		f.Limit = int64(n)
	}
	page, err := s.admin.ListTasks(r.Context(), f)
	if err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, page)
}

func (s *Server) handleAdminTaskCancel(w http.ResponseWriter, r *http.Request) {
	t, err := s.admin.CancelTask(r.Context(), s.actor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"task": t})
}
