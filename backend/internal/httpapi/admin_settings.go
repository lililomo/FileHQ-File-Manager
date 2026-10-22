package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"kppbwi.app/filemanager/internal/admin"
)

// Settings + branding (SPEC §8.17 "Settings", §3.1 overrides; Step 19a).

func (s *Server) handleAdminSettingsList(w http.ResponseWriter, r *http.Request) {
	items, err := s.admin.ListSettings(r.Context())
	if err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleAdminSettingsPut(w http.ResponseWriter, r *http.Request) {
	var req map[string]json.RawMessage
	if !ReadJSON(w, r, &req) {
		return
	}
	items, err := s.admin.UpdateSettings(r.Context(), s.actor(r), req)
	if err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

type settingsResetRequest struct {
	Keys []string `json:"keys"`
}

func (s *Server) handleAdminSettingsReset(w http.ResponseWriter, r *http.Request) {
	var req settingsResetRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	items, err := s.admin.ResetSettings(r.Context(), s.actor(r), req.Keys)
	if err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

// handleAdminLogoUpload accepts multipart field "logo" (or a raw image/png
// body): PNG by magic bytes, at most 512 KB.
func (s *Server) handleAdminLogoUpload(w http.ResponseWriter, r *http.Request) {
	ct := r.Header.Get("Content-Type")
	var body = r.Body
	if len(ct) >= 19 && ct[:19] == "multipart/form-data" {
		if err := r.ParseMultipartForm(admin.MaxLogoBytes + 64<<10); err != nil {
			var mbe *http.MaxBytesError
			if errors.As(err, &mbe) {
				WriteError(w, r, CodePayloadTooLarge, 0, "logo must be at most 512 KB", map[string]any{"limit": admin.MaxLogoBytes})
				return
			}
			WriteError(w, r, CodeValidationFailed, 0, "invalid multipart body", map[string]any{"fields": map[string]any{"logo": "invalid"}})
			return
		}
		f, _, err := r.FormFile("logo")
		if err != nil {
			WriteError(w, r, CodeValidationFailed, 0, "multipart field logo is required", map[string]any{"fields": map[string]any{"logo": "required"}})
			return
		}
		defer f.Close()
		body = f
	}
	if err := s.admin.SaveLogo(r.Context(), s.actor(r), body); err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"logoUrl": logoURL(s.cfg.Runtime().BrandingLogo)})
}

func (s *Server) handleAdminLogoDelete(w http.ResponseWriter, r *http.Request) {
	if err := s.admin.DeleteLogo(r.Context(), s.actor(r)); err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// logoURL is the public URL of the branding logo ("" when none).
func logoURL(name string) string {
	if name == "" {
		return ""
	}
	return "/api/v1/branding/logo"
}

// handleBrandingLogo serves DATA_DIR/branding/logo.png (public; the login
// page shows it). Content policy as for any served image: nosniff, no
// script execution context.
func (s *Server) handleBrandingLogo(w http.ResponseWriter, r *http.Request) {
	f, info, err := s.admin.OpenLogo()
	if err != nil {
		if errors.Is(err, admin.ErrNoLogo) {
			WriteError(w, r, CodeNotFound, 0, "no logo configured", nil)
			return
		}
		WriteError(w, r, CodeInternal, 0, "internal server error", nil)
		return
	}
	defer f.Close()
	h := w.Header()
	h.Set("Content-Type", "image/png")
	h.Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	h.Set("Cache-Control", "public, max-age=300")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
	http.ServeContent(w, r, "logo.png", info.ModTime(), f)
}
