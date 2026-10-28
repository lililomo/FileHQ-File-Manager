package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/auth"
)

// TOTP two-factor authentication of the signed-in user (Step 23a, SPEC §6.3
// /me/totp/*, §8.18, §10.4). Mounted under the S+P group: a session that
// still has to change its password cannot touch the second factor.

func (s *Server) mountMeTOTP(p chi.Router) {
	p.Post("/me/totp/setup", s.handleTOTPSetup)
	p.Post("/me/totp/enable", s.handleTOTPEnable)
	p.Post("/me/totp/disable", s.handleTOTPDisable)
	p.Post("/me/totp/recovery-codes", s.handleTOTPRecoveryCodes)
}

func (s *Server) handleTOTPSetup(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	issuer := s.cfg.Runtime().AppName
	res, err := s.auth.SetupTOTP(r.Context(), u, issuer)
	if err != nil {
		s.writeTOTPError(w, r, err, "code")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	WriteJSON(w, http.StatusOK, map[string]any{"secret": res.Secret, "otpauthUri": res.OTPAuthURI})
}

type totpCodeRequest struct {
	Code     string `json:"code"`
	Password string `json:"password,omitempty"`
}

func (s *Server) handleTOTPEnable(w http.ResponseWriter, r *http.Request) {
	var req totpCodeRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	if req.Code == "" || len(req.Code) > 16 {
		WriteError(w, r, CodeValidationFailed, 0, "code is required", map[string]any{"fields": map[string]any{"code": "required"}})
		return
	}
	u, _ := UserFrom(r.Context())
	codes, enabledAt, err := s.auth.EnableTOTP(r.Context(), u, req.Code, auth.Actor{IP: ClientIP(r), UserAgent: r.UserAgent()})
	if err != nil {
		s.writeTOTPError(w, r, err, "code")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	WriteJSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes, "enabledAt": enabledAt})
}

func (s *Server) handleTOTPDisable(w http.ResponseWriter, r *http.Request) {
	var req totpCodeRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	fields := map[string]any{}
	if req.Password == "" || len(req.Password) > 1024 {
		fields["password"] = "required"
	}
	if req.Code == "" || len(req.Code) > 32 {
		fields["code"] = "required"
	}
	if len(fields) > 0 {
		WriteError(w, r, CodeValidationFailed, 0, "password and code are required", map[string]any{"fields": fields})
		return
	}
	u, _ := UserFrom(r.Context())
	if err := s.auth.DisableTOTP(r.Context(), u, req.Password, req.Code, auth.Actor{IP: ClientIP(r), UserAgent: r.UserAgent()}); err != nil {
		s.writeTOTPError(w, r, err, "code")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTOTPRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	var req totpCodeRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	if req.Password == "" || len(req.Password) > 1024 {
		WriteError(w, r, CodeValidationFailed, 0, "password is required", map[string]any{"fields": map[string]any{"password": "required"}})
		return
	}
	u, _ := UserFrom(r.Context())
	codes, err := s.auth.RegenerateRecoveryCodes(r.Context(), u, req.Password, auth.Actor{IP: ClientIP(r), UserAgent: r.UserAgent()})
	if err != nil {
		s.writeTOTPError(w, r, err, "code")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	WriteJSON(w, http.StatusOK, map[string]any{"recoveryCodes": codes})
}

// writeTOTPError maps the auth TOTP errors to SPEC §6.2 codes; codeField is
// the field name reported for an invalid code ("code" on /me/totp, "totp"
// on login).
func (s *Server) writeTOTPError(w http.ResponseWriter, r *http.Request, err error, codeField string) {
	var limited *auth.RateLimitedError
	switch {
	case errors.As(err, &limited):
		secs := int(limited.RetryAfter.Seconds())
		setRetryAfter(w, secs)
		WriteError(w, r, CodeRateLimited, 0, "too many attempts, try again later", map[string]any{"retryAfter": secs})
	case errors.Is(err, auth.ErrTOTPInvalid):
		WriteError(w, r, CodeValidationFailed, 0, "verification code is invalid",
			map[string]any{"fields": map[string]any{codeField: "totp_invalid"}})
	case errors.Is(err, auth.ErrInvalidCredentials):
		WriteError(w, r, CodeValidationFailed, 0, "password is incorrect",
			map[string]any{"fields": map[string]any{"password": "incorrect"}})
	case errors.Is(err, auth.ErrTOTPEnabled):
		WriteError(w, r, CodeConflict, 0, "two-factor authentication is already enabled", map[string]any{"reason": "totp_enabled"})
	case errors.Is(err, auth.ErrTOTPNotSetup):
		WriteError(w, r, CodeConflict, 0, "run setup first", map[string]any{"reason": "totp_not_setup"})
	case errors.Is(err, auth.ErrTOTPNotEnabled):
		WriteError(w, r, CodeConflict, 0, "two-factor authentication is not enabled", map[string]any{"reason": "totp_disabled"})
	default:
		s.logger.ErrorContext(r.Context(), "totp operation failed", slog.String("error", err.Error()))
		WriteError(w, r, CodeInternal, 0, "internal server error", nil)
	}
}
