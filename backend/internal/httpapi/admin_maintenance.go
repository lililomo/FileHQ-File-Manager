package httpapi

import (
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/admin"
)

// Admin maintenance (Step 19b, SPEC §6.3 GET /admin/maintenance, POST
// /admin/maintenance/{job}, GET /admin/maintenance/backups[/{name}]).

// BackupDownloadPrefix streams for longer than APITimeout.
const BackupDownloadPrefix = "/api/v1/admin/maintenance/backups/"

func (s *Server) handleAdminMaintenance(w http.ResponseWriter, r *http.Request) {
	v, err := s.admin.MaintenanceStatus(r.Context())
	if err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, v)
}

func (s *Server) handleAdminMaintenanceRun(w http.ResponseWriter, r *http.Request) {
	var in admin.RunInput
	if r.ContentLength != 0 {
		if !ReadJSON(w, r, &in) {
			return
		}
	}
	t, err := s.admin.RunMaintenance(r.Context(), s.actor(r), chi.URLParam(r, "job"), in)
	if err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusAccepted, map[string]any{"task": t})
}

func (s *Server) handleAdminBackups(w http.ResponseWriter, r *http.Request) {
	v, err := s.admin.MaintenanceStatus(r.Context())
	if err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": v.Backups})
}

func (s *Server) handleAdminBackupDownload(w http.ResponseWriter, r *http.Request) {
	name := chi.URLParam(r, "name")
	f, fi, err := s.admin.OpenBackup(r.Context(), s.actor(r), name)
	if err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	defer func() { _ = f.Close() }()
	h := w.Header()
	h.Set("Content-Type", "application/vnd.sqlite3")
	h.Set("Content-Disposition", `attachment; filename="`+name+`"`)
	h.Set("Content-Length", strconv.FormatInt(fi.Size(), 10))
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, f); err != nil {
		s.logger.WarnContext(r.Context(), "backup download aborted", "error", err.Error())
	}
}
