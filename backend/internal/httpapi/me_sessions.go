package httpapi

import (
	"encoding/hex"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/auth"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Active sessions of the signed-in user (Step 19b, SPEC §6.3 GET
// /me/sessions, DELETE /me/sessions/{id}, POST /me/sessions/revoke-others,
// §8.18). Ids are the first 8 hex characters of the session hash; another
// user's session answers 404 (same as unknown). The current session is
// ended through POST /auth/logout, not here.

const sessionIDPrefix = 8

func (s *Server) mountMeSessions(p chi.Router) {
	p.Get("/me/sessions", s.handleMeSessions)
	p.Delete("/me/sessions/{id}", s.handleMeSessionDelete)
	p.Post("/me/sessions/revoke-others", s.handleMeSessionsRevokeOthers)
}

type sessionView struct {
	ID          string `json:"id"`
	Current     bool   `json:"current"`
	IP          string `json:"ip"`
	DeviceLabel string `json:"deviceLabel"`
	CreatedAt   string `json:"createdAt"`
	LastSeenAt  string `json:"lastSeenAt"`
}

func (s *Server) activeSessions(r *http.Request) ([]sqlc.Session, bool) {
	u, _ := UserFrom(r.Context())
	rows, err := s.auth.Sessions.ListActive(r.Context(), u.ID)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "list sessions failed", slog.String("error", err.Error()))
		return nil, false
	}
	return rows, true
}

func (s *Server) handleMeSessions(w http.ResponseWriter, r *http.Request) {
	cur, _ := SessionFrom(r.Context())
	rows, ok := s.activeSessions(r)
	if !ok {
		WriteError(w, r, CodeInternal, 0, "internal server error", nil)
		return
	}
	items := make([]sessionView, 0, len(rows))
	for _, row := range rows {
		label := ""
		if row.DeviceLabel != nil {
			label = *row.DeviceLabel
		}
		if label == "" {
			label = auth.DeviceLabel(row.UserAgent)
		}
		items = append(items, sessionView{ID: row.ID[:sessionIDPrefix], Current: row.ID == cur.ID, IP: row.Ip, DeviceLabel: label,
			CreatedAt: row.CreatedAt, LastSeenAt: row.LastSeenAt})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) handleMeSessionDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.ToLower(chi.URLParam(r, "id"))
	if _, err := hex.DecodeString(id); err != nil || len(id) != sessionIDPrefix {
		WriteError(w, r, CodeNotFound, 0, "session not found", nil)
		return
	}
	cur, _ := SessionFrom(r.Context())
	u, _ := UserFrom(r.Context())
	rows, ok := s.activeSessions(r)
	if !ok {
		WriteError(w, r, CodeInternal, 0, "internal server error", nil)
		return
	}
	var match []sqlc.Session
	for _, row := range rows {
		if strings.HasPrefix(row.ID, id) {
			match = append(match, row)
		}
	}
	if len(match) != 1 {
		WriteError(w, r, CodeNotFound, 0, "session not found", nil)
		return
	}
	if match[0].ID == cur.ID {
		WriteError(w, r, CodeConflict, 0, "use logout to end the current session", map[string]any{"reason": "current_session"})
		return
	}
	if err := s.auth.Sessions.Revoke(r.Context(), match[0].ID); err != nil {
		s.logger.ErrorContext(r.Context(), "revoke session failed", slog.String("error", err.Error()))
		WriteError(w, r, CodeInternal, 0, "internal server error", nil)
		return
	}
	s.audit.Record(r.Context(), audit.Entry{ActorID: u.ID, ActorUsername: u.Username, Action: audit.ActionRevokeSessions, TargetType: "user", TargetID: u.ID,
		IP: ClientIP(r), UserAgent: r.UserAgent(), Details: map[string]any{"scope": "one", "session": id}})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMeSessionsRevokeOthers(w http.ResponseWriter, r *http.Request) {
	cur, _ := SessionFrom(r.Context())
	u, _ := UserFrom(r.Context())
	n, err := s.auth.Sessions.RevokeAllExcept(r.Context(), u.ID, cur.ID)
	if err != nil {
		s.logger.ErrorContext(r.Context(), "revoke sessions failed", slog.String("error", err.Error()))
		WriteError(w, r, CodeInternal, 0, "internal server error", nil)
		return
	}
	s.audit.Record(r.Context(), audit.Entry{ActorID: u.ID, ActorUsername: u.Username, Action: audit.ActionRevokeSessions, TargetType: "user", TargetID: u.ID,
		IP: ClientIP(r), UserAgent: r.UserAgent(), Details: map[string]any{"scope": "others", "count": n}})
	w.WriteHeader(http.StatusNoContent)
}
