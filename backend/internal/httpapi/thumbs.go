package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/download"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/thumbs"
)

// Thumbnail serving limits (SPEC §8.4, PROMPT Step 8 task 4).
const (
	thumbMaxParallelPerUser = 20
	thumbCacheControl       = "private, max-age=86400"
	thumbRetryAfterSeconds  = 2
	// RawCSP isolates any user-derived bytes served inline (SPEC §10.3).
	RawCSP = "sandbox; default-src 'none'"
)

// thumbLimiter bounds concurrent thumbnail requests per user.
type thumbLimiter struct {
	mu sync.Mutex
	n  map[string]int
}

func (l *thumbLimiter) acquire(userID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n == nil {
		l.n = map[string]int{}
	}
	if l.n[userID] >= thumbMaxParallelPerUser {
		return false
	}
	l.n[userID]++
	return true
}

func (l *thumbLimiter) release(userID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.n[userID] <= 1 {
		delete(l.n, userID)
	} else {
		l.n[userID]--
	}
}

// mountThumbs registers GET /files/thumb: authenticated by signature only
// (no cookie/CSRF) so <img> tags, tabs and download managers can use it
// (SPEC §6.3, §8.9).
func (s *Server) mountThumbs(api chi.Router) {
	api.Get("/files/thumb", s.handleFilesThumb)
}

// verifySigned parses and verifies a signed URL of the given purpose and
// resolves its user scope. On failure the error is written and ok=false.
func (s *Server) verifySigned(w http.ResponseWriter, r *http.Request, purpose download.Purpose) (download.Params, string, bool) {
	p, sig, err := download.FromQuery(r.URL.Query())
	if err != nil || p.Purpose != purpose {
		WriteError(w, r, CodeSignatureInvalid, 0, "invalid signed url", nil)
		return p, "", false
	}
	if err := s.signer.Verify(p, sig); err != nil {
		if errors.Is(err, download.ErrSignatureExpired) {
			WriteError(w, r, CodeSignatureExpired, 0, "signed url expired", nil)
		} else {
			WriteError(w, r, CodeSignatureInvalid, 0, "invalid signed url", nil)
		}
		return p, "", false
	}
	userID, ok := strings.CutPrefix(p.Scope, "u:")
	if !ok || userID == "" {
		// share scopes arrive in Step 18a
		WriteError(w, r, CodeSignatureInvalid, 0, "unsupported scope", nil)
		return p, "", false
	}
	return p, userID, true
}

func (s *Server) handleFilesThumb(w http.ResponseWriter, r *http.Request) {
	if s.signer == nil {
		WriteError(w, r, CodeDependencyMissing, 0, "thumbnails are not available", nil)
		return
	}
	p, userID, ok := s.verifySigned(w, r, download.PurposeThumb)
	if !ok {
		return
	}
	if s.thumbs == nil {
		WriteError(w, r, CodeDependencyMissing, 0, "thumbnails are not available", nil)
		return
	}
	size, ok := thumbs.ParseSize(p.Extra)
	if !ok {
		WriteError(w, r, CodeValidationFailed, 0, "invalid size", map[string]any{"fields": map[string]any{"size": "must be 128, 256 or 512"}})
		return
	}
	u, err := s.store.GetUserByID(r.Context(), userID)
	if err != nil || u.Status != "active" || u.DeletedAt != nil {
		WriteError(w, r, CodeNotFound, 0, fsMessages[CodeNotFound], nil)
		return
	}
	if !s.thumbLimit.acquire(u.ID) {
		w.Header().Set("Retry-After", "1")
		WriteError(w, r, CodeRateLimited, 0, "too many thumbnail requests", map[string]any{"limit": thumbMaxParallelPerUser})
		return
	}
	defer s.thumbLimit.release(u.ID)

	vp, ok := cleanPath(w, r, p.Path)
	if !ok {
		return
	}
	j, release, err := s.jails.acquire(s.cfg.StorageRoot, u)
	if err != nil {
		if !WriteFSError(w, r, err) {
			WriteError(w, r, CodeInternal, 0, "storage unavailable", nil)
		}
		return
	}
	defer release()
	info, err := j.Lstat(vp)
	if err != nil {
		if !WriteFSError(w, r, err) {
			WriteError(w, r, CodeNotFound, 0, fsMessages[CodeNotFound], nil)
		}
		return
	}
	if info.IsSymlink || !info.IsRegular() {
		WriteError(w, r, CodeNotFound, 0, "no thumbnail for this item", map[string]any{"reason": "not_a_file"})
		return
	}
	if !thumbs.Thumbable(string(files.Lookup(info.Name).Category)) {
		WriteError(w, r, CodeNotFound, 0, "no thumbnail for this type", map[string]any{"reason": "unsupported"})
		return
	}
	key := thumbs.Key(string(vp), info.Size, info.ModTime.UnixNano())
	if _, ok := s.thumbs.Lookup(u.ID, key, size); ok {
		s.serveThumb(w, r, u.ID, key, size)
		return
	}
	status := s.thumbs.Request(r.Context(), thumbs.Job{UserID: u.ID, Username: u.Username, Path: vp}, info, size)
	switch status {
	case thumbs.StatusReady:
		s.serveThumb(w, r, u.ID, key, size)
	case thumbs.StatusPending:
		w.Header().Set("Retry-After", strconv.Itoa(thumbRetryAfterSeconds))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusAccepted)
	case thumbs.StatusFailed:
		WriteError(w, r, CodeNotFound, 0, "thumbnail could not be generated", map[string]any{"reason": "failed"})
	default:
		WriteError(w, r, CodeNotFound, 0, "no thumbnail for this type", map[string]any{"reason": "unsupported"})
	}
}

// serveThumb streams a cached thumbnail with ETag/304 and a sandboxed CSP.
func (s *Server) serveThumb(w http.ResponseWriter, r *http.Request, userID, key string, size thumbs.Size) {
	fi, ok := s.thumbs.Lookup(userID, key, size)
	if !ok {
		WriteError(w, r, CodeNotFound, 0, "thumbnail missing", nil)
		return
	}
	etag := thumbs.ETag(key, size)
	h := w.Header()
	h.Set("Content-Type", "image/webp")
	h.Set("Cache-Control", thumbCacheControl)
	h.Set("Content-Security-Policy", RawCSP)
	h.Set("ETag", etag)
	h.Set("Content-Disposition", "inline")
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	f, err := s.thumbs.Open(userID, key, size)
	if err != nil {
		WriteError(w, r, CodeNotFound, 0, "thumbnail missing", nil)
		return
	}
	defer func() { _ = f.Close() }()
	http.ServeContent(w, r, "thumb.webp", fi.ModTime(), f)
}

// etagMatches implements If-None-Match for strong validators ("*" and
// comma-separated lists accepted).
func etagMatches(header, etag string) bool {
	if header == "" {
		return false
	}
	if strings.TrimSpace(header) == "*" {
		return true
	}
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		part = strings.TrimPrefix(part, "W/")
		if part == etag {
			return true
		}
	}
	return false
}

// OnChange is the mutation hook of SPEC §8.4: drops cached media rows and
// thumbnails for path (recursively for folders). Rename/copy/move/delete,
// upload finalisation and extraction (Steps 9a, 12a, 14a) call it after the
// filesystem operation succeeded.
func (s *Server) OnChange(r *http.Request, userID string, path fsjail.VPath, isDir bool) {
	if s.thumbs == nil {
		return
	}
	if err := s.thumbs.Invalidate(r.Context(), userID, path, isDir); err != nil {
		s.logger.WarnContext(r.Context(), "thumbnail invalidation failed", "path", string(path), "error", err.Error())
	}
}
