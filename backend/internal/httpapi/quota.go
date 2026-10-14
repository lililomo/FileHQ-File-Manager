package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/db/sqlc"
)

// mountQuota registers GET /me/quota (SPEC §6.3 "me", §8.2).
func (s *Server) mountQuota(api chi.Router) {
	api.Group(func(g chi.Router) {
		g.Use(OriginCheck(s.cfg.AppBaseURL))
		g.Use(s.RequireAuth)
		g.Use(s.RequireCSRF)
		g.Use(RequirePasswordChanged)
		g.Get("/me/quota", s.handleMeQuota)
	})
}

// quotaView is the `quota` object of /auth/me and /me/quota.
func (s *Server) quotaView(ctx context.Context, u sqlc.User) map[string]any {
	out := map[string]any{"limit": u.QuotaBytes, "quota": u.QuotaBytes, "used": int64(0), "trash": int64(0), "versions": int64(0),
		"reserved": int64(0), "free": u.QuotaBytes, "percent": 0.0, "dirty": true, "calculatedAt": nil}
	if s.quota == nil {
		return out
	}
	// Usage re-reads the user row: the session cache (5 s) must not hide a
	// limit an admin just changed.
	usage, err := s.quota.Usage(ctx, u.ID)
	if err != nil {
		s.logger.ErrorContext(ctx, "quota usage failed", slog.String("error", err.Error()))
		return out
	}
	out["limit"], out["quota"] = usage.Limit, usage.Limit
	out["used"], out["trash"], out["versions"], out["reserved"] = usage.Used, usage.Trash, usage.Versions, usage.Reserved
	out["free"], out["percent"], out["dirty"] = usage.Free, usage.Percent, usage.Dirty
	out["fileCount"], out["dirCount"] = usage.FileCount, usage.DirCount
	if usage.CalculatedAt != nil {
		out["calculatedAt"] = *usage.CalculatedAt
	}
	return out
}

func (s *Server) handleMeQuota(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	WriteJSON(w, http.StatusOK, s.quotaView(r.Context(), u))
}
