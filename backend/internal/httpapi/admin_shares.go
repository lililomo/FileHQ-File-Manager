package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

// Admin shares of every user (Step 19b, SPEC §6.3 GET /admin/shares, POST
// /admin/shares/{id}/revoke): metadata only, never a link or content.

func (s *Server) handleAdminSharesList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	page, err := s.admin.ListShares(r.Context(), q.Get("cursor"), q.Get("owner"), q.Get("mode"), q.Get("status"))
	if err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, page)
}

func (s *Server) handleAdminShareRevoke(w http.ResponseWriter, r *http.Request) {
	if err := s.admin.RevokeShare(r.Context(), s.actor(r), chi.URLParam(r, "id")); err != nil {
		s.writeAdminError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
