package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/download"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/media"
	"kppbwi.app/filemanager/internal/share"
	"kppbwi.app/filemanager/internal/thumbs"
)

// Streaming content routes (SPEC §8.9); excluded from the API timeout.
const (
	DownloadPath = "/api/v1/files/download"
	RawPath      = "/api/v1/files/raw"
	ZipPath      = "/api/v1/files/zip"

	maxZipPaths            = 2000
	downloadRetryAfterSecs = "5"
)

// mountDownload registers the signed content routes (no cookie: signature
// only, like /files/thumb) and the cookie-authenticated POST /files/zip.
func (s *Server) mountDownload(api chi.Router) {
	for _, m := range []string{http.MethodGet, http.MethodHead} {
		api.Method(m, "/files/download", http.HandlerFunc(s.handleFilesDownload))
		api.Method(m, "/files/raw", http.HandlerFunc(s.handleFilesRaw))
		api.Method(m, "/files/zip", http.HandlerFunc(s.handleFilesZipGet))
	}
	api.Group(func(g chi.Router) {
		g.Use(OriginCheck(s.cfg.AppBaseURL))
		g.Use(s.RequireAuth)
		g.Use(s.RequireCSRF)
		g.Use(RequirePasswordChanged)
		g.Post("/files/zip", s.handleFilesZipCreate)
	})
}

// corp is the Cross-Origin-Resource-Policy of content responses.
func (s *Server) corp() string {
	if s.cfg.ContentBaseURL != "" {
		return "same-site"
	}
	return "same-origin"
}

// acquireDownloadSlot enforces the 3 concurrent downloads/zips per user.
func (s *Server) acquireDownloadSlot(w http.ResponseWriter, r *http.Request, userID string) (func(), bool) {
	release, ok := s.dlLimit.Acquire(userID)
	if !ok {
		w.Header().Set("Retry-After", downloadRetryAfterSecs)
		WriteError(w, r, CodeRateLimited, 0, "too many concurrent downloads", map[string]any{"reason": "concurrent_downloads", "limit": s.dlLimit.Limit()})
		return nil, false
	}
	return release, true
}

func (s *Server) handleFilesDownload(w http.ResponseWriter, r *http.Request) {
	if s.signer == nil {
		WriteError(w, r, CodeDependencyMissing, 0, "signed urls are not available", nil)
		return
	}
	p, ss, ok := s.verifySigned(w, r, download.PurposeDownload)
	if !ok {
		return
	}
	if ss.Share != nil && ss.Share.Row.AllowDownload == 0 && !ss.Share.Editor() {
		WriteError(w, r, CodeForbidden, 0, "the share does not allow downloads", map[string]any{"reason": "download_disabled"})
		return
	}
	release, ok := s.acquireDownloadSlot(w, r, ss.Actor.ID)
	if !ok {
		return
	}
	defer release()
	s.serveSigned(w, r, ss, p.Path, download.PurposeDownload)
}

func (s *Server) handleFilesRaw(w http.ResponseWriter, r *http.Request) {
	if s.signer == nil {
		WriteError(w, r, CodeDependencyMissing, 0, "signed urls are not available", nil)
		return
	}
	q := r.URL.Query()
	purpose, ok := download.ParsePurpose(q.Get("purpose"))
	if !ok || (purpose != download.PurposeMedia && purpose != download.PurposeInline) {
		WriteError(w, r, CodeSignatureInvalid, 0, "invalid signed url", nil)
		return
	}
	p, ss, ok := s.verifySigned(w, r, purpose)
	if !ok {
		return
	}
	switch variant := q.Get("variant"); variant {
	case "":
		s.serveSigned(w, r, ss, p.Path, purpose)
	case "thumb", "preview":
		s.serveVariant(w, r, ss, p.Path, variant)
	default:
		WriteError(w, r, CodeValidationFailed, 0, "invalid variant", map[string]any{"fields": map[string]any{"variant": "must be preview or thumb"}})
	}
}

// serveSigned streams one file with the inline/attachment policy.
func (s *Server) serveSigned(w http.ResponseWriter, r *http.Request, ss *signedScope, rawPath string, purpose download.Purpose) {
	u := ss.User
	j, release, info, ok := s.signedFile(w, r, ss, rawPath, true)
	if !ok {
		return
	}
	defer release()
	f, err := j.Open(info.Path)
	if err != nil {
		if !WriteFSError(w, r, err) {
			WriteError(w, r, CodeNotFound, 0, fsMessages[CodeNotFound], nil)
		}
		return
	}
	defer func() { _ = f.Close() }()
	mt, err := download.SniffMIME(f, media.DetectMime)
	if err != nil {
		WriteError(w, r, CodeInternal, 0, "cannot read file", nil)
		return
	}
	d := download.Policy(mt, info.Name, purpose)
	download.SetContentHeaders(w.Header(), s.corp())
	download.ServeFile(w, r, f, info, d, info.Name)
	if purpose == download.PurposeDownload && r.Method == http.MethodGet && r.Header.Get("Range") == "" && s.files != nil && ss.Share == nil {
		s.files.RecordActivity(r.Context(), u.ID, files.ActDownload, string(info.Path), "", map[string]any{"size": info.Size, "mime": d.ContentType})
	}
}

// serveVariant answers raw?variant=thumb|preview from the thumbnail
// pipeline: preview = 2048 px for raster images (rendered on demand), the
// 512 px thumbnail otherwise; 202 while a thumbnail job is pending.
func (s *Server) serveVariant(w http.ResponseWriter, r *http.Request, ss *signedScope, rawPath, variant string) {
	if s.thumbs == nil {
		WriteError(w, r, CodeDependencyMissing, 0, "thumbnails are not available", nil)
		return
	}
	u := ss.User
	j, release, info, ok := s.signedFile(w, r, ss, rawPath, true)
	if !ok {
		return
	}
	defer release()
	key := thumbs.Key(string(info.Path), info.Size, info.ModTime.UnixNano())
	job := thumbs.Job{UserID: u.ID, Username: u.Username, Path: info.Path}
	if variant == "preview" {
		switch s.thumbs.Preview(r.Context(), j, job, info) {
		case thumbs.StatusReady:
			s.serveThumb(w, r, u.ID, key, thumbs.SizePreview)
			return
		case thumbs.StatusFailed:
			WriteError(w, r, CodeNotFound, 0, "preview could not be generated", map[string]any{"reason": "failed"})
			return
		}
		// unsupported for a large preview: fall back to the 512 thumbnail
	}
	if !thumbs.Thumbable(string(files.Lookup(info.Name).Category)) {
		WriteError(w, r, CodeNotFound, 0, "no thumbnail for this type", map[string]any{"reason": "unsupported"})
		return
	}
	switch s.thumbs.Request(r.Context(), job, info, thumbs.Size512) {
	case thumbs.StatusReady:
		s.serveThumb(w, r, u.ID, key, thumbs.Size512)
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

// zipRequest is POST /files/zip.
type zipRequest struct {
	Paths  []string `json:"paths"`
	Name   string   `json:"name,omitempty"`
	Method string   `json:"method,omitempty"`
}

func (s *Server) handleFilesZipCreate(w http.ResponseWriter, r *http.Request) {
	if s.signer == nil || s.zips == nil {
		WriteError(w, r, CodeDependencyMissing, 0, "zip download is not available", nil)
		return
	}
	var req zipRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	fields := map[string]any{}
	if len(req.Paths) == 0 {
		fields["paths"] = "required"
	}
	if len(req.Paths) > maxZipPaths {
		fields["paths"] = "at most " + strconv.Itoa(maxZipPaths)
	}
	method, ok := download.ParseMethod(req.Method)
	if !ok {
		fields["method"] = "must be store or deflate"
	}
	if req.Name != "" {
		if _, err := fsjail.ValidateName(req.Name); err != nil {
			fields["name"] = "invalid"
		}
	}
	if len(fields) > 0 {
		WriteError(w, r, CodeValidationFailed, 0, "invalid zip request", map[string]any{"fields": fields})
		return
	}
	_, sc, release, ok := s.scoped(w, r)
	if !ok {
		return
	}
	release()
	if !sc.requireFolderShare(w, r) {
		return
	}
	if sc.Shared() && sc.Share.AllowDownload == 0 && !sc.Editor() {
		WriteError(w, r, CodeForbidden, 0, "the share does not allow downloads", map[string]any{"reason": "download_disabled"})
		return
	}
	paths, ok := cleanPaths(w, r, req.Paths)
	if !ok {
		return
	}
	// the root itself can not be an entry; everything else is checked lazily while streaming
	for _, p := range paths {
		if p.IsRoot() {
			WriteError(w, r, CodeValidationFailed, 0, "cannot zip the root folder", map[string]any{"fields": map[string]any{"paths": "root"}})
			return
		}
	}
	strPaths := make([]string, len(paths))
	for i, p := range paths {
		strPaths[i] = string(p)
	}
	zipKey := sc.Owner.ID
	if sc.Shared() {
		zipKey = sc.Listing
	}
	id, err := s.zips.Put(download.ZipRequest{UserID: zipKey, Paths: strPaths, Name: req.Name, Method: method})
	if err != nil {
		s.logger.ErrorContext(r.Context(), "zip request store failed", slog.String("error", err.Error()))
		WriteError(w, r, CodeInternal, 0, "cannot prepare zip", nil)
		return
	}
	if sc.Shared() {
		s.shareAccessLog(r, sc, share.ActZip, paths[0])
	}
	exp := s.signer.Expiry(download.TTLFor(download.PurposeZip, s.cfg.SignedURLTTL, s.cfg.MediaURLTTL))
	url := s.signer.URL(download.Params{Scope: sc.signScope(), Purpose: download.PurposeZip, Extra: id, Exp: exp})
	name := download.ZipFilename(s.cfg.AppName, paths, req.Name, time.Now())
	WriteJSON(w, http.StatusOK, map[string]any{"url": url, "expiresAt": isoTime(exp), "name": name, "count": len(paths)})
}

func isoTime(unix int64) string { return time.Unix(unix, 0).UTC().Format("2006-01-02T15:04:05.000Z") }

func (s *Server) handleFilesZipGet(w http.ResponseWriter, r *http.Request) {
	if s.signer == nil || s.zips == nil {
		WriteError(w, r, CodeDependencyMissing, 0, "zip download is not available", nil)
		return
	}
	p, ss, ok := s.verifySigned(w, r, download.PurposeZip)
	if !ok {
		return
	}
	u := ss.User
	req, err := s.zips.Get(p.Extra, ss.ZipKey())
	if err != nil {
		WriteError(w, r, CodeNotFound, 0, "zip request expired or unknown", map[string]any{"reason": "zip_expired"})
		return
	}
	paths := make([]fsjail.VPath, 0, len(req.Paths))
	for _, raw := range req.Paths {
		vp, err := fsjail.Clean(raw)
		if err != nil || vp.IsRoot() {
			continue
		}
		paths = append(paths, vp)
	}
	if len(paths) == 0 {
		WriteError(w, r, CodeNotFound, 0, "zip request has no valid paths", map[string]any{"reason": "zip_empty"})
		return
	}
	release, ok := s.acquireDownloadSlot(w, r, ss.Actor.ID)
	if !ok {
		return
	}
	defer release()
	var (
		j           *fsjail.Jail
		releaseJail func()
	)
	if ss.Share != nil {
		// recipients stream from the share sub-root: nothing above it exists
		var sc *reqScope
		j, sc, releaseJail, ok = s.openShareScope(w, r, ss.Actor, ss.Share)
		if !ok {
			return
		}
		if !sc.requireFolderShare(w, r) {
			releaseJail()
			return
		}
	} else {
		j, releaseJail, err = s.jails.acquire(s.cfg.StorageRoot, u)
		if err != nil {
			if !WriteFSError(w, r, err) {
				WriteError(w, r, CodeInternal, 0, "storage unavailable", nil)
			}
			return
		}
	}
	defer releaseJail()
	lockPaths := make([]fsjail.VPath, len(paths))
	for i, p := range paths {
		lockPaths[i] = j.Abs(p)
	}
	unlock, err := s.locks.TryAcquire(u.ID, fsjail.Read, "zip", lockPaths...)
	if err != nil {
		if !WriteFSError(w, r, err) {
			WriteError(w, r, CodeOperationInProgress, 0, "another operation holds these paths", nil)
		}
		return
	}
	defer unlock()

	h := w.Header()
	download.SetContentHeaders(h, s.corp())
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", download.ContentDisposition(false, download.ZipFilename(s.cfg.AppName, paths, req.Name, req.CreatedAt)))
	h.Set("Cache-Control", "private, no-store")
	h.Set("Accept-Ranges", "none")
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	stats, err := download.WriteZip(r.Context(), w, download.JailSource(j, paths, j.Redact), req.Method)
	if err != nil && !errors.Is(err, context.Canceled) {
		s.logger.InfoContext(r.Context(), "zip stream aborted", slog.String("error", j.Redact(err.Error())), slog.Int("files", stats.Files))
		return
	}
	if s.files != nil && ss.Share == nil {
		s.files.RecordActivity(r.Context(), u.ID, files.ActDownload, string(paths[0]), "", map[string]any{"zip": true, "files": stats.Files, "bytes": stats.Bytes, "skipped": len(stats.Skipped), "count": len(paths)})
	}
}
