package httpapi

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/admin"
)

// Admin API (SPEC §6.3 "Admin", §8.17; Step 19a): every route sits behind
// RequireAdmin — a non-admin session gets 403 FORBIDDEN, never a hidden
// menu only. GET /branding/logo is public (login page).

func (s *Server) mountAdmin(api chi.Router) {
	if s.admin == nil {
		return
	}
	api.Get("/branding/logo", s.handleBrandingLogo)
	api.Group(func(g chi.Router) {
		g.Use(OriginCheck(s.cfg.AppBaseURL))
		g.Use(s.RequireAuth)
		g.Use(s.RequireCSRF)
		g.Use(RequirePasswordChanged)
		g.Use(RequireAdmin)
		g.Get("/admin/dashboard", s.handleAdminDashboard)
		g.Get("/admin/users", s.handleAdminUsersList)
		g.Post("/admin/users", s.handleAdminUsersCreate)
		g.Get("/admin/users/{id}", s.handleAdminUserGet)
		g.Patch("/admin/users/{id}", s.handleAdminUserPatch)
		g.Delete("/admin/users/{id}", s.handleAdminUserDelete)
		g.Post("/admin/users/{id}/reset-password", s.handleAdminUserResetPassword)
		g.Post("/admin/users/{id}/revoke-sessions", s.handleAdminUserRevokeSessions)
		g.Post("/admin/users/{id}/reset-totp", s.handleAdminUserResetTOTP) // Step 23a
		g.Post("/admin/users/{id}/disable", s.handleAdminUserStatus("disabled"))
		g.Post("/admin/users/{id}/enable", s.handleAdminUserStatus("active"))
		g.Get("/admin/settings", s.handleAdminSettingsList)
		g.Put("/admin/settings", s.handleAdminSettingsPut)
		g.Post("/admin/settings/reset", s.handleAdminSettingsReset)
		g.Post("/admin/branding/logo", s.handleAdminLogoUpload)
		g.Delete("/admin/branding/logo", s.handleAdminLogoDelete)
		// Step 19b
		g.Get("/admin/audit", s.handleAdminAuditList)
		g.Get("/admin/audit/export.csv", s.handleAdminAuditExport)
		g.Get("/admin/tasks", s.handleAdminTasksList)
		g.Post("/admin/tasks/{id}/cancel", s.handleAdminTaskCancel)
		g.Get("/admin/shares", s.handleAdminSharesList)
		g.Post("/admin/shares/{id}/revoke", s.handleAdminShareRevoke)
		g.Get("/admin/maintenance", s.handleAdminMaintenance)
		g.Post("/admin/maintenance/{job}", s.handleAdminMaintenanceRun)
		g.Get("/admin/maintenance/backups", s.handleAdminBackups)
		g.With(s.limitBy("backup_download", s.cfg.RateLimits.BackupDownload, keySession)).Get("/admin/maintenance/backups/{name}", s.handleAdminBackupDownload)
	})
}

// actor is the audit identity of the admin request.
func (s *Server) actor(r *http.Request) admin.Actor {
	u, _ := UserFrom(r.Context())
	return admin.Actor{ID: u.ID, Username: u.Username, IP: ClientIP(r), UserAgent: r.UserAgent()}
}

// writeAdminError maps admin.Error to the envelope.
func (s *Server) writeAdminError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *admin.Error
	if errors.As(err, &ae) {
		code := ErrorCode(ae.Code)
		if _, known := statusByCode[code]; !known {
			code = CodeInternal
		}
		if code == CodeInternal {
			s.logger.ErrorContext(r.Context(), "admin operation failed", "error", ae.Message)
			WriteError(w, r, CodeInternal, 0, "internal server error", nil)
			return
		}
		WriteError(w, r, code, ae.Status, ae.Message, ae.Details)
		return
	}
	s.logger.ErrorContext(r.Context(), "admin operation failed", "error", err.Error())
	WriteError(w, r, CodeInternal, 0, "internal server error", nil)
}

func (s *Server) handleAdminDashboard(w http.ResponseWriter, r *http.Request) {
	d, err := s.admin.Dashboard(r.Context())
	if err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, d)
}

func (s *Server) handleAdminUsersList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := admin.ListFilter{Q: q.Get("q"), Role: q.Get("role"), Status: q.Get("status"), Cursor: q.Get("cursor")}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			WriteError(w, r, CodeValidationFailed, 0, "invalid limit", map[string]any{"fields": map[string]any{"limit": "invalid"}})
			return
		}
		f.Limit = n
	}
	page, err := s.admin.ListUsers(r.Context(), f)
	if err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, page)
}

func (s *Server) handleAdminUsersCreate(w http.ResponseWriter, r *http.Request) {
	var req admin.CreateInput
	if !ReadJSON(w, r, &req) {
		return
	}
	res, err := s.admin.CreateUser(r.Context(), s.actor(r), req)
	if err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	s.evictUser(res.User.ID)
	WriteJSON(w, http.StatusCreated, res)
}

func (s *Server) handleAdminUserGet(w http.ResponseWriter, r *http.Request) {
	u, err := s.admin.GetUser(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"user": s.admin.View(r.Context(), u)})
}

func (s *Server) handleAdminUserPatch(w http.ResponseWriter, r *http.Request) {
	var req admin.UpdateInput
	if !ReadJSON(w, r, &req) {
		return
	}
	v, err := s.admin.UpdateUser(r.Context(), s.actor(r), chi.URLParam(r, "id"), req)
	if err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	s.evictUser(v.ID)
	WriteJSON(w, http.StatusOK, map[string]any{"user": v})
}

func (s *Server) handleAdminUserStatus(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := s.admin.SetStatus(r.Context(), s.actor(r), chi.URLParam(r, "id"), status)
		if err != nil {
			s.writeAdminError(w, r, err)
			return
		}
		s.evictUser(v.ID)
		WriteJSON(w, http.StatusOK, map[string]any{"user": v})
	}
}

func (s *Server) handleAdminUserResetPassword(w http.ResponseWriter, r *http.Request) {
	pw, err := s.admin.ResetPassword(r.Context(), s.actor(r), chi.URLParam(r, "id"))
	if err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	WriteJSON(w, http.StatusOK, map[string]any{"temporaryPassword": pw})
}

func (s *Server) handleAdminUserRevokeSessions(w http.ResponseWriter, r *http.Request) {
	if _, err := s.admin.RevokeSessions(r.Context(), s.actor(r), chi.URLParam(r, "id")); err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleAdminUserResetTOTP clears the user's second factor and revokes
// their sessions (SPEC §8.17, Step 23a).
func (s *Server) handleAdminUserResetTOTP(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if err := s.admin.ResetTOTP(r.Context(), s.actor(r), id); err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	s.evictUser(id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleAdminUserDelete(w http.ResponseWriter, r *http.Request) {
	var req admin.DeleteInput
	if !ReadJSON(w, r, &req) {
		return
	}
	id := chi.URLParam(r, "id")
	t, res, err := s.admin.DeleteUser(r.Context(), s.actor(r), id, req)
	if err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	s.evictUser(id)
	if t != nil {
		WriteJSON(w, http.StatusAccepted, map[string]any{"task": t})
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"result": res})
}

// evictUser drops cached handles of an account whose row changed.
func (s *Server) evictUser(userID string) {
	s.jails.evict(userID)
	if s.auth != nil {
		s.auth.InvalidateUser(userID)
	}
}
