package httpapi

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/upload"
)

// PublicShareTusMarker exempts the guest API from the JSON body cap: the
// tus PATCH bodies below it carry file chunks (limitChunk caps them), the
// JSON routes re-apply JSONBodyLimit themselves.
const PublicShareTusMarker = PublicSharePrefix

// mountPublicShareUploadRoutes registers file-request uploads under
// /public/shares/{token}/upload/* (SPEC §6.3 "Publik", §8.15 file request):
// the guest uploads as the owner into the shared folder, quota is the
// owner's, conflicts always rename, nothing can be overwritten.
func (s *Server) mountPublicShareUploadRoutes(g chi.Router) {
	if s.upload == nil {
		return
	}
	g.With(MaxBytes(JSONBodyLimit)).Post("/upload/precheck", s.handlePublicSharePrecheck)
	tus := s.upload.Tus()
	withPrincipal := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			p, ok := s.resolvePublicUpload(w, r)
			if !ok {
				return
			}
			base := strings.TrimSuffix(PublicSharePrefix, "/") + "/" + p.token + "/upload/tus"
			h := http.StripPrefix(base, tus.Middleware(next))
			ctx := upload.WithPrincipal(r.Context(), upload.Principal{
				UserID: p.owner.ID, Username: p.owner.Username, ShareID: p.row.ID, ShareDest: fsjail.VPath(p.row.Path),
				IP: p.ip, UserAgent: r.UserAgent(),
			})
			// tusd builds Location from the private base path; guests must
			// continue on the public one
			rw := &locationRewriter{ResponseWriter: w, from: strings.TrimSuffix(TusPrefix, "/"), to: base}
			h.ServeHTTP(rw, r.WithContext(ctx))
		}
	}
	for _, p := range []string{"/upload/tus", "/upload/tus/"} {
		g.Post(p, withPrincipal(tus.PostFile))
		g.Options(p, withPrincipal(http.NotFound))
	}
	g.Options("/upload/tus/{id}", withPrincipal(http.NotFound))
	g.Head("/upload/tus/{id}", s.requireShareUploadOwner(withPrincipal(tus.HeadFile)))
	g.Patch("/upload/tus/{id}", s.requireShareUploadOwner(s.limitChunk(withPrincipal(tus.PatchFile))))
	g.Delete("/upload/tus/{id}", s.requireShareUploadOwner(withPrincipal(tus.DelFile)))
}

// locationRewriter swaps the private tus base in Location for the public one.
type locationRewriter struct {
	http.ResponseWriter
	from, to string
	done     bool
}

func (l *locationRewriter) WriteHeader(code int) {
	if !l.done {
		l.done = true
		if loc := l.Header().Get("Location"); loc != "" {
			if i := strings.Index(loc, l.from+"/"); i >= 0 {
				l.Header().Set("Location", loc[:i]+l.to+loc[i+len(l.from):])
			}
		}
	}
	l.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer (tusd
// sets read deadlines per request; without it tusd logs "feature not
// supported" on every POST/PATCH).
func (l *locationRewriter) Unwrap() http.ResponseWriter { return l.ResponseWriter }

func (l *locationRewriter) Write(b []byte) (int, error) {
	if !l.done {
		l.WriteHeader(http.StatusOK)
	}
	return l.ResponseWriter.Write(b)
}

// resolvePublicUpload gates a guest upload: the share must be an unlocked
// folder with allowUpload.
func (s *Server) resolvePublicUpload(w http.ResponseWriter, r *http.Request) (*publicShare, bool) {
	p, ok := s.resolvePublic(w, r, true)
	if !ok {
		return nil, false
	}
	if p.row.IsDir == 0 || p.row.AllowUpload == 0 {
		WriteError(w, r, CodeForbidden, 0, "this link does not accept uploads", map[string]any{"reason": "upload_disabled"})
		return nil, false
	}
	return p, true
}

// requireShareUploadOwner answers 404 unless {id} is an active upload of
// this share (another share's or user's id must look non-existent).
func (s *Server) requireShareUploadOwner(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, ok := s.resolvePublicUpload(w, r)
		if !ok {
			return
		}
		ok, err := s.upload.OwnedActiveShare(r.Context(), p.owner.ID, p.row.ID, chi.URLParam(r, "id"))
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

// handlePublicSharePrecheck: POST …/upload/precheck {files} — the folder
// is always the shared one; conflicts are reported so the client can show
// "akan diganti nama".
func (s *Server) handlePublicSharePrecheck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Files []upload.PrecheckFile `json:"files"`
		Path  string                `json:"path,omitempty"`
	}
	if !ReadJSON(w, r, &req) {
		return
	}
	p, ok := s.resolvePublicUpload(w, r)
	if !ok {
		return
	}
	j, release, ok := s.ownerJail(w, r, p)
	if !ok {
		return
	}
	defer release()
	dest, err := fsjail.Clean(p.row.Path)
	if err != nil {
		WriteError(w, r, CodeShareDisabled, 0, "the shared item is no longer available", map[string]any{"reason": "broken"})
		return
	}
	res, uerr := s.upload.Precheck(r.Context(), j, p.owner.ID, dest, req.Files)
	if uerr != nil {
		s.writeUploadError(w, r, uerr)
		return
	}
	WriteJSON(w, http.StatusOK, res)
}
