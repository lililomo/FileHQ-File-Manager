package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/upload"
)

// TusPrefix is the resumable upload endpoint (SPEC §6.5). It is excluded
// from the JSON body limit and the API timeout: a PATCH streams up to one
// chunk and may legitimately take minutes on a slow link.
const TusPrefix = "/api/v1/upload/tus/"

// mountUpload registers POST /upload/precheck and the tus routes (SPEC
// §6.3 "Upload", §6.5). Same gates as the file operations: origin check,
// session, CSRF on POST/PATCH/DELETE (HEAD/OPTIONS are safe) and the
// password-change gate. HEAD/PATCH/DELETE additionally require the upload
// to belong to the caller (404 otherwise, SPEC §8.1 IDOR).
func (s *Server) mountUpload(api chi.Router) {
	if s.upload == nil {
		return
	}
	tus := s.upload.Tus()
	// tusd's unrouted handler expects the base path stripped ("/" or "/{id}").
	withPrincipal := func(next http.HandlerFunc) http.HandlerFunc {
		h := http.StripPrefix(strings.TrimSuffix(TusPrefix, "/"), tus.Middleware(next))
		return func(w http.ResponseWriter, r *http.Request) {
			u, _ := UserFrom(r.Context())
			ctx := upload.WithPrincipal(r.Context(), upload.Principal{UserID: u.ID, Username: u.Username})
			h.ServeHTTP(w, r.WithContext(ctx))
		}
	}
	api.Group(func(g chi.Router) {
		g.Use(OriginCheck(s.cfg.AppBaseURL))
		g.Use(s.RequireAuth)
		g.Use(s.RequireCSRF)
		g.Use(RequirePasswordChanged)
		g.Post("/upload/precheck", s.handleUploadPrecheck)
		// tus-js-client posts to the endpoint with its trailing slash; both
		// spellings map to the same documented operation.
		for _, p := range []string{"/upload/tus", "/upload/tus/"} {
			g.Post(p, withPrincipal(tus.PostFile))
			g.Options(p, withPrincipal(http.NotFound))
		}
		g.Options("/upload/tus/{id}", withPrincipal(http.NotFound))
		// HEAD/PATCH go through upload.Handler, which finalises a complete
		// upload whose last PATCH failed in the pre-finish hook (review C).
		g.Head("/upload/tus/{id}", s.requireUploadOwner(withPrincipal(tus.HeadFile)))
		g.Patch("/upload/tus/{id}", s.requireUploadOwner(s.limitChunk(withPrincipal(tus.PatchFile))))
		g.Delete("/upload/tus/{id}", s.requireUploadOwner(withPrincipal(tus.DelFile)))
	})
}

// requireUploadOwner answers 404 unless {id} is an active upload of the
// authenticated user (another user's id must look non-existent).
func (s *Server) requireUploadOwner(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, _ := UserFrom(r.Context())
		id := chi.URLParam(r, "id")
		ok, err := s.upload.OwnedActive(r.Context(), u.ID, id)
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

// limitChunk rejects PATCH bodies larger than UPLOAD_CHUNK_SIZE (SPEC §3.3)
// before tusd reads them.
func (s *Server) limitChunk(next http.HandlerFunc) http.HandlerFunc {
	limit := s.upload.ChunkSize()
	return func(w http.ResponseWriter, r *http.Request) {
		if limit > 0 {
			if r.ContentLength > limit {
				WriteError(w, r, CodePayloadTooLarge, 0, "chunk exceeds UPLOAD_CHUNK_SIZE", map[string]any{"reason": "chunk_too_large", "max": limit, "size": r.ContentLength})
				return
			}
			if r.Body != nil && r.Body != http.NoBody {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
		}
		next(w, r)
	}
}

type precheckRequest struct {
	Path  string                `json:"path"`
	Files []upload.PrecheckFile `json:"files"`
}

// handleUploadPrecheck answers POST /upload/precheck (SPEC §8.8 step 2).
func (s *Server) handleUploadPrecheck(w http.ResponseWriter, r *http.Request) {
	var req precheckRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	dest, err := fsjail.Clean(req.Path)
	if err != nil {
		if !WriteFSError(w, r, err) {
			WriteError(w, r, CodePathInvalid, 0, fsMessages[CodePathInvalid], map[string]any{"fields": map[string]string{"path": "invalid"}})
		}
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	res, uerr := s.upload.Precheck(r.Context(), j, u.ID, dest, req.Files)
	if uerr != nil {
		s.writeUploadError(w, r, uerr)
		return
	}
	WriteJSON(w, http.StatusOK, res)
}

// writeUploadError maps upload.Error to the envelope.
func (s *Server) writeUploadError(w http.ResponseWriter, r *http.Request, err error) {
	var ue *upload.Error
	if errors.As(err, &ue) {
		code := ErrorCode(ue.Code)
		if _, known := statusByCode[code]; !known {
			code = CodeInternal
		}
		WriteError(w, r, code, ue.Status, ue.Message, ue.Details)
		return
	}
	s.logger.ErrorContext(r.Context(), "upload failed", slog.String("error", err.Error()))
	WriteError(w, r, CodeInternal, 0, "internal server error", nil)
}
