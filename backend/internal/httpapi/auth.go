package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/auth"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

// mountAuth registers /auth/* and /me/* (SPEC §6.3 "Auth & sesi", "Profil").
func (s *Server) mountAuth(api chi.Router) {
	api.Group(func(g chi.Router) {
		g.Use(OriginCheck(s.cfg.AppBaseURL))
		g.Post("/auth/login", s.handleLogin)

		g.Group(func(a chi.Router) {
			a.Use(s.RequireAuth)
			a.Use(s.RequireCSRF)
			a.With(s.limitBy("csrf", s.cfg.RateLimits.CSRF, keySession)).Get("/auth/csrf", s.handleCSRF)
			a.Get("/auth/me", s.handleMe)
			a.Post("/auth/logout", s.handleLogout)
			a.Post("/auth/change-password", s.handleChangePassword)

			a.Group(func(p chi.Router) {
				p.Use(RequirePasswordChanged)
				p.Get("/me/preferences", s.handleGetPreferences)
				p.Put("/me/preferences", s.handlePutPreferences)
				s.mountMeSessions(p)
				s.mountMeTOTP(p)
				s.mountFolderPrefs(p)
			})
		})
	})
}

type loginRequest struct {
	Username     string `json:"username"`
	Password     string `json:"password"`
	TOTP         string `json:"totp,omitempty"`         // Step 23a second factor
	RecoveryCode string `json:"recoveryCode,omitempty"` // Step 23a single-use alternative
}

func setRetryAfter(w http.ResponseWriter, secs int) {
	if secs < 1 {
		secs = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(secs))
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	fields := map[string]any{}
	if strings.TrimSpace(req.Username) == "" {
		fields["username"] = "required"
	}
	if req.Password == "" {
		fields["password"] = "required"
	}
	if len(req.Username) > 64 {
		fields["username"] = "too_long"
	}
	if len(req.Password) > 1024 {
		fields["password"] = "too_long"
	}
	if len(req.TOTP) > 16 {
		fields["totp"] = "too_long"
	}
	if len(req.RecoveryCode) > 32 {
		fields["recoveryCode"] = "too_long"
	}
	if len(fields) > 0 {
		WriteError(w, r, CodeValidationFailed, 0, "username and password are required", map[string]any{"fields": fields})
		return
	}
	res, err := s.auth.Login(r.Context(), auth.LoginInput{
		Username: req.Username, Password: req.Password, TOTP: req.TOTP, RecoveryCode: req.RecoveryCode,
		IP: ClientIP(r), UserAgent: r.UserAgent(),
	})
	if err != nil {
		var locked *auth.LockedError
		var limited *auth.RateLimitedError
		switch {
		case errors.As(err, &limited):
			secs := int(limited.RetryAfter.Seconds())
			setRetryAfter(w, secs)
			WriteError(w, r, CodeRateLimited, 0, "too many login attempts, try again later", map[string]any{"retryAfter": secs})
		case errors.Is(err, auth.ErrTOTPRequired):
			// password verified, second factor missing: the client shows the code step
			WriteError(w, r, CodeValidationFailed, 0, "verification code required",
				map[string]any{"fields": map[string]any{"totp": "totp_required"}})
		case errors.Is(err, auth.ErrTOTPInvalid):
			WriteError(w, r, CodeValidationFailed, 0, "verification code is invalid",
				map[string]any{"fields": map[string]any{"totp": "totp_invalid"}})
		case errors.As(err, &locked):
			secs := int(locked.RetryAfter.Seconds())
			setRetryAfter(w, secs)
			WriteError(w, r, CodeAccountLocked, 0, "account temporarily locked", map[string]any{"retryAfter": secs})
		case errors.Is(err, auth.ErrAccountDisabled):
			WriteError(w, r, CodeAccountDisabled, 0, "account is disabled", nil)
		case errors.Is(err, auth.ErrInvalidCredentials):
			WriteError(w, r, CodeUnauthenticated, 0, "username atau password salah", nil)
		default:
			s.logger.ErrorContext(r.Context(), "login failed", slog.String("error", err.Error()))
			WriteError(w, r, CodeInternal, 0, "internal server error", nil)
		}
		return
	}
	http.SetCookie(w, s.auth.Sessions.Cookie(res.Token, s.secureCookies()))
	body := map[string]any{
		"user":               userView(res.User),
		"mustChangePassword": res.User.MustChangePassword != 0,
		"csrfToken":          res.CSRFToken,
	}
	if res.RecoveryCodesRemaining != nil {
		body["recoveryCodesRemaining"] = *res.RecoveryCodesRemaining
	}
	WriteJSON(w, http.StatusOK, body)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	sess, _ := SessionFrom(r.Context())
	u, _ := UserFrom(r.Context())
	if err := s.auth.Logout(r.Context(), sess, u, ClientIP(r), r.UserAgent()); err != nil {
		s.logger.ErrorContext(r.Context(), "logout failed", slog.String("error", err.Error()))
		WriteError(w, r, CodeInternal, 0, "internal server error", nil)
		return
	}
	http.SetCookie(w, auth.ClearCookie(s.secureCookies()))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCSRF(w http.ResponseWriter, r *http.Request) {
	sess, _ := SessionFrom(r.Context())
	WriteJSON(w, http.StatusOK, map[string]any{"csrfToken": s.auth.CSRF(sess)})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	prefs, err := s.store.GetPrefs(r.Context(), u.ID)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "load preferences failed", slog.String("error", err.Error()))
		WriteError(w, r, CodeInternal, 0, "internal server error", nil)
		return
	}
	language := s.cfg.Runtime().DefaultLanguage
	if raw, ok := prefs["ui.language"]; ok {
		var l string
		if json.Unmarshal(raw, &l) == nil && (l == "id" || l == "en") {
			language = l
		}
	}
	view := userView(u)
	view["quota"] = s.quotaView(r.Context(), u)
	view["language"] = language
	view["prefs"] = prefs
	WriteJSON(w, http.StatusOK, map[string]any{"user": view, "config": s.meConfig()})
}

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	if req.CurrentPassword == "" || req.NewPassword == "" {
		WriteError(w, r, CodeValidationFailed, 0, "currentPassword and newPassword are required",
			map[string]any{"fields": map[string]any{"newPassword": "required"}})
		return
	}
	u, _ := UserFrom(r.Context())
	sess, _ := SessionFrom(r.Context())
	err := s.auth.ChangePassword(r.Context(), u, sess, req.CurrentPassword, req.NewPassword, ClientIP(r), r.UserAgent())
	if err != nil {
		var pe *auth.PolicyError
		var limited *auth.RateLimitedError
		switch {
		case errors.As(err, &pe):
			WriteError(w, r, CodeValidationFailed, 0, "password does not meet the policy",
				map[string]any{"fields": map[string]any{"newPassword": pe.Reason}, "minLength": s.cfg.Runtime().PasswordMinLength})
		case errors.Is(err, auth.ErrInvalidCredentials):
			WriteError(w, r, CodeValidationFailed, 0, "current password is incorrect",
				map[string]any{"fields": map[string]any{"currentPassword": "incorrect"}})
		case errors.As(err, &limited):
			secs := int(limited.RetryAfter.Seconds())
			setRetryAfter(w, secs)
			WriteError(w, r, CodeRateLimited, 0, "too many password changes, try again later", map[string]any{"retryAfter": secs})
		default:
			s.logger.ErrorContext(r.Context(), "change password failed", slog.String("error", err.Error()))
			WriteError(w, r, CodeInternal, 0, "internal server error", nil)
		}
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleGetPreferences(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	prefs, err := s.store.GetPrefs(r.Context(), u.ID)
	if err != nil {
		WriteError(w, r, CodeInternal, 0, "internal server error", nil)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"prefs": prefs})
}

type preferencesRequest struct {
	Prefs map[string]json.RawMessage `json:"prefs"`
}

// handlePutPreferences applies a merge patch: a JSON null removes the key.
// Keys must be registered (prefs.go, SPEC §8.18) and values must match the
// declared shape.
func (s *Server) handlePutPreferences(w http.ResponseWriter, r *http.Request) {
	var req preferencesRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	if req.Prefs == nil {
		WriteError(w, r, CodeValidationFailed, 0, "prefs object required", map[string]any{"fields": map[string]any{"prefs": "required"}})
		return
	}
	u, _ := UserFrom(r.Context())
	prefs, err := s.store.GetPrefs(r.Context(), u.ID)
	if err != nil {
		WriteError(w, r, CodeInternal, 0, "internal server error", nil)
		return
	}
	for k, v := range req.Prefs {
		if len(k) == 0 || len(k) > 64 {
			WriteError(w, r, CodeValidationFailed, 0, "invalid preference key", map[string]any{"fields": map[string]any{k: "invalid_key"}})
			return
		}
		if string(v) == "null" {
			if _, known := prefRegistry[k]; !known && !strings.HasPrefix(k, "editor.") {
				WriteError(w, r, CodeValidationFailed, 0, "unknown preference key", map[string]any{"fields": map[string]any{k: "unknown_key"}})
				return
			}
			delete(prefs, k)
			continue
		}
		if reason := validatePref(k, v); reason != "" {
			WriteError(w, r, CodeValidationFailed, 0, "invalid preference", map[string]any{"fields": map[string]any{k: reason}})
			return
		}
		prefs[k] = v
	}
	if err := s.store.SetPrefs(r.Context(), u.ID, prefs); err != nil {
		WriteError(w, r, CodePayloadTooLarge, 0, "preferences too large", nil)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"prefs": prefs})
}

// userView is the public representation of a user (never the hash or TOTP secret).
func userView(u sqlc.User) map[string]any {
	v := map[string]any{
		"id":                 u.ID,
		"username":           u.Username,
		"displayName":        u.DisplayName,
		"role":               u.Role,
		"status":             u.Status,
		"mustChangePassword": u.MustChangePassword != 0,
		"homeProvisioned":    u.HomeProvisionedAt != nil,
		"quotaBytes":         u.QuotaBytes,
		"createdAt":          u.CreatedAt,
		"totpEnabled":        u.TotpEnabled != 0,
	}
	if u.TotpEnabled != 0 && u.TotpEnabledAt != nil {
		v["totpEnabledAt"] = *u.TotpEnabledAt
	}
	if u.HomeWarning != nil {
		v["homeWarning"] = *u.HomeWarning
	}
	if u.LastLoginAt != nil {
		v["lastLoginAt"] = *u.LastLoginAt
	}
	return v
}

// meConfig exposes the client-relevant limits and feature flags (SPEC §6.3 /auth/me).
func (s *Server) meConfig() map[string]any {
	missing := map[string]bool{}
	for _, t := range s.cfg.MissingTools {
		missing[t] = true
	}
	feature := func(name, path string) bool { return path != "" && !missing[name] }
	// effective values: environment overridden by the admin settings (Step 19a)
	rt := s.cfg.Runtime()
	return map[string]any{
		"appName":                rt.AppName,
		"logoUrl":                logoURL(rt.BrandingLogo),
		"chunkSize":              rt.UploadChunkSize,
		"maxUploadSize":          rt.MaxUploadSize,
		"editorMaxFileSize":      rt.EditorMaxFileSize,
		"remoteDownloadEnabled":  rt.RemoteDownloadEnabled,
		"publicSharesEnabled":    rt.PublicSharesEnabled,
		"shareAllowNoExpiry":     rt.ShareAllowNoExpiry,
		"shareRequirePassword":   rt.ShareRequirePassword,
		"shareDefaultExpiryDays": rt.ShareDefaultExpiryDays,
		"passwordMinLength":      rt.PasswordMinLength,
		"defaultLanguage":        rt.DefaultLanguage,
		"adminCanBrowseUsers":    s.cfg.AdminCanBrowseUsers,
		// Step 23d: the player offers "play with conversion" only when true
		"transcodeEnabled": s.transcode != nil && s.transcode.Enabled(),
		// Step 23e: "" = same origin; otherwise media comes from this origin and
		// the HTML viewer offers the sandboxed preview
		"contentBaseUrl": s.contentBaseURL(),
		"features": map[string]bool{
			"ffmpeg":   feature("ffmpeg", s.cfg.FFmpegPath),
			"ffprobe":  feature("ffprobe", s.cfg.FFprobePath),
			"vips":     feature("vipsthumbnail", s.cfg.VipsPath),
			"pdftoppm": feature("pdftoppm", s.cfg.PdftoppmPath),
			"sevenzip": feature("7z", s.cfg.SevenZipPath),
			"rar":      feature("rar", s.cfg.RarPath),
			"unrar":    feature("unrar", s.cfg.UnrarPath),
			"rg":       feature("rg", s.cfg.RgPath),
		},
	}
}
