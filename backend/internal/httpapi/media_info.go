package httpapi

import (
	"log/slog"
	"net/http"

	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/listing"
	"kppbwi.app/filemanager/internal/media"
)

// mediaInfoResponse is GET /files/media-info (SPEC §6.3, §8.14 "panel
// info"): the media_metadata row of one file plus the EXIF subset. GPS is
// stored but only sent with include_gps=1 (SPEC §10.1 information
// disclosure); exif.hasGps tells the client whether asking is worthwhile.
type mediaInfoResponse struct {
	Path       string            `json:"path"`
	Name       string            `json:"name"`
	Size       int64             `json:"size"`
	Mtime      string            `json:"mtime"`
	Category   string            `json:"category"`
	Mime       string            `json:"mime"`
	Kind       media.Kind        `json:"kind"`
	Width      int               `json:"width,omitempty"`
	Height     int               `json:"height,omitempty"`
	DurationMs int64             `json:"durationMs,omitempty"`
	CodecVideo string            `json:"codecVideo,omitempty"`
	CodecAudio string            `json:"codecAudio,omitempty"`
	Bitrate    int64             `json:"bitrate,omitempty"`
	HasCover   bool              `json:"hasCover"`
	Tags       map[string]string `json:"tags,omitempty"`
	ThumbState string            `json:"thumbState,omitempty"`
	Exif       *mediaInfoExif    `json:"exif"`
	GPS        *media.GPS        `json:"gps,omitempty"`
	// Subtitles are the sidecar .vtt/.srt files of a video (Step 17b), each
	// with a signed /files/subtitle URL; omitted when there are none.
	Subtitles []subtitleTrack `json:"subtitles,omitempty"`
}

type mediaInfoExif struct {
	*media.Exif
	HasGPS bool `json:"hasGps"`
}

// handleFilesMediaInfo: GET /files/media-info?path&include_gps=1.
func (s *Server) handleFilesMediaInfo(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	q := r.URL.Query()
	p, ok := cleanPath(w, r, q.Get("path"))
	if !ok {
		return
	}
	includeGPS, ok := parseBoolDefault(q.Get("include_gps"), false)
	if !ok {
		WriteError(w, r, CodeValidationFailed, 0, "include_gps must be a boolean", map[string]any{"fields": map[string]any{"include_gps": "invalid"}})
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	// symlinks to files inside the jail are followed, like /files/raw
	info, err := j.Stat(p)
	if err != nil {
		s.writeListingError(w, r, err, string(p))
		return
	}
	if !info.IsRegular() {
		WriteError(w, r, CodeValidationFailed, 0, "the path is not a regular file", map[string]any{"reason": "not_a_file", "path": string(p)})
		return
	}
	ti := files.Lookup(info.Name)
	out := mediaInfoResponse{
		Path: string(info.Path), Name: info.Name, Size: info.Size, Mtime: listing.ISO(info.ModTime),
		Category: string(ti.Category), Mime: ti.Mime, Kind: media.KindOther,
	}
	if s.media == nil || !media.Probeable(string(ti.Category)) {
		WriteJSON(w, http.StatusOK, out)
		return
	}
	row, _, err := s.media.Ensure(r.Context(), j, u.ID, info)
	if err != nil {
		s.logger.DebugContext(r.Context(), "media-info probe failed", slog.String("path", string(p)), slog.String("error", j.Redact(err.Error())))
		WriteJSON(w, http.StatusOK, out)
		return
	}
	out.Mime = row.Mime
	out.Kind = media.KindOfRow(row)
	out.ThumbState = row.ThumbState
	out.HasCover = row.HasCover == 1
	if row.Width != nil && row.Height != nil {
		out.Width, out.Height = int(*row.Width), int(*row.Height)
	}
	if row.DurationMs != nil {
		out.DurationMs = *row.DurationMs
	}
	if row.CodecVideo != nil {
		out.CodecVideo = *row.CodecVideo
	}
	if row.CodecAudio != nil {
		out.CodecAudio = *row.CodecAudio
	}
	if row.Bitrate != nil {
		out.Bitrate = *row.Bitrate
	}
	if t := media.TagsOf(row); t != nil {
		it := &listing.Item{}
		media.Apply(it, row)
		if it.Media != nil {
			out.Tags = it.Media.Tags
		}
	}
	if out.Kind == media.KindVideo {
		out.Subtitles = s.subtitlesFor(j, u.ID, info)
	}
	if out.Kind == media.KindImage {
		if ex := s.media.ExifFor(r.Context(), j, u.ID, info, row); ex != nil {
			out.Exif = &mediaInfoExif{Exif: ex.Public(), HasGPS: ex.HasGPS()}
			if includeGPS && ex.HasGPS() {
				out.GPS = ex.GPS
			}
		}
	}
	WriteJSON(w, http.StatusOK, out)
}
