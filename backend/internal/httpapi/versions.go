package httpapi

import (
	"errors"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/download"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/media"
	"kppbwi.app/filemanager/internal/versions"
)

// mountVersions registers the /versions group (SPEC §6.3 "Versions", §8.13
// "Riwayat versi"; Step 15 minimal, completed in Step 16b): list (also
// reachable as GET /files/versions), content, download, restore, delete
// one or all. Owner only: ids of other users answer 404.
func (s *Server) mountVersions(api chi.Router) {
	if s.versions == nil {
		return
	}
	api.Group(func(g chi.Router) {
		g.Use(OriginCheck(s.cfg.AppBaseURL))
		g.Use(s.RequireAuth)
		g.Use(s.RequireCSRF)
		g.Use(RequirePasswordChanged)
		g.Get("/versions", s.handleVersionsList)
		g.Get("/files/versions", s.handleVersionsList)
		g.Delete("/versions", s.handleVersionsDeleteAll)
		g.Get("/versions/{id}/content", s.handleVersionContent)
		g.Get("/versions/{id}/download", s.handleVersionDownload)
		g.Post("/versions/{id}/restore", s.handleVersionRestore)
		g.Delete("/versions/{id}", s.handleVersionDelete)
	})
}

func (s *Server) versionsPath(w http.ResponseWriter, r *http.Request) (fsjail.VPath, bool) {
	vp, err := fsjail.Clean(r.URL.Query().Get("path"))
	if err != nil || vp.IsRoot() {
		WriteError(w, r, CodePathInvalid, 0, "path is not valid", map[string]any{"fields": map[string]any{"path": "invalid"}})
		return "", false
	}
	return vp, true
}

func (s *Server) handleVersionsList(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	vp, ok := s.versionsPath(w, r)
	if !ok {
		return
	}
	u, _ := UserFrom(r.Context())
	items, err := s.versions.List(r.Context(), u.ID, vp)
	if err != nil {
		s.writeSearchError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "limit": s.versions.Limit(), "enabled": s.versions.Enabled()})
}

// handleVersionContent: GET /versions/{id}/content?encoding= — the blob
// decoded like /files/content (read-only, etag "v-<id>").
func (s *Server) handleVersionContent(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	u, _ := UserFrom(r.Context())
	opts := files.ContentOptions{MaxSize: s.cfg.EditorMaxFileSize}
	if enc := r.URL.Query().Get("encoding"); enc != "" {
		n := files.NormalizeEncoding(enc)
		if n == "" {
			WriteError(w, r, CodeValidationFailed, 0, "unsupported encoding", map[string]any{"fields": map[string]any{"encoding": "unsupported"}, "encodings": files.Encodings()})
			return
		}
		opts.Encoding = n
	}
	c, err := s.versions.Content(r.Context(), u.ID, chi.URLParam(r, "id"), opts)
	if err != nil {
		var ve *versions.Error
		if errors.As(err, &ve) {
			s.writeSearchError(w, r, err)
			return
		}
		s.writeContentError(w, r, err, "")
		return
	}
	w.Header().Set("ETag", c.Etag)
	WriteJSON(w, http.StatusOK, c)
}

// handleVersionDownload streams the blob as an attachment named after the
// file with the version number appended (no signed URL: the route is
// session-bound and owner-only).
func (s *Server) handleVersionDownload(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	u, _ := UserFrom(r.Context())
	f, info, v, err := s.versions.OpenForDownload(r.Context(), u.ID, chi.URLParam(r, "id"))
	if err != nil {
		s.writeSearchError(w, r, err)
		return
	}
	defer func() { _ = f.Close() }()
	base := path.Base(v.Path)
	ext := path.Ext(base)
	name := strings.TrimSuffix(base, ext) + ".v" + strconv.FormatInt(v.No, 10) + ext
	mt, err := download.SniffMIME(f, media.DetectMime)
	if err != nil {
		s.writeSearchError(w, r, err)
		return
	}
	download.ServeFile(w, r, f, info, download.Policy(mt, base, download.PurposeDownload), name)
}

func (s *Server) handleVersionRestore(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	id := chi.URLParam(r, "id")
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	v, err := s.versions.Get(r.Context(), u.ID, id)
	if err != nil {
		s.writeSearchError(w, r, err)
		return
	}
	vp, err := fsjail.Clean(v.Path)
	if err != nil {
		s.writeSearchError(w, r, err)
		return
	}
	if s.locks != nil {
		unlock, lerr := s.locks.TryAcquire(u.ID, fsjail.Write, "restore", vp)
		if lerr != nil {
			WriteError(w, r, CodeOperationInProgress, http.StatusLocked, "another operation is running on this file", map[string]any{"path": string(vp)})
			return
		}
		defer unlock()
	}
	res, err := s.versions.Restore(r.Context(), j, u.ID, id)
	if err != nil {
		s.writeSearchError(w, r, err)
		return
	}
	if s.files != nil {
		s.files.RecordActivity(r.Context(), u.ID, files.ActRestore, res.Path, "", map[string]any{"versionId": id, "versionNo": v.No})
	}
	if s.search != nil {
		s.search.InvalidateNames(u.ID)
	}
	w.Header().Set("ETag", res.Etag)
	WriteJSON(w, http.StatusOK, res)
}

func (s *Server) handleVersionDelete(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	u, _ := UserFrom(r.Context())
	if err := s.versions.Delete(r.Context(), u.ID, chi.URLParam(r, "id")); err != nil {
		s.writeSearchError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleVersionsDeleteAll: DELETE /versions?path= removes every version of a file.
func (s *Server) handleVersionsDeleteAll(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	vp, ok := s.versionsPath(w, r)
	if !ok {
		return
	}
	u, _ := UserFrom(r.Context())
	if _, err := s.versions.DeleteAll(r.Context(), u.ID, vp); err != nil {
		s.writeSearchError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
