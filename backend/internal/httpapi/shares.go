package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/share"
)

// mountShares registers the owner-side share routes (SPEC §6.3 "Shares").
// The public routes live in public_share.go.
func (s *Server) mountShares(api chi.Router) {
	if s.shares == nil {
		return
	}
	api.Group(func(g chi.Router) {
		g.Use(OriginCheck(s.cfg.AppBaseURL))
		g.Use(s.RequireAuth)
		g.Use(s.RequireCSRF)
		g.Use(RequirePasswordChanged)
		g.Post("/shares", s.handleSharesCreate)
		g.Get("/shares", s.handleSharesList)
		g.Get("/shares/by-path", s.handleSharesByPath)
		g.Get("/shares/{id}", s.handleSharesGet)
		g.Patch("/shares/{id}", s.handleSharesUpdate)
		g.Delete("/shares/{id}", s.handleSharesDelete)
		g.Post("/shares/{id}/disable", s.handleSharesDisable)
		g.Post("/shares/{id}/enable", s.handleSharesEnable)
		g.Post("/shares/{id}/regenerate-token", s.handleSharesRegenerate)
		g.Get("/shares/{id}/access-logs", s.handleSharesAccessLogs)
	})
}

// writeShareError maps share.Error to the envelope.
func (s *Server) writeShareError(w http.ResponseWriter, r *http.Request, err error) {
	var se *share.Error
	if errors.As(err, &se) {
		code := ErrorCode(se.Code)
		if _, known := statusByCode[code]; !known {
			code = CodeInternal
		}
		WriteError(w, r, code, se.Status, se.Message, se.Details)
		return
	}
	s.logger.ErrorContext(r.Context(), "share operation failed", slog.String("error", err.Error()))
	WriteError(w, r, CodeInternal, 0, "internal server error", nil)
}

// shareAudit records a metadata-only audit entry (never token/password).
func (s *Server) shareAudit(r *http.Request, u sqlc.User, action string, v *share.View, extra map[string]any) {
	if s.audit == nil || v == nil {
		return
	}
	details := map[string]any{
		"path": v.Path, "isDir": v.IsDir, "mode": v.Mode, "hasPassword": v.HasPassword, "expiresAt": v.ExpiresAt,
		"maxDownloads": v.MaxDownloads, "allowDownload": v.AllowDownload, "allowUpload": v.AllowUpload, "hideOwner": v.HideOwner,
		"slug": v.Slug, "status": v.Status,
	}
	for k, val := range extra {
		details[k] = val
	}
	s.audit.Record(r.Context(), audit.Entry{ActorID: u.ID, ActorUsername: u.Username, Action: action, TargetType: "share", TargetID: v.ID,
		IP: ClientIP(r), UserAgent: r.UserAgent(), Details: details})
}

func (s *Server) shareActivity(r *http.Request, u sqlc.User, action string, v *share.View) {
	if s.files == nil || v == nil {
		return
	}
	s.files.RecordActivity(r.Context(), u.ID, action, v.Path, "", map[string]any{"shareId": v.ID, "mode": v.Mode})
}

func (s *Server) handleSharesCreate(w http.ResponseWriter, r *http.Request) {
	var in share.Input
	if !ReadJSON(w, r, &in) {
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	v, err := s.shares.Create(r.Context(), j, u, in)
	if err != nil {
		s.writeShareError(w, r, err)
		return
	}
	s.shareAudit(r, u, audit.ActionShareCreate, v, nil)
	s.shareActivity(r, u, files.ActShareCreate, v)
	WriteJSON(w, http.StatusCreated, map[string]any{"share": v, "url": v.URL})
}

func (s *Server) handleSharesList(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	q := r.URL.Query()
	mode, status := q.Get("mode"), q.Get("status")
	fields := map[string]any{}
	if mode != "" && mode != share.ModePublic && mode != share.ModeUsers {
		fields["mode"] = "must be public or users"
	}
	switch status {
	case "", "active", "disabled", "expired", "broken":
	default:
		fields["status"] = "must be active, disabled, expired or broken"
	}
	if len(fields) > 0 {
		WriteError(w, r, CodeValidationFailed, 0, "invalid query parameters", map[string]any{"fields": fields})
		return
	}
	page, err := s.shares.List(r.Context(), u.ID, q.Get("cursor"), mode, status)
	if err != nil {
		s.writeShareError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, page)
}

func (s *Server) handleSharesByPath(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	p, ok := cleanPath(w, r, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	items, err := s.shares.ByPath(r.Context(), u.ID, p)
	if err != nil {
		s.writeShareError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// sharesForStat fills the `shares` key of GET /files/stat.
func (s *Server) sharesForStat(r *http.Request, userID string, p fsjail.VPath) any {
	if s.shares == nil {
		return []any{}
	}
	items, err := s.shares.ByPath(r.Context(), userID, p)
	if err != nil {
		return []any{}
	}
	return items
}

func (s *Server) handleSharesGet(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	row, err := s.shares.Get(r.Context(), u.ID, chi.URLParam(r, "id"))
	if err != nil {
		s.writeShareError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"share": s.shares.View(row)})
}

func (s *Server) handleSharesUpdate(w http.ResponseWriter, r *http.Request) {
	var in share.Input
	if !ReadJSON(w, r, &in) {
		return
	}
	u, _ := UserFrom(r.Context())
	v, err := s.shares.Update(r.Context(), u.ID, chi.URLParam(r, "id"), in)
	if err != nil {
		s.writeShareError(w, r, err)
		return
	}
	s.shareAudit(r, u, audit.ActionShareUpdate, v, nil)
	s.shareActivity(r, u, files.ActShareUpdate, v)
	WriteJSON(w, http.StatusOK, map[string]any{"share": v})
}

func (s *Server) handleSharesDelete(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	row, err := s.shares.Delete(r.Context(), u.ID, chi.URLParam(r, "id"))
	if err != nil {
		s.writeShareError(w, r, err)
		return
	}
	v := s.shares.View(row)
	s.shareAudit(r, u, audit.ActionShareDelete, v, nil)
	s.shareActivity(r, u, files.ActShareDelete, v)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSharesDisable(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	v, err := s.shares.Disable(r.Context(), u.ID, chi.URLParam(r, "id"), share.ReasonManual)
	if err != nil {
		s.writeShareError(w, r, err)
		return
	}
	s.shareAudit(r, u, audit.ActionShareUpdate, v, map[string]any{"disabled": true})
	WriteJSON(w, http.StatusOK, map[string]any{"share": v})
}

func (s *Server) handleSharesEnable(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	v, err := s.shares.Enable(r.Context(), u.ID, chi.URLParam(r, "id"))
	if err != nil {
		s.writeShareError(w, r, err)
		return
	}
	s.shareAudit(r, u, audit.ActionShareUpdate, v, map[string]any{"disabled": false})
	WriteJSON(w, http.StatusOK, map[string]any{"share": v})
}

func (s *Server) handleSharesRegenerate(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	v, err := s.shares.RegenerateToken(r.Context(), u.ID, chi.URLParam(r, "id"))
	if err != nil {
		s.writeShareError(w, r, err)
		return
	}
	s.shareAudit(r, u, audit.ActionShareUpdate, v, map[string]any{"regenerated": true})
	WriteJSON(w, http.StatusOK, map[string]any{"share": v, "url": v.URL})
}

func (s *Server) handleSharesAccessLogs(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	page, err := s.shares.AccessLogs(r.Context(), u.ID, chi.URLParam(r, "id"), r.URL.Query().Get("cursor"))
	if err != nil {
		s.writeShareError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, page)
}
