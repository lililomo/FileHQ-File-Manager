package httpapi

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/share"
	"kppbwi.app/filemanager/internal/upload"
)

// ShareUploadPrefix is the tus base of uploads into a received share
// (Step 18b): /api/v1/upload/share/{shareId}/tus/. The share id lives in
// the path because tus HEAD/PATCH/DELETE carry no metadata or query.
const ShareUploadPrefix = "/api/v1/upload/share/"

// mountShareUpload registers precheck + tus for editors of a received
// share. The principal is the owner (quota, folder, versions) with the
// recipient journaled; HEAD/PATCH/DELETE must address an active upload of
// this share (404 otherwise).
func (s *Server) mountShareUpload(api chi.Router) {
	if s.upload == nil || s.shares == nil {
		return
	}
	tus := s.upload.Tus()
	withPrincipal := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			sc, ok := s.resolveShareUpload(w, r)
			if !ok {
				return
			}
			base := strings.TrimSuffix(ShareUploadPrefix, "/") + "/" + sc.Share.ID + "/tus"
			h := http.StripPrefix(base, tus.Middleware(next))
			ctx := upload.WithPrincipal(r.Context(), upload.Principal{
				UserID: sc.Owner.ID, Username: sc.Owner.Username, ShareID: sc.Share.ID, ShareDest: fsjail.VPath(sc.Share.Path),
				IP: ClientIP(r), UserAgent: r.UserAgent(), Recipient: sc.Actor.Username, RecipientID: sc.Actor.ID,
			})
			rw := &locationRewriter{ResponseWriter: w, from: strings.TrimSuffix(TusPrefix, "/"), to: base}
			h.ServeHTTP(rw, r.WithContext(ctx))
		}
	}
	api.Route("/upload/share/{shareId}", func(g chi.Router) {
		g.Use(OriginCheck(s.cfg.AppBaseURL))
		g.Use(s.RequireAuth)
		g.Use(s.RequireCSRF)
		g.Use(RequirePasswordChanged)
		g.With(MaxBytes(JSONBodyLimit)).Post("/precheck", s.handleShareUploadPrecheck)
		for _, p := range []string{"/tus", "/tus/"} {
			g.Post(p, withPrincipal(tus.PostFile))
			g.Options(p, withPrincipal(http.NotFound))
		}
		g.Options("/tus/{id}", withPrincipal(http.NotFound))
		g.Head("/tus/{id}", s.requireRecipientUploadOwner(withPrincipal(tus.HeadFile)))
		g.Patch("/tus/{id}", s.requireRecipientUploadOwner(s.limitChunk(withPrincipal(tus.PatchFile))))
		g.Delete("/tus/{id}", s.requireRecipientUploadOwner(withPrincipal(tus.DelFile)))
	})
}

// resolveShareUpload gates an upload into {shareId}: the caller must be an
// editor of an active folder share.
func (s *Server) resolveShareUpload(w http.ResponseWriter, r *http.Request) (*reqScope, bool) {
	actor, ok := UserFrom(r.Context())
	if !ok {
		WriteError(w, r, CodeUnauthenticated, 0, "authentication required", nil)
		return nil, false
	}
	acc, serr := s.shares.RecipientAccess(r.Context(), actor.ID, chi.URLParam(r, "shareId"))
	if serr != nil {
		s.writeShareError(w, r, serr)
		return nil, false
	}
	if !acc.Editor() {
		WriteError(w, r, CodeForbidden, 0, "the share only allows viewing", map[string]any{"reason": "share_permission", "permission": acc.Permission})
		return nil, false
	}
	if acc.Row.IsDir == 0 {
		WriteError(w, r, CodeValidationFailed, 0, "the share is a single file", map[string]any{"reason": "not_a_directory", "path": "/"})
		return nil, false
	}
	return &reqScope{Actor: actor, Owner: acc.Owner, Share: &acc.Row, Perm: acc.Permission, Base: fsjail.Root, Listing: "s:" + acc.Row.ID}, true
}

func (s *Server) requireRecipientUploadOwner(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sc, ok := s.resolveShareUpload(w, r)
		if !ok {
			return
		}
		ok, err := s.upload.OwnedActiveShare(r.Context(), sc.Owner.ID, sc.Share.ID, chi.URLParam(r, "id"))
		if err != nil {
			s.logger.ErrorContext(r.Context(), "upload lookup failed", slog.String("error", err.Error()))
			WriteError(w, r, CodeInternal, 0, "internal server error", nil)
			return
		}
		if !ok {
			WriteError(w, r, CodeNotFound, 0, "upload not found", nil)
			return
		}
		next(w, r)
	}
}

// handleShareUploadPrecheck: POST /upload/share/{shareId}/precheck {path, files}
// — path relative to the shared folder.
func (s *Server) handleShareUploadPrecheck(w http.ResponseWriter, r *http.Request) {
	var req precheckRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	actor, _ := UserFrom(r.Context())
	acc, serr := s.shares.RecipientAccess(r.Context(), actor.ID, chi.URLParam(r, "shareId"))
	if serr != nil {
		s.writeShareError(w, r, serr)
		return
	}
	j, sc, release, ok := s.openShareScope(w, r, actor, acc)
	if !ok {
		return
	}
	defer release()
	if !sc.requireEditor(w, r) || !sc.requireFolderShare(w, r) {
		return
	}
	dest, ok := sc.path(w, r, req.Path)
	if !ok {
		return
	}
	res, uerr := s.upload.Precheck(r.Context(), j, sc.Owner.ID, dest, req.Files)
	if uerr != nil {
		s.writeUploadError(w, r, uerr)
		return
	}
	WriteJSON(w, http.StatusOK, res)
}

var _ = share.PermEditor
