package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/download"
	"kppbwi.app/filemanager/internal/exec"
	"kppbwi.app/filemanager/internal/media"
	"kppbwi.app/filemanager/internal/tasks"
)

// On-the-fly HLS transcoding (SPEC §6.3 files, §8.14 "Transcoding HLS",
// Step 23d). POST /files/hls is a cookie + CSRF route of the owner; the
// playlist and the segments are served by signature only (purpose hls, one
// HMAC per task folder, TTL MEDIA_URL_TTL) like /files/raw.

// HLSPrefix is the signed playlist/segment route prefix (exempt from the
// coarse API limit and the API timeout, like /files/raw).
const HLSPrefix = "/api/v1/files/hls/"

const (
	hlsPlaylistType  = "application/vnd.apple.mpegurl"
	hlsSegmentType   = "video/mp2t"
	hlsCacheFinished = "private, max-age=3600"
	hlsCacheRunning  = "private, no-store"
	hlsRetryAfter    = "2"
)

// hlsEnqueueMu makes the "already running" check and the enqueue atomic
// within the process (two quick clicks never start two conversions).
var hlsEnqueueMu sync.Mutex

type hlsStartRequest struct {
	Path string `json:"path"`
}

// hlsStartResponse is the body of POST /files/hls (202 new task, 200 the
// conversion of this file that is running or finished already).
type hlsStartResponse struct {
	Task        tasks.Task `json:"task"`
	PlaylistURL string     `json:"playlistUrl"`
	ExpiresAt   string     `json:"expiresAt"`
	// Ready: at least two segments exist (or ffmpeg is done) — the player
	// can start. Clients also poll the playlist (≥ 2 #EXTINF or ENDLIST).
	Ready bool `json:"ready"`
}

func (s *Server) mountHLS(api chi.Router) {
	api.Get("/files/hls/{id}/"+media.PlaylistName, s.handleHLSPlaylist)
	api.Get("/files/hls/{id}/{segment}", s.handleHLSSegment)
	api.Group(func(g chi.Router) {
		g.Use(OriginCheck(s.cfg.AppBaseURL))
		g.Use(s.RequireAuth)
		g.Use(s.RequireCSRF)
		g.Use(RequirePasswordChanged)
		g.With(s.limitBy("files_hls", s.cfg.RateLimits.FilesSign, keySession)).Post("/files/hls", s.handleHLSStart)
	})
}

// hlsURL signs the playlist URL of task id for userID.
func (s *Server) hlsURL(userID, id string) (string, string) {
	exp := s.signer.Expiry(download.TTLFor(download.PurposeHLS, s.cfg.SignedURLTTL, s.cfg.MediaURLTTL))
	return HLSPrefix + id + "/" + media.PlaylistName + "?" + s.hlsQuery(userID, id, exp), isoTime(exp)
}

func (s *Server) hlsQuery(userID, id string, exp int64) string {
	scope := download.UserScope(userID)
	q := url.Values{}
	q.Set("scope", scope)
	q.Set("exp", strconv.FormatInt(exp, 10))
	q.Set("sig", s.signer.Sign(download.Params{Scope: scope, Path: id, Purpose: download.PurposeHLS, Exp: exp}))
	return q.Encode()
}

func (s *Server) hlsStarted(w http.ResponseWriter, status int, userID string, t tasks.Task) {
	u, exp := s.hlsURL(userID, t.ID)
	WriteJSON(w, status, hlsStartResponse{Task: t, PlaylistURL: u, ExpiresAt: exp, Ready: t.State == tasks.Done || s.transcode.Ready(t.ID)})
}

func (s *Server) handleHLSStart(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req hlsStartRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	if req.Path == "" {
		WriteError(w, r, CodeValidationFailed, 0, "path is required", map[string]any{"fields": map[string]any{"path": "required"}})
		return
	}
	p, ok := cleanPath(w, r, req.Path)
	if !ok {
		return
	}
	if s.transcode == nil || s.signer == nil || s.tasks == nil || !s.transcode.Available() {
		WriteError(w, r, CodeDependencyMissing, 0, "transcoding needs ffmpeg and ffprobe", map[string]any{"tool": "ffmpeg"})
		return
	}
	if !s.transcode.Settings().Enabled {
		s.writeTranscodeError(w, r, &media.TranscodeError{Reason: media.ReasonDisabled})
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	info, err := j.Stat(p)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	if !info.IsRegular() {
		WriteError(w, r, CodeValidationFailed, 0, "path is not a file", map[string]any{"reason": "not_a_file", "fields": map[string]any{"path": "not_a_file"}})
		return
	}
	hlsEnqueueMu.Lock()
	defer hlsEnqueueMu.Unlock()
	ctx := r.Context()
	// one queued/running conversion per user; the same file returns it
	active, err := s.store.ListUserActiveTasksOfType(ctx, u.ID, media.TaskTranscodeHLS)
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	busy := ""
	for _, row := range active {
		if row.CancelRequested == 1 {
			continue // finishing; the global cap still keeps a new one queued
		}
		busy = row.ID
		var pl media.TranscodePayload
		if json.Unmarshal([]byte(row.PayloadJson), &pl) == nil && pl.Path == string(p) && pl.Size == info.Size && pl.MtimeNs == info.ModTime.UnixNano() {
			if t, err := s.tasks.Get(ctx, u.ID, row.ID); err == nil && !t.State.Final() {
				s.hlsStarted(w, http.StatusOK, u.ID, t)
				return
			}
		}
	}
	if busy != "" {
		WriteError(w, r, CodeConflict, 0, "another conversion of this user is queued or running", map[string]any{"reason": "already_running", "taskId": busy})
		return
	}
	if id, ok := s.transcode.FindDone(u.ID, string(p), info.Size, info.ModTime.UnixNano()); ok {
		if t, err := s.tasks.Get(ctx, u.ID, id); err == nil && t.State == tasks.Done {
			s.hlsStarted(w, http.StatusOK, u.ID, t)
			return
		}
	}
	if err := s.transcode.CheckDisk(); err != nil {
		s.writeTranscodeError(w, r, err)
		return
	}
	payload, err := s.transcode.Check(ctx, j, info)
	if err != nil {
		s.writeTranscodeError(w, r, err)
		return
	}
	t, err := s.tasks.Enqueue(ctx, tasks.EnqueueRequest{UserID: u.ID, Type: media.TaskTranscodeHLS, Title: "Konversi " + p.Base(), Payload: payload})
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	s.hlsStarted(w, http.StatusAccepted, u.ID, t)
}

// writeTranscodeError maps refusals to SPEC §6.2 codes.
func (s *Server) writeTranscodeError(w http.ResponseWriter, r *http.Request, err error) {
	var te *media.TranscodeError
	var ee *exec.ExitError
	switch {
	case errors.As(err, &te):
		d := map[string]any{"reason": te.Reason}
		switch te.Reason {
		case media.ReasonDisabled:
			WriteError(w, r, CodeForbidden, 0, "transcoding is disabled", d)
		case media.ReasonNotVideo:
			WriteError(w, r, CodeUnsupportedType, 0, "not a video", d)
		case media.ReasonTooLong, media.ReasonUnknownDuration:
			d["maxMinutes"] = te.MaxMinutes
			if te.DurationMs > 0 {
				d["durationMs"] = te.DurationMs
			}
			WriteError(w, r, CodeValidationFailed, http.StatusUnprocessableEntity, "the video is too long to convert", d)
		case media.ReasonDiskBudget:
			WriteError(w, r, CodeConflict, 0, "the conversion cache is full, try again later", d)
		case media.ReasonDiskFull:
			WriteError(w, r, CodeQuotaExceeded, 0, "not enough free disk space", d)
		default:
			WriteError(w, r, CodeValidationFailed, 0, "cannot convert this file", d)
		}
	case errors.Is(err, exec.ErrMissing):
		WriteError(w, r, CodeDependencyMissing, 0, "transcoding needs ffmpeg and ffprobe", map[string]any{"tool": "ffprobe"})
	case errors.As(err, &ee), errors.Is(err, exec.ErrTimeout):
		// ffprobe could not read it: not a video we can convert
		WriteError(w, r, CodeUnsupportedType, 0, "not a video", map[string]any{"reason": media.ReasonNotVideo})
	default:
		s.writeOpsError(w, r, err)
	}
}

// hlsVerify checks the folder signature of a playlist/segment request and
// resolves the owner (inactive accounts → 403, like every signed route).
func (s *Server) hlsVerify(w http.ResponseWriter, r *http.Request) (userID, id, query string, ok bool) {
	if s.signer == nil {
		WriteError(w, r, CodeDependencyMissing, 0, "signed urls are not available", nil)
		return "", "", "", false
	}
	id = chi.URLParam(r, "id")
	q := r.URL.Query()
	scope, sig := q.Get("scope"), q.Get("sig")
	exp, err := strconv.ParseInt(q.Get("exp"), 10, 64)
	if err != nil || scope == "" || sig == "" {
		WriteError(w, r, CodeSignatureInvalid, 0, "invalid signed url", nil)
		return "", "", "", false
	}
	if err := s.signer.Verify(download.Params{Scope: scope, Path: id, Purpose: download.PurposeHLS, Exp: exp}, sig); err != nil {
		if errors.Is(err, download.ErrSignatureExpired) {
			WriteError(w, r, CodeSignatureExpired, 0, "signed url expired", nil)
		} else {
			WriteError(w, r, CodeSignatureInvalid, 0, "invalid signed url", nil)
		}
		return "", "", "", false
	}
	uid, okScope := download.UserFromScope(scope)
	if !okScope {
		WriteError(w, r, CodeSignatureInvalid, 0, "unsupported scope", nil)
		return "", "", "", false
	}
	u, okUser := s.signedUserCode(w, r, uid, CodeForbidden)
	if !okUser {
		return "", "", "", false
	}
	if s.transcode == nil {
		WriteError(w, r, CodeNotFound, 0, "not found", nil)
		return "", "", "", false
	}
	if !s.transcode.Settings().Enabled {
		WriteError(w, r, CodeForbidden, 0, "transcoding is disabled", map[string]any{"reason": media.ReasonDisabled})
		return "", "", "", false
	}
	return u.ID, id, s.hlsQuery(u.ID, id, exp), true
}

func (s *Server) handleHLSPlaylist(w http.ResponseWriter, r *http.Request) {
	userID, id, query, ok := s.hlsVerify(w, r)
	if !ok {
		return
	}
	f, _, finished, err := s.transcode.Open(userID, id, media.PlaylistName)
	if errors.Is(err, media.ErrHLSNotFound) && media.ValidHLSID(id) {
		// a queued (or just starting) conversion has no folder yet
		if t, gerr := s.tasks.Get(r.Context(), userID, id); gerr == nil && t.Type == media.TaskTranscodeHLS && !t.State.Final() {
			err = media.ErrHLSNotReady
		}
	}
	switch {
	case errors.Is(err, media.ErrHLSNotReady):
		w.Header().Set("Retry-After", hlsRetryAfter)
		w.Header().Set("Cache-Control", hlsCacheRunning)
		w.WriteHeader(http.StatusAccepted)
		return
	case errors.Is(err, media.ErrHLSNotFound):
		WriteError(w, r, CodeNotFound, 0, "conversion not found", nil)
		return
	case err != nil:
		WriteError(w, r, CodeInternal, 0, "cannot read the playlist", nil)
		return
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, media.MaxPlaylistBytes+1))
	if err != nil || len(data) > media.MaxPlaylistBytes {
		WriteError(w, r, CodeInternal, 0, "cannot read the playlist", nil)
		return
	}
	out, err := media.RewritePlaylist(data, query)
	if err != nil {
		s.logger.Warn("hls playlist rejected", slog.String("task", id), slog.String("error", err.Error()))
		WriteError(w, r, CodeInternal, 0, "cannot read the playlist", nil)
		return
	}
	h := w.Header()
	download.SetContentHeaders(h, s.corp())
	h.Set("Content-Type", hlsPlaylistType)
	h.Set("Content-Length", strconv.Itoa(len(out)))
	if finished {
		h.Set("Cache-Control", hlsCacheFinished)
	} else {
		h.Set("Cache-Control", hlsCacheRunning)
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out)
}

func (s *Server) handleHLSSegment(w http.ResponseWriter, r *http.Request) {
	userID, id, _, ok := s.hlsVerify(w, r)
	if !ok {
		return
	}
	name := chi.URLParam(r, "segment")
	if !media.ValidHLSSegment(name) {
		WriteError(w, r, CodeNotFound, 0, "segment not found", nil)
		return
	}
	f, info, _, err := s.transcode.Open(userID, id, name)
	if err != nil {
		if errors.Is(err, media.ErrHLSNotFound) {
			WriteError(w, r, CodeNotFound, 0, "segment not found", nil)
		} else {
			WriteError(w, r, CodeInternal, 0, "cannot read the segment", nil)
		}
		return
	}
	defer func() { _ = f.Close() }()
	h := w.Header()
	download.SetContentHeaders(h, s.corp())
	h.Set("Content-Type", hlsSegmentType)
	h.Set("Cache-Control", hlsCacheFinished) // a written segment never changes
	h.Set("Accept-Ranges", "bytes")
	h.Set("ETag", fmt.Sprintf(`"%x-%x"`, info.Size(), info.ModTime().UnixNano()))
	http.ServeContent(w, r, "", info.ModTime(), f)
}
