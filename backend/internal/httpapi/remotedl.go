package httpapi

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/netguard"
	"kppbwi.app/filemanager/internal/remotedl"
)

// mountRemoteDL registers /remote-downloads (SPEC §6.3, §8.10): S+P, CSRF,
// one task per URL, probe. The menu is hidden client-side when the feature
// is off; the endpoints answer 403 FORBIDDEN then.
func (s *Server) mountRemoteDL(api chi.Router) {
	if s.remote == nil {
		return
	}
	api.Group(func(g chi.Router) {
		g.Use(OriginCheck(s.cfg.AppBaseURL))
		g.Use(s.RequireAuth)
		g.Use(s.RequireCSRF)
		g.Use(RequirePasswordChanged)
		g.Post("/remote-downloads", s.handleRemoteCreate)
		g.Post("/remote-downloads/probe", s.handleRemoteProbe)
	})
}

type remoteCreateRequest struct {
	URLs      []string          `json:"urls"`
	Path      string            `json:"path"`
	Dest      string            `json:"dest"` // SPEC alias
	Filename  string            `json:"filename"`
	Filenames map[string]string `json:"filenames"`
	// onConflict is the Step 9a spelling; policy / on_conflict are accepted.
	OnConflict string `json:"onConflict"`
	Policy     string `json:"policy"`
	Snake      string `json:"on_conflict"`
}

func (s *Server) writeRemoteError(w http.ResponseWriter, r *http.Request, err error) {
	var re *remotedl.Error
	switch {
	case errors.As(err, &re):
		if re.Status == http.StatusTooManyRequests {
			if ra, ok := re.Details["retryAfter"].(int); ok && ra > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(ra))
			}
		}
		WriteError(w, r, ErrorCode(re.Code), re.Status, re.Message, re.Details)
	default:
		if be, ok := netguard.Unwrap(err); ok {
			WriteError(w, r, CodeSSRFBlocked, http.StatusUnprocessableEntity, "destination address is not allowed", map[string]any{"reason": be.Reason, "host": be.Host})
			return
		}
		s.writeTaskError(w, r, err)
	}
}

func (s *Server) handleRemoteCreate(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req remoteCreateRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	dest := req.Path
	if dest == "" {
		dest = req.Dest
	}
	policy := req.OnConflict
	if policy == "" {
		policy = req.Policy
	}
	if policy == "" {
		policy = req.Snake
	}
	if policy == "" {
		policy = "fail"
	}
	vp, err := fsjail.Clean(dest)
	if err != nil {
		WriteError(w, r, CodePathInvalid, 0, "destination path is not valid", map[string]any{"fields": map[string]any{"path": "invalid"}})
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	info, err := j.Lstat(vp)
	if err != nil {
		if !WriteFSError(w, r, err) {
			WriteError(w, r, CodeNotFound, 0, "destination folder not found", map[string]any{"path": string(vp)})
		}
		return
	}
	if !info.IsDir {
		WriteError(w, r, CodeValidationFailed, 0, "destination is not a folder", map[string]any{"reason": "not_a_folder", "path": string(vp)})
		return
	}
	created, err := s.remote.Create(r.Context(), remotedl.CreateRequest{UserID: u.ID, URLs: req.URLs, Dest: string(vp), Filename: req.Filename, Filenames: req.Filenames, Policy: policy})
	if err != nil {
		s.writeRemoteError(w, r, err)
		return
	}
	hosts := make([]string, 0, len(req.URLs))
	for _, raw := range req.URLs {
		if pu, perr := url.Parse(raw); perr == nil {
			hosts = append(hosts, netguard.SafeHost(pu))
		}
	}
	// SPEC §8.10 step 10 / PROMPT Step 13: host only, never the full URL.
	s.audit.Record(r.Context(), audit.Entry{ActorID: u.ID, ActorUsername: u.Username, Action: audit.ActionRemoteDownload, TargetType: "folder", TargetID: string(vp),
		IP: ClientIP(r), UserAgent: r.UserAgent(), Details: map[string]any{"hosts": hosts, "count": len(created), "policy": policy}})
	s.logger.InfoContext(r.Context(), "remote downloads created", slog.Int("count", len(created)), slog.Any("hosts", hosts))
	WriteJSON(w, http.StatusAccepted, map[string]any{"tasks": created})
}

func (s *Server) handleRemoteProbe(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req struct {
		URL string `json:"url"`
	}
	if !ReadJSON(w, r, &req) {
		return
	}
	res, err := s.remote.Probe(r.Context(), req.URL)
	if err != nil {
		s.writeRemoteError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, res)
}
