package httpapi

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/archive"
	"kppbwi.app/filemanager/internal/download"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/media"
)

// Archive browser routes (SPEC §6.3 "Archives", PROMPT Step 14b). The
// password of an encrypted archive travels in the X-Archive-Password header
// (never a query string, never logged); entry-content is signature-only so
// <img>/fetch can load it and takes the archive session id instead.
const (
	EntryContentPath = "/api/v1/archives/entry-content"
	// ArchivePasswordHeader carries the archive password on GET /archives/entries.
	ArchivePasswordHeader = "X-Archive-Password" //nolint:gosec // HTTP header name, not a credential
	entryCacheControl     = "private, max-age=300"
)

func (s *Server) mountArchiveBrowser(api chi.Router) {
	for _, m := range []string{http.MethodGet, http.MethodHead} {
		api.Method(m, "/archives/entry-content", http.HandlerFunc(s.handleArchiveEntryContent))
	}
	api.Group(func(g chi.Router) {
		g.Use(s.RequireAuth)
		g.Use(RequirePasswordChanged)
		g.Get("/archives/entries", s.handleArchiveEntries)
	})
	api.Group(func(g chi.Router) {
		g.Use(OriginCheck(s.cfg.AppBaseURL))
		g.Use(s.RequireAuth)
		g.Use(s.RequireCSRF)
		g.Use(RequirePasswordChanged)
		g.Post("/archives/sign", s.handleArchiveSign)
		g.Post("/archives/extract-entries", s.handleArchiveExtractEntries)
		g.Post("/archives/add", s.handleArchiveAdd)
		g.Post("/archives/remove-entries", s.handleArchiveRemove)
		g.Post("/archives/remove-entry", s.handleArchiveRemove)
	})
}

// handleArchiveEntries: GET /archives/entries?path&inner&encoding&cursor&limit.
func (s *Server) handleArchiveEntries(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	q := r.URL.Query()
	if q.Get("password") != "" {
		WriteError(w, r, CodeValidationFailed, 0, "send the archive password in the X-Archive-Password header", map[string]any{"fields": map[string]any{"password": "use_header"}})
		return
	}
	limit := 0
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			WriteError(w, r, CodeValidationFailed, 0, "invalid limit", map[string]any{"fields": map[string]any{"limit": "1..1000"}})
			return
		}
		limit = n
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	resp, err := s.archives.Entries(r.Context(), j, archive.EntriesRequest{UserID: u.ID, Archive: q.Get("path"), Inner: q.Get("inner"),
		Password: r.Header.Get(ArchivePasswordHeader), Encoding: firstNonEmpty(q.Get("encoding"), q.Get("name_encoding")), Cursor: q.Get("cursor"), Limit: limit})
	if err != nil {
		s.writeArchiveBrowserError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, resp)
}

// writeArchiveBrowserError maps ARCHIVE_PASSWORD_REQUIRED to 401 on the
// browser endpoints (PROMPT 14b) and everything else like the task API.
func (s *Server) writeArchiveBrowserError(w http.ResponseWriter, r *http.Request, err error) {
	var ae *archive.Error
	if errors.As(err, &ae) && ae.Code == archive.CodePasswordReq {
		WriteError(w, r, CodeArchivePasswordReq, http.StatusUnauthorized, ae.Message, ae.Details)
		return
	}
	s.writeArchiveError(w, r, err)
}

type archiveSignRequest struct {
	Path    string `json:"path"`
	Archive string `json:"archive"`
	Inner   string `json:"inner"`
	Entry   string `json:"entry"`
	Session string `json:"session"`
}

// handleArchiveSign: POST /archives/sign {path, inner, session?} → signed
// entry-content URL (purpose entry, media TTL). The session id is appended
// unsigned: it is a bearer secret bound to this user and archive.
func (s *Server) handleArchiveSign(w http.ResponseWriter, r *http.Request) {
	if s.signer == nil {
		WriteError(w, r, CodeDependencyMissing, 0, "signed urls are not available", nil)
		return
	}
	if rejectScope(w, r) {
		return
	}
	var req archiveSignRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	vp, ok := cleanPath(w, r, firstNonEmpty(req.Path, req.Archive))
	if !ok {
		return
	}
	inner, ok := archive.NormalizeInner(firstNonEmpty(req.Inner, req.Entry))
	if !ok || inner == "" {
		WriteError(w, r, CodeValidationFailed, 0, "inner path is not valid", map[string]any{"fields": map[string]any{"inner": "invalid"}})
		return
	}
	u, _ := UserFrom(r.Context())
	if req.Session != "" {
		if _, ok := s.archives.Session(req.Session, u.ID, string(vp)); !ok {
			WriteError(w, r, CodeNotFound, 0, "archive session expired", map[string]any{"reason": "session_expired"})
			return
		}
	}
	exp := s.signer.Expiry(download.TTLFor(download.PurposeEntry, s.cfg.SignedURLTTL, s.cfg.MediaURLTTL))
	url := s.signer.URL(download.Params{Scope: download.UserScope(u.ID), Path: string(vp), Purpose: download.PurposeEntry, Extra: inner, Exp: exp})
	if req.Session != "" {
		url += "&sess=" + req.Session
	}
	WriteJSON(w, http.StatusOK, map[string]any{"url": url, "expiresAt": isoTime(exp), "purpose": string(download.PurposeEntry), "path": string(vp), "inner": inner})
}

// handleArchiveEntryContent: GET|HEAD /archives/entry-content (signed).
func (s *Server) handleArchiveEntryContent(w http.ResponseWriter, r *http.Request) {
	if s.signer == nil || s.archives == nil {
		WriteError(w, r, CodeDependencyMissing, 0, "archive preview is not available", nil)
		return
	}
	p, userID, ok := s.verifySigned(w, r, download.PurposeEntry)
	if !ok {
		return
	}
	u, ok := s.signedUser(w, r, userID)
	if !ok {
		return
	}
	password, encoding := "", "auto"
	if id := r.URL.Query().Get("sess"); id != "" {
		sess, ok := s.archives.Session(id, u.ID, p.Path)
		if !ok {
			WriteError(w, r, CodeNotFound, 0, "archive session expired", map[string]any{"reason": "session_expired"})
			return
		}
		password, encoding = sess.Password, sess.Encoding
	}
	vp, ok := cleanPath(w, r, p.Path)
	if !ok {
		return
	}
	release, ok := s.acquireDownloadSlot(w, r, u.ID)
	if !ok {
		return
	}
	defer release()
	unlock, err := s.locks.TryAcquire(u.ID, fsjail.Read, "archive-entry", vp)
	if err != nil {
		if !WriteFSError(w, r, err) {
			WriteError(w, r, CodeOperationInProgress, 0, "another operation holds this archive", nil)
		}
		return
	}
	defer unlock()
	j, releaseJail, err := s.jails.acquire(s.cfg.StorageRoot, u)
	if err != nil {
		if !WriteFSError(w, r, err) {
			WriteError(w, r, CodeInternal, 0, "storage unavailable", nil)
		}
		return
	}
	defer releaseJail()
	es, err := s.archives.OpenEntry(r.Context(), j, u.ID, string(vp), p.Extra, password, encoding)
	if err != nil {
		s.writeArchiveBrowserError(w, r, err)
		return
	}
	defer func() { _ = es.Body.Close() }()
	// peek for the content type and surface password/corruption errors as a
	// status code instead of a truncated body
	br := bufio.NewReaderSize(es.Body, 8192)
	head, perr := br.Peek(3072)
	if perr != nil && !errors.Is(perr, io.EOF) && !errors.Is(perr, bufio.ErrBufferFull) {
		s.writeArchiveBrowserError(w, r, perr)
		return
	}
	mt, _ := media.DetectMime(bytes.NewReader(head))
	name := archive.EntryBaseName(es.Entry.Name)
	d := download.Policy(mt, name, download.PurposeMedia)
	h := w.Header()
	download.SetContentHeaders(h, s.corp())
	h.Set("Content-Type", d.ContentType)
	h.Set("Content-Disposition", download.ContentDisposition(d.Inline, name))
	h.Set("Cache-Control", entryCacheControl)
	h.Set("Accept-Ranges", "none")
	if es.Entry.Size >= 0 {
		h.Set("Content-Length", strconv.FormatInt(es.Entry.Size, 10))
	}
	if r.Method == http.MethodHead {
		w.WriteHeader(http.StatusOK)
		return
	}
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, br); err != nil {
		s.logger.InfoContext(r.Context(), "archive entry stream aborted", slog.String("error", j.Redact(err.Error())))
	}
}

type extractEntriesRequest struct {
	Path         string   `json:"path"`
	Archive      string   `json:"archive"`
	Entries      []string `json:"entries"`
	DestPath     string   `json:"dest_path"`
	DestPathC    string   `json:"destPath"`
	Dest         string   `json:"dest"`
	Password     string   `json:"password"`
	NameEncoding string   `json:"name_encoding"`
	Encoding     string   `json:"encoding"`
	OnConflict   string   `json:"on_conflict"`
	OnConflictC  string   `json:"onConflict"`
	Policy       string   `json:"policy"`
}

// handleArchiveExtractEntries: POST /archives/extract-entries → extract
// task limited to the given members (folders include everything below).
func (s *Server) handleArchiveExtractEntries(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req extractEntriesRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	if len(req.Entries) == 0 {
		WriteError(w, r, CodeValidationFailed, 0, "entries are required", map[string]any{"fields": map[string]any{"entries": "required"}})
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	archivePath := firstNonEmpty(req.Path, req.Archive)
	encoding := firstNonEmpty(req.NameEncoding, req.Encoding)
	known, unknown, err := s.archives.MembersOf(r.Context(), j, u.ID, archivePath, req.Password, encoding, req.Entries)
	if err != nil {
		s.writeArchiveBrowserError(w, r, err)
		return
	}
	if len(unknown) > 0 {
		WriteError(w, r, CodeNotFound, 0, "entry not found in archive", map[string]any{"reason": "entry_not_found", "entries": unknown})
		return
	}
	policy := firstNonEmpty(req.OnConflict, req.OnConflictC, req.Policy, "fail")
	task, err := s.archives.Extract(r.Context(), j, archive.ExtractRequest{UserID: u.ID, Archive: archivePath, Dest: firstNonEmpty(req.DestPath, req.DestPathC, req.Dest),
		Password: req.Password, Encoding: encoding, Policy: policy, Entries: known})
	if err != nil {
		s.writeArchiveBrowserError(w, r, err)
		return
	}
	s.logger.InfoContext(r.Context(), "extract entries queued", slog.String("task", task.ID), slog.String("archive", archivePath), slog.Int("entries", len(known)), slog.Bool("password", req.Password != ""))
	WriteJSON(w, http.StatusAccepted, map[string]any{"task": task})
}

type zipAddRequest struct {
	Path     string   `json:"path"`
	Archive  string   `json:"archive"`
	Sources  []string `json:"sources"`
	Paths    []string `json:"paths"`
	InnerDir string   `json:"inner_dir"`
	InnerC   string   `json:"innerDir"`
	Into     string   `json:"into"`
}

func (s *Server) handleArchiveAdd(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req zipAddRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	sources := req.Sources
	if len(sources) == 0 {
		sources = req.Paths
	}
	task, err := s.archives.ZipAdd(r.Context(), j, archive.ZipAddRequest{UserID: u.ID, Archive: firstNonEmpty(req.Path, req.Archive), Sources: sources, Into: firstNonEmpty(req.InnerDir, req.InnerC, req.Into)})
	if err != nil {
		s.writeArchiveError(w, r, err)
		return
	}
	s.logger.InfoContext(r.Context(), "zip add queued", slog.String("task", task.ID), slog.Int("sources", len(sources)))
	WriteJSON(w, http.StatusAccepted, map[string]any{"task": task})
}

type zipRemoveRequest struct {
	Path    string   `json:"path"`
	Archive string   `json:"archive"`
	Entries []string `json:"entries"`
	Entry   string   `json:"entry"`
}

func (s *Server) handleArchiveRemove(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req zipRemoveRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	entries := req.Entries
	if len(entries) == 0 && req.Entry != "" {
		entries = []string{req.Entry}
	}
	task, err := s.archives.ZipRemove(r.Context(), j, archive.ZipRemoveRequest{UserID: u.ID, Archive: firstNonEmpty(req.Path, req.Archive), Entries: entries})
	if err != nil {
		s.writeArchiveError(w, r, err)
		return
	}
	s.logger.InfoContext(r.Context(), "zip remove queued", slog.String("task", task.ID), slog.Int("entries", len(entries)))
	WriteJSON(w, http.StatusAccepted, map[string]any{"task": task})
}
