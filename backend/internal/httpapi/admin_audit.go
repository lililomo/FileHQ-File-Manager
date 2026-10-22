package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"kppbwi.app/filemanager/internal/admin"
)

// Admin audit log (Step 19b, SPEC §6.3 GET /admin/audit, export.csv).

// AuditExportPath streams for longer than APITimeout.
const AuditExportPath = "/api/v1/admin/audit/export.csv"

func auditFilter(r *http.Request) admin.AuditFilter {
	q := r.URL.Query()
	return admin.AuditFilter{Actor: q.Get("actor"), Action: q.Get("action"), From: q.Get("from"), To: q.Get("to"), Q: q.Get("q"), Cursor: q.Get("cursor")}
}

func (s *Server) handleAdminAuditList(w http.ResponseWriter, r *http.Request) {
	f := auditFilter(r)
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			WriteError(w, r, CodeValidationFailed, 0, "invalid limit", map[string]any{"fields": map[string]any{"limit": "invalid"}})
			return
		}
		f.Limit = n
	}
	page, err := s.admin.ListAudit(r.Context(), f)
	if err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, page)
}

func (s *Server) handleAdminAuditExport(w http.ResponseWriter, r *http.Request) {
	f := auditFilter(r)
	f.Cursor = ""
	if err := s.admin.ValidateAudit(f); err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/csv; charset=utf-8")
	h.Set("Content-Disposition", `attachment; filename="audit-`+time.Now().UTC().Format("20060102-150405")+`.csv"`)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	rc := http.NewResponseController(w)
	n, err := s.admin.ExportAudit(r.Context(), f, w, func() { _ = rc.Flush() })
	if err != nil {
		// headers are gone: the truncated download is the only signal
		s.logger.WarnContext(r.Context(), "audit export aborted", "rows", n, "error", err.Error())
	}
}
