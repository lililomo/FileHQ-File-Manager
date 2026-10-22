package httpapi

import (
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/download"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/media"
)

// SubtitlePath serves sidecar subtitles as sanitised WebVTT (SPEC §8.14
// VideoPlayer, Step 17b). Signature-only like /files/raw: the <track>
// element loads it without the API's JSON error handling.
const SubtitlePath = "/api/v1/files/subtitle"

// subtitleTrack is one entry of media-info `subtitles` for a video.
type subtitleTrack struct {
	Path   string `json:"path"`
	Name   string `json:"name"`
	Lang   string `json:"lang,omitempty"`
	Label  string `json:"label"`
	Format string `json:"format"`
	URL    string `json:"url"`
}

func (s *Server) mountSubtitle(api chi.Router) {
	api.Get("/files/subtitle", s.handleFilesSubtitle)
}

// subtitleURL signs path (a subtitle file) for the subtitle route: purpose
// media (MEDIA_URL_TTL) in the given scope ("u:<id>" or "r:<share>:<user>").
func (s *Server) subtitleURL(scope string, path fsjail.VPath) string {
	p := download.Params{
		Scope: scope, Path: string(path), Purpose: download.PurposeMedia,
		Exp: s.signer.Expiry(download.TTLFor(download.PurposeMedia, s.cfg.SignedURLTTL, s.cfg.MediaURLTTL)),
	}
	return SubtitlePath + "?" + s.signer.Query(p).Encode()
}

// subtitlesFor lists the sidecar subtitles of a video (same folder, see
// media.FindSidecars) with signed URLs. Errors leave the list empty.
func (s *Server) subtitlesFor(j *fsjail.Jail, scope string, video fsjail.Info) []subtitleTrack {
	out := []subtitleTrack{}
	if s.signer == nil {
		return out
	}
	dir := video.Path.Dir()
	entries, err := j.ReadDir(dir)
	if err != nil {
		return out
	}
	names := make([]string, 0, 8)
	for _, e := range entries {
		if !e.IsDir && media.IsSubtitleName(e.Name) {
			names = append(names, e.Name)
		}
	}
	for _, sc := range media.FindSidecars(video.Name, names) {
		p, err := fsjail.Join(dir, sc.Name)
		if err != nil {
			continue
		}
		label := sc.Label
		if label == "" {
			label = sc.Format
		}
		out = append(out, subtitleTrack{
			Path: string(p), Name: sc.Name, Lang: sc.Lang, Label: label, Format: sc.Format,
			URL: s.subtitleURL(scope, p),
		})
	}
	return out
}

// handleFilesSubtitle: GET /files/subtitle?path&purpose=media&scope&exp&sig.
// The path must be a .srt/.vtt file of the signing user (≤ 2 MiB); SubRip is
// converted, WebVTT re-emitted, both through the cue sanitiser. The text
// encoding is detected like the editor's (BOM, UTF-8, chardet).
func (s *Server) handleFilesSubtitle(w http.ResponseWriter, r *http.Request) {
	if s.signer == nil {
		WriteError(w, r, CodeDependencyMissing, 0, "signed urls are not available", nil)
		return
	}
	p, ss, ok := s.verifySigned(w, r, download.PurposeMedia)
	if !ok {
		return
	}
	if !media.IsSubtitleName(p.Path) {
		WriteError(w, r, CodeValidationFailed, 0, "not a subtitle file", map[string]any{"fields": map[string]any{"path": "not_a_subtitle"}})
		return
	}
	j, release, info, ok := s.signedFile(w, r, ss, p.Path, true)
	if !ok {
		return
	}
	defer release()
	s.writeSubtitle(w, r, j, info)
}

// writeSubtitle converts and streams one sidecar (shared with the public
// share route, Step 18a).
func (s *Server) writeSubtitle(w http.ResponseWriter, r *http.Request, j *fsjail.Jail, info fsjail.Info) {
	if info.Size > media.MaxSubtitleBytes {
		WriteError(w, r, CodeFileTooLarge, 0, "subtitle file too large", map[string]any{"limit": media.MaxSubtitleBytes})
		return
	}
	f, err := j.Open(info.Path)
	if err != nil {
		if !WriteFSError(w, r, err) {
			WriteError(w, r, CodeNotFound, 0, fsMessages[CodeNotFound], nil)
		}
		return
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, media.MaxSubtitleBytes+1))
	if err != nil || len(raw) > media.MaxSubtitleBytes {
		WriteError(w, r, CodeFileTooLarge, 0, "subtitle file too large", map[string]any{"limit": media.MaxSubtitleBytes})
		return
	}
	text, _, err := files.DecodeText(raw, files.DetectEncoding(raw).Encoding)
	if err != nil {
		WriteError(w, r, CodeUnsupportedType, 0, "subtitle encoding not supported", nil)
		return
	}
	var vtt string
	if strings.HasSuffix(strings.ToLower(info.Name), ".srt") {
		vtt = media.SRTToVTT(text)
	} else {
		vtt = media.SanitizeVTT(text)
	}
	h := w.Header()
	download.SetContentHeaders(h, s.corp())
	h.Set("Content-Type", "text/vtt; charset=utf-8")
	h.Set("Cache-Control", "private, max-age=300")
	h.Set("Content-Length", strconv.Itoa(len(vtt)))
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, vtt)
}
