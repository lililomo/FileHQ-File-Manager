package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/archive"
	"kppbwi.app/filemanager/internal/quota"
)

// mountArchives registers /archives (SPEC §6.3, §8.11): capabilities behind
// S+P, compress/extract as tasks behind S+P+CSRF. Passwords
// travel in the JSON body only (never query strings or logs) and live in
// the engine's memory for the task's lifetime.
func (s *Server) mountArchives(api chi.Router) {
	if s.archives == nil {
		return
	}
	api.Group(func(g chi.Router) {
		g.Use(s.RequireAuth)
		g.Use(RequirePasswordChanged)
		g.Get("/archives/capabilities", s.handleArchiveCapabilities)
	})
	api.Group(func(g chi.Router) {
		g.Use(OriginCheck(s.cfg.AppBaseURL))
		g.Use(s.RequireAuth)
		g.Use(s.RequireCSRF)
		g.Use(RequirePasswordChanged)
		g.Post("/archives/compress", s.handleArchiveCompress)
		g.Post("/archives/extract", s.handleArchiveExtract)
	})
}

func (s *Server) handleArchiveCapabilities(w http.ResponseWriter, r *http.Request) {
	WriteJSON(w, http.StatusOK, s.archives.Capabilities())
}

func (s *Server) writeArchiveError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		ae *archive.Error
		qe *quota.ExceededError
	)
	switch {
	case errors.As(err, &ae):
		WriteError(w, r, ErrorCode(ae.Code), ae.Status, ae.Message, ae.Details)
	case errors.As(err, &qe):
		WriteError(w, r, CodeQuotaExceeded, 0, quotaMessage(qe), map[string]any{"reason": qe.Reason, "used": qe.Used, "quota": qe.Limit, "needed": qe.Needed, "free": qe.Free})
	default:
		if WriteFSError(w, r, err) {
			return
		}
		s.writeTaskError(w, r, err)
	}
}

// compressRequest accepts the PROMPT (snake_case) and SPEC (camelCase)
// spellings.
type compressRequest struct {
	Paths         []string `json:"paths"`
	Format        string   `json:"format"`
	Name          string   `json:"name"`
	DestPath      string   `json:"dest_path"`
	DestPathC     string   `json:"destPath"`
	Dest          string   `json:"dest"`
	Level         string   `json:"level"`
	Password      string   `json:"password"`
	EncryptNames  *bool    `json:"encrypt_names"`
	EncryptNamesC *bool    `json:"encryptNames"`
	DeleteSource  *bool    `json:"delete_source"`
	DeleteSourceC *bool    `json:"deleteSource"`
	SplitMB       *int     `json:"split_volume_mb"`
	SplitMBC      *int     `json:"splitVolumeMb"`
}

func pick[T any](a, b *T) T {
	var zero T
	if a != nil {
		return *a
	}
	if b != nil {
		return *b
	}
	return zero
}

func (s *Server) handleArchiveCompress(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req compressRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	task, err := s.archives.Compress(r.Context(), j, archive.CompressRequest{UserID: u.ID, Paths: req.Paths, Format: req.Format, Name: req.Name,
		Dest: firstNonEmpty(req.DestPath, req.DestPathC, req.Dest), Level: req.Level, Password: req.Password,
		EncryptNames: pick(req.EncryptNames, req.EncryptNamesC), DeleteSource: pick(req.DeleteSource, req.DeleteSourceC), SplitMB: pick(req.SplitMB, req.SplitMBC)})
	if err != nil {
		s.writeArchiveError(w, r, err)
		return
	}
	s.logger.InfoContext(r.Context(), "compress queued", slog.String("task", task.ID), slog.String("format", req.Format), slog.Int("sources", len(req.Paths)), slog.Bool("password", req.Password != ""))
	WriteJSON(w, http.StatusAccepted, map[string]any{"task": task})
}

type extractRequest struct {
	Path         string   `json:"path"`
	Archive      string   `json:"archive"`
	DestPath     string   `json:"dest_path"`
	Dest         string   `json:"dest"`
	Password     string   `json:"password"`
	NameEncoding string   `json:"name_encoding"`
	Encoding     string   `json:"encoding"`
	OnConflict   string   `json:"on_conflict"`
	OnConflictC  string   `json:"onConflict"`
	Policy       string   `json:"policy"`
	Entries      []string `json:"entries"`
}

func (s *Server) handleArchiveExtract(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req extractRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	policy := firstNonEmpty(req.OnConflict, req.OnConflictC, req.Policy, "fail")
	task, err := s.archives.Extract(r.Context(), j, archive.ExtractRequest{UserID: u.ID, Archive: firstNonEmpty(req.Path, req.Archive), Dest: firstNonEmpty(req.DestPath, req.Dest),
		Password: req.Password, Encoding: firstNonEmpty(req.NameEncoding, req.Encoding), Policy: policy, Entries: req.Entries})
	if err != nil {
		s.writeArchiveError(w, r, err)
		return
	}
	s.logger.InfoContext(r.Context(), "extract queued", slog.String("task", task.ID), slog.String("archive", firstNonEmpty(req.Path, req.Archive)), slog.Bool("password", req.Password != ""))
	WriteJSON(w, http.StatusAccepted, map[string]any{"task": task})
}
