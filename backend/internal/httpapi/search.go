package httpapi

import (
	"bufio"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/listing"
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/search"
	"kppbwi.app/filemanager/internal/versions"
)

// ContentSearchPath streams NDJSON and is exempt from the API timeout.
const ContentSearchPath = "/api/v1/search/content"

// mountSearch registers /search (SPEC §6.3 "Search", §8.12): S+P, CSRF on
// POST/DELETE, rate limits inside the service.
func (s *Server) mountSearch(api chi.Router) {
	if s.search == nil {
		return
	}
	api.Group(func(g chi.Router) {
		g.Use(OriginCheck(s.cfg.AppBaseURL))
		g.Use(s.RequireAuth)
		g.Use(s.RequireCSRF)
		g.Use(RequirePasswordChanged)
		g.Get("/search/names", s.handleSearchNames)
		g.Post("/search/content", s.handleSearchContent)
		g.Post("/search/replace/preview", s.handleReplacePreview)
		g.Post("/search/replace", s.handleReplaceApply)
		g.Get("/search/history", s.handleSearchHistory)
		g.Delete("/search/history", s.handleSearchHistoryClear)
	})
}

// mountVersions registers the minimal /versions group of Step 15 (list,
// restore, delete); content/download arrive with the editor (16b).
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
		g.Post("/versions/{id}/restore", s.handleVersionRestore)
		g.Delete("/versions/{id}", s.handleVersionDelete)
	})
}

func (s *Server) writeSearchError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		se *search.Error
		ve *versions.Error
		qe *quota.ExceededError
	)
	switch {
	case errors.As(err, &se):
		if se.Status == http.StatusTooManyRequests {
			if ra, ok := se.Details["retryAfter"].(int); ok && ra > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(ra))
			}
		}
		WriteError(w, r, ErrorCode(se.Code), se.Status, se.Message, se.Details)
	case errors.As(err, &ve):
		WriteError(w, r, ErrorCode(ve.Code), ve.Status, ve.Message, ve.Details)
	case errors.As(err, &qe):
		WriteError(w, r, CodeQuotaExceeded, 0, quotaMessage(qe), map[string]any{"reason": qe.Reason, "used": qe.Used, "quota": qe.Limit, "needed": qe.Needed, "free": qe.Free})
	default:
		if WriteFSError(w, r, err) {
			return
		}
		s.writeTaskError(w, r, err)
	}
}

func boolParam(q string, def bool) bool {
	switch strings.ToLower(q) {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func csvParam(raw string) []string {
	if raw == "" {
		return nil
	}
	var out []string
	for _, p := range strings.Split(raw, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func (s *Server) handleSearchNames(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	q := r.URL.Query()
	fields := map[string]any{}
	nq := search.NameQuery{Path: q.Get("path"), Q: q.Get("q"), Recursive: boolParam(q.Get("recursive"), true),
		Glob: boolParam(q.Get("glob"), false), Regex: boolParam(q.Get("regex"), false), Ext: csvParam(q.Get("ext")), Tag: q.Get("tag"), Cursor: q.Get("cursor")}
	for _, t := range csvParam(q.Get("types")) {
		c, ok := files.ParseCategory(t)
		if !ok {
			fields["types"] = "unknown category " + t
			break
		}
		nq.Types = append(nq.Types, c)
	}
	var err error
	if nq.MinSize, err = search.ParseSize(q.Get("minSize")); err != nil {
		fields["minSize"] = "invalid"
	}
	if nq.MaxSize, err = search.ParseSize(q.Get("maxSize")); err != nil {
		fields["maxSize"] = "invalid"
	}
	if nq.From, err = search.ParseTime(q.Get("from")); err != nil {
		fields["from"] = "invalid"
	}
	if nq.To, err = search.ParseTime(q.Get("to")); err != nil {
		fields["to"] = "invalid"
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > search.NamePageMax {
			fields["limit"] = "1..1000"
		}
		nq.Limit = n
	}
	if len(fields) > 0 {
		WriteError(w, r, CodeValidationFailed, 0, "invalid search parameters", map[string]any{"fields": fields})
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	res, err := s.search.SearchNames(r.Context(), j, u.ID, nq)
	if err != nil {
		s.writeSearchError(w, r, err)
		return
	}
	items := make([]*listing.Item, 0, len(res.Items))
	for i := range res.Items {
		items = append(items, res.Items[i].Item)
	}
	s.annotateItems(r, u.ID, items)
	WriteJSON(w, http.StatusOK, res)
}

// handleSearchContent streams NDJSON: one object per match, then the
// summary line {done:true,…}. Errors before the first byte use the normal
// envelope; a failure mid-stream ends the stream with {done:true, error}.
func (s *Server) handleSearchContent(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req search.ContentQuery
	if !ReadJSON(w, r, &req) {
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	rc := http.NewResponseController(w)
	bw := bufio.NewWriterSize(w, 32<<10)
	started := false
	enc := json.NewEncoder(bw)
	emit := func(m search.Match) error {
		if !started {
			h := w.Header()
			h.Set("Content-Type", "application/x-ndjson; charset=utf-8")
			h.Set("Cache-Control", "no-store")
			h.Set("X-Content-Type-Options", "nosniff")
			w.WriteHeader(http.StatusOK)
			started = true
		}
		if err := enc.Encode(m); err != nil {
			return err
		}
		if bw.Buffered() > 16<<10 {
			if err := bw.Flush(); err != nil {
				return err
			}
			_ = rc.Flush()
		}
		return nil
	}
	sum, err := s.search.SearchContent(r.Context(), j, u.ID, req, emit)
	if err != nil {
		if !started {
			s.writeSearchError(w, r, err)
			return
		}
		s.logger.WarnContext(r.Context(), "content search ended early", slog.String("error", err.Error()))
		_ = enc.Encode(map[string]any{"done": true, "error": "stream_interrupted", "files": sum.Files, "matches": sum.Matches, "truncated": true, "engine": sum.Engine})
		_ = bw.Flush()
		return
	}
	if !started {
		h := w.Header()
		h.Set("Content-Type", "application/x-ndjson; charset=utf-8")
		h.Set("Cache-Control", "no-store")
		h.Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(http.StatusOK)
	}
	_ = enc.Encode(sum)
	_ = bw.Flush()
	_ = rc.Flush()
}

func (s *Server) handleReplacePreview(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req search.PreviewPayload
	if !ReadJSON(w, r, &req) {
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	t, err := s.search.Preview(r.Context(), j, u.ID, req)
	if err != nil {
		s.writeSearchError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusAccepted, map[string]any{"task": t})
}

func (s *Server) handleReplaceApply(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req search.ApplyPayload
	if !ReadJSON(w, r, &req) {
		return
	}
	u, _ := UserFrom(r.Context())
	t, err := s.search.Apply(r.Context(), u.ID, req)
	if err != nil {
		s.writeSearchError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusAccepted, map[string]any{"task": t})
}

func (s *Server) handleSearchHistory(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	kind := r.URL.Query().Get("kind")
	if kind != "" && kind != "name" && kind != "content" {
		WriteError(w, r, CodeValidationFailed, 0, "kind must be name or content", map[string]any{"fields": map[string]any{"kind": "name|content"}})
		return
	}
	items, err := s.search.History(r.Context(), u.ID, kind)
	if err != nil {
		s.writeSearchError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "limit": search.MaxHistory})
}

func (s *Server) handleSearchHistoryClear(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	if err := s.search.ClearHistory(r.Context(), u.ID); err != nil {
		s.writeSearchError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- versions (minimal, SPEC §6.3 "Versions") ---

func (s *Server) handleVersionsList(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	vp, err := fsjail.Clean(r.URL.Query().Get("path"))
	if err != nil || vp.IsRoot() {
		WriteError(w, r, CodePathInvalid, 0, "path is not valid", map[string]any{"fields": map[string]any{"path": "invalid"}})
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
