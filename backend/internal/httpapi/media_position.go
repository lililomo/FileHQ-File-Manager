package httpapi

import (
	"database/sql"
	"errors"
	"math"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/fsjail"
)

// Playback positions (SPEC §4.2 media_positions, §8.14 VideoPlayer "lanjut
// dari posisi terakhir"; Step 17b): one row per (user, path), at most
// MaxMediaPositions per user — the least recently updated rows are pruned on
// every write.

// MaxMediaPositions is the per-user cap (LRU).
const MaxMediaPositions = 500

// maxMediaSeconds bounds a position (≈ 115 days) so garbage cannot be stored.
const maxMediaSeconds = 1e7

type mediaPosition struct {
	Path      string   `json:"path"`
	Seconds   float64  `json:"seconds"`
	Duration  *float64 `json:"duration,omitempty"`
	UpdatedAt *string  `json:"updatedAt"`
}

func (s *Server) mountMediaPosition(api chi.Router) {
	api.Group(func(g chi.Router) {
		g.Use(OriginCheck(s.cfg.AppBaseURL))
		g.Use(s.RequireAuth)
		g.Use(s.RequireCSRF)
		g.Use(RequirePasswordChanged)
		g.Get("/me/media-position", s.handleMediaPositionGet)
		g.Put("/me/media-position", s.handleMediaPositionPut)
	})
}

// handleMediaPositionGet: GET /me/media-position?path → the stored position,
// or seconds 0 / updatedAt null when there is none.
func (s *Server) handleMediaPositionGet(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	p, ok := cleanPath(w, r, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	u, _ := UserFrom(r.Context())
	row, err := s.store.GetMediaPosition(r.Context(), sqlc.GetMediaPositionParams{UserID: u.ID, Path: string(p)})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			WriteJSON(w, http.StatusOK, mediaPosition{Path: string(p)})
			return
		}
		WriteError(w, r, CodeInternal, 0, "could not load the position", nil)
		return
	}
	WriteJSON(w, http.StatusOK, mediaPosition{Path: row.Path, Seconds: row.Seconds, Duration: row.Duration, UpdatedAt: &row.UpdatedAt})
}

// handleMediaPositionPut: PUT /me/media-position {path, seconds, duration?}.
// seconds 0 forgets the position (playback finished or restarted).
func (s *Server) handleMediaPositionPut(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req struct {
		Path     string   `json:"path"`
		Seconds  *float64 `json:"seconds"`
		Duration *float64 `json:"duration"`
	}
	if !ReadJSON(w, r, &req) {
		return
	}
	fields := map[string]any{}
	vp, err := fsjail.Clean(req.Path)
	if err != nil || vp.IsRoot() {
		fields["path"] = "invalid"
	}
	if req.Seconds == nil || math.IsNaN(*req.Seconds) || *req.Seconds < 0 || *req.Seconds > maxMediaSeconds {
		fields["seconds"] = "invalid"
	}
	if req.Duration != nil && (math.IsNaN(*req.Duration) || *req.Duration < 0 || *req.Duration > maxMediaSeconds) {
		fields["duration"] = "invalid"
	}
	if len(fields) > 0 {
		WriteError(w, r, CodeValidationFailed, 0, "invalid media position", map[string]any{"fields": fields})
		return
	}
	u, _ := UserFrom(r.Context())
	if *req.Seconds == 0 {
		if _, err := s.store.DeleteMediaPosition(r.Context(), sqlc.DeleteMediaPositionParams{UserID: u.ID, Path: string(vp)}); err != nil {
			WriteError(w, r, CodeInternal, 0, "could not save the position", nil)
			return
		}
		WriteJSON(w, http.StatusOK, mediaPosition{Path: string(vp)})
		return
	}
	row, err := s.store.UpsertMediaPosition(r.Context(), sqlc.UpsertMediaPositionParams{
		UserID: u.ID, Path: string(vp), Seconds: math.Round(*req.Seconds*1000) / 1000, Duration: req.Duration,
		UpdatedAt: db.FormatTime(time.Now()),
	})
	if err != nil {
		WriteError(w, r, CodeInternal, 0, "could not save the position", nil)
		return
	}
	if _, err := s.store.PruneMediaPositions(r.Context(), sqlc.PruneMediaPositionsParams{UserID: u.ID, Keep: MaxMediaPositions}); err != nil {
		s.logger.WarnContext(r.Context(), "prune media positions failed", "error", err.Error())
	}
	WriteJSON(w, http.StatusOK, mediaPosition{Path: row.Path, Seconds: row.Seconds, Duration: row.Duration, UpdatedAt: &row.UpdatedAt})
}
