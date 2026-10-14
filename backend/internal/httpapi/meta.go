package httpapi

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/meta"
)

// mountMeta registers /meta/* (SPEC §6.3 "Meta", §8.7): favorites, pins,
// notes and colour tags. S+P, CSRF on writes, same-origin only.
func (s *Server) mountMeta(api chi.Router) {
	if s.meta == nil {
		return
	}
	api.Group(func(g chi.Router) {
		g.Use(OriginCheck(s.cfg.AppBaseURL))
		g.Use(s.RequireAuth)
		g.Use(s.RequireCSRF)
		g.Use(RequirePasswordChanged)
		g.Get("/meta/favorites", s.handleMetaFavoritesList)
		g.Put("/meta/favorites", s.handleMetaFavoritesPut)
		g.Delete("/meta/favorites", s.handleMetaFavoritesDelete)
		g.Get("/meta/pins", s.handleMetaPinsList)
		g.Put("/meta/pins", s.handleMetaPinsPut)
		g.Delete("/meta/pins", s.handleMetaPinsDelete)
		g.Put("/meta/notes", s.handleMetaNotePut)
		g.Delete("/meta/notes", s.handleMetaNoteDelete)
		g.Put("/meta/tags", s.handleMetaTagsPut)
		g.Delete("/meta/tags", s.handleMetaTagsDelete)
		g.Get("/meta/tagged", s.handleMetaTagged)
	})
}

// metaPathsRequest accepts {paths:[…]} or the single-item alias {path}.
type metaPathsRequest struct {
	Paths []string `json:"paths,omitempty"`
	Path  string   `json:"path,omitempty"`
	Color string   `json:"color,omitempty"`
	Note  *string  `json:"note,omitempty"`
}

func (s *Server) metaPaths(w http.ResponseWriter, r *http.Request, req metaPathsRequest) ([]fsjail.VPath, bool) {
	raw := req.Paths
	if len(raw) == 0 && req.Path != "" {
		raw = []string{req.Path}
	}
	if len(raw) == 0 {
		WriteError(w, r, CodeValidationFailed, 0, "paths are required", map[string]any{"fields": map[string]any{"paths": "required"}})
		return nil, false
	}
	if len(raw) > meta.MaxBatchPaths {
		WriteError(w, r, CodeValidationFailed, 0, "too many paths", map[string]any{"fields": map[string]any{"paths": "too_many"}, "max": meta.MaxBatchPaths})
		return nil, false
	}
	out := make([]fsjail.VPath, 0, len(raw))
	for _, p := range raw {
		vp, ok := cleanPath(w, r, p)
		if !ok {
			return nil, false
		}
		if vp.IsRoot() {
			WriteError(w, r, CodeValidationFailed, 0, "the root folder cannot carry metadata", map[string]any{"fields": map[string]any{"paths": "root_not_allowed"}})
			return nil, false
		}
		out = append(out, vp)
	}
	return out, true
}

// metaWrite runs a batch mutation that needs the jail and writes the result.
func (s *Server) metaWrite(w http.ResponseWriter, r *http.Request, fn func(j *fsjail.Jail, userID string, paths []fsjail.VPath) (meta.Result, error)) {
	if rejectScope(w, r) {
		return
	}
	var req metaPathsRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	paths, ok := s.metaPaths(w, r, req)
	if !ok {
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	res, err := fn(j, u.ID, paths)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, res)
}

// metaRemove runs a batch removal (no jail: broken references can be removed).
func (s *Server) metaRemove(w http.ResponseWriter, r *http.Request, fn func(userID string, paths []fsjail.VPath) (meta.Result, error)) {
	if rejectScope(w, r) {
		return
	}
	var req metaPathsRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	paths, ok := s.metaPaths(w, r, req)
	if !ok {
		return
	}
	u, _ := UserFrom(r.Context())
	if _, err := fn(u.ID, paths); err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMetaFavoritesList(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	items, err := s.meta.ListFavorites(r.Context(), j, u.ID, s.userLocale(r, u.ID))
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items)})
}

func (s *Server) handleMetaFavoritesPut(w http.ResponseWriter, r *http.Request) {
	s.metaWrite(w, r, func(j *fsjail.Jail, userID string, paths []fsjail.VPath) (meta.Result, error) {
		return s.meta.AddFavorites(r.Context(), j, userID, paths)
	})
}

func (s *Server) handleMetaFavoritesDelete(w http.ResponseWriter, r *http.Request) {
	s.metaRemove(w, r, func(userID string, paths []fsjail.VPath) (meta.Result, error) {
		return s.meta.RemoveFavorites(r.Context(), userID, paths)
	})
}

func (s *Server) handleMetaPinsList(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	dir, ok := cleanPath(w, r, r.URL.Query().Get("dir"))
	if !ok {
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	items, err := s.meta.ListPins(r.Context(), j, u.ID, dir)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "dir": string(dir), "max": meta.MaxPinsPerFolder})
}

func (s *Server) handleMetaPinsPut(w http.ResponseWriter, r *http.Request) {
	s.metaWrite(w, r, func(j *fsjail.Jail, userID string, paths []fsjail.VPath) (meta.Result, error) {
		return s.meta.AddPins(r.Context(), j, userID, paths)
	})
}

func (s *Server) handleMetaPinsDelete(w http.ResponseWriter, r *http.Request) {
	s.metaRemove(w, r, func(userID string, paths []fsjail.VPath) (meta.Result, error) {
		return s.meta.RemovePins(r.Context(), userID, paths)
	})
}

func (s *Server) handleMetaNotePut(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req metaPathsRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	if req.Note == nil {
		WriteError(w, r, CodeValidationFailed, 0, "note is required", map[string]any{"fields": map[string]any{"note": "required"}})
		return
	}
	paths, ok := s.metaPaths(w, r, req)
	if !ok {
		return
	}
	if len(paths) != 1 {
		WriteError(w, r, CodeValidationFailed, 0, "one path per note", map[string]any{"fields": map[string]any{"paths": "single"}})
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	if err := s.meta.SetNote(r.Context(), j, u.ID, paths[0], *req.Note); err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"path": string(paths[0]), "note": *req.Note, "max": meta.MaxNoteChars})
}

func (s *Server) handleMetaNoteDelete(w http.ResponseWriter, r *http.Request) {
	s.metaRemove(w, r, func(userID string, paths []fsjail.VPath) (meta.Result, error) {
		for _, p := range paths {
			if err := s.meta.RemoveNote(r.Context(), userID, p); err != nil {
				return meta.Result{}, err
			}
		}
		return meta.Result{}, nil
	})
}

func (s *Server) handleMetaTagsPut(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req metaPathsRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	if !meta.ValidColor(req.Color) {
		WriteError(w, r, CodeValidationFailed, 0, "unknown colour", map[string]any{"fields": map[string]any{"color": "invalid"}, "colors": meta.Colors})
		return
	}
	paths, ok := s.metaPaths(w, r, req)
	if !ok {
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	res, err := s.meta.SetTags(r.Context(), j, u.ID, paths, req.Color)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"paths": res.Paths, "count": res.Count, "color": req.Color})
}

func (s *Server) handleMetaTagsDelete(w http.ResponseWriter, r *http.Request) {
	s.metaRemove(w, r, func(userID string, paths []fsjail.VPath) (meta.Result, error) {
		return s.meta.RemoveTags(r.Context(), userID, paths)
	})
}

func (s *Server) handleMetaTagged(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	color := r.URL.Query().Get("color")
	if !meta.ValidColor(color) {
		WriteError(w, r, CodeValidationFailed, 0, "unknown colour", map[string]any{"fields": map[string]any{"color": "invalid"}, "colors": meta.Colors})
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	items, err := s.meta.ListTagged(r.Context(), j, u.ID, color, s.userLocale(r, u.ID))
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "total": len(items), "color": color})
}
