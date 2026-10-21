// Package media detects a file's real MIME type and probes dimensions,
// duration, codecs and audio tags (SPEC §8.4, ADR-10). Results are cached in
// media_metadata by Service; the thumbnail pipeline (internal/thumbs) and the
// listing columns Dimensi/Durasi read from that cache.
package media

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	// Image decoders for image.DecodeConfig (header only, no pixel decode).
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	"github.com/gabriel-vasile/mimetype"
	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/tiff"
	_ "golang.org/x/image/webp"

	"kppbwi.app/filemanager/internal/exec"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
)

// Kind is the coarse media class that selects the probe and the thumbnail
// generator.
type Kind string

// Kinds.
const (
	KindImage Kind = "image"
	KindSVG   Kind = "svg"
	KindVideo Kind = "video"
	KindAudio Kind = "audio"
	KindPDF   Kind = "pdf"
	KindOther Kind = "other"
)

// SniffBytes is how much of a file is read for MIME detection (SPEC §8.9).
const SniffBytes = 3072

func init() {
	mimetype.SetLimit(SniffBytes)
}

// Tags are the audio tags kept in media_metadata.tags_json.
type Tags struct {
	Title  string `json:"title,omitempty"`
	Artist string `json:"artist,omitempty"`
	Album  string `json:"album,omitempty"`
	Year   string `json:"year,omitempty"`
	Track  string `json:"track,omitempty"`
}

// Meta is the probe result.
type Meta struct {
	Mime       string
	Kind       Kind
	Width      int
	Height     int
	DurationMs int64
	CodecVideo string
	CodecAudio string
	Bitrate    int64
	HasCover   bool
	Tags       *Tags
	// Exif is filled for raster images that carry an EXIF block (Step 17a);
	// GPS stays inside and is stripped by the API unless asked for.
	Exif *Exif
}

// Prober runs the detectors. FFprobe/Vipsheader may be "" (feature degrades).
type Prober struct {
	Runner     *exec.Runner
	FFprobe    string
	Vipsheader string
}

// DetectMime sniffs the MIME type from the first SniffBytes of r (without
// parameters such as "; charset=").
func DetectMime(r io.Reader) (string, error) {
	mt, err := mimetype.DetectReader(r)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	m := mt.String()
	if i := strings.IndexByte(m, ';'); i >= 0 {
		m = strings.TrimSpace(m[:i])
	}
	return m, nil
}

// KindOf derives the Kind from the sniffed MIME type, falling back to the
// extension when the content is not recognised.
func KindOf(mime, name string) Kind {
	switch {
	case mime == "image/svg+xml":
		return KindSVG
	case strings.HasPrefix(mime, "image/"):
		return KindImage
	case strings.HasPrefix(mime, "video/"):
		return KindVideo
	case strings.HasPrefix(mime, "audio/"):
		return KindAudio
	case mime == "application/pdf":
		return KindPDF
	}
	ti := files.Lookup(name)
	switch ti.Category {
	case files.CatImage:
		if ti.Ext == "svg" {
			return KindSVG
		}
		return KindImage
	case files.CatVideo:
		return KindVideo
	case files.CatAudio:
		return KindAudio
	case files.CatPDF:
		return KindPDF
	}
	return KindOther
}

// Probe inspects path (a regular file) through the jail.
func (p *Prober) Probe(ctx context.Context, j *fsjail.Jail, path fsjail.VPath, name string) (Meta, error) {
	f, err := j.Open(path)
	if err != nil {
		return Meta{}, err
	}
	defer func() { _ = f.Close() }()
	mime, err := DetectMime(io.LimitReader(f, SniffBytes))
	if err != nil {
		return Meta{}, fmt.Errorf("media: sniff: %w", err)
	}
	m := Meta{Mime: mime, Kind: KindOf(mime, name)}
	switch m.Kind {
	case KindImage:
		if _, err := f.Seek(0, io.SeekStart); err == nil {
			if cfg, _, err := image.DecodeConfig(f); err == nil {
				m.Width, m.Height = cfg.Width, cfg.Height
			}
		}
		if m.Width == 0 && p != nil && p.Runner != nil && p.Vipsheader != "" {
			if w, h, err := p.vipsDimensions(ctx, j, path); err == nil {
				m.Width, m.Height = w, h
			}
		}
		// EXIF is read from the container directly (bounded reads); a damaged
		// block is ignored, the rest of the probe still succeeds.
		if ex, err := ReadExif(f); err == nil && !ex.Empty() {
			m.Exif = ex
		}
	case KindVideo, KindAudio:
		if p != nil && p.Runner != nil && p.FFprobe != "" {
			if err := p.ffprobe(ctx, j, path, &m); err != nil {
				return m, err
			}
		}
	}
	return m, nil
}

var dimRe = regexp.MustCompile(`:\s*(\d+)x(\d+)\s`)

// vipsDimensions parses "file: WxH uchar, …" printed by vipsheader (for
// formats Go cannot decode: avif, heic, jpeg2000).
func (p *Prober) vipsDimensions(ctx context.Context, j *fsjail.Jail, path fsjail.VPath) (int, int, error) {
	abs, err := j.AbsForCLI(path)
	if err != nil {
		return 0, 0, err
	}
	res, err := p.Runner.Run(ctx, exec.Spec{Bin: p.Vipsheader, Args: []string{abs}, Timeout: 15 * time.Second, NoNice: true})
	if err != nil {
		return 0, 0, err
	}
	m := dimRe.FindSubmatch(res.Stdout)
	if m == nil {
		return 0, 0, errors.New("media: vipsheader output not understood")
	}
	w, _ := strconv.Atoi(string(m[1]))
	h, _ := strconv.Atoi(string(m[2]))
	return w, h, nil
}

func (p *Prober) ffprobe(ctx context.Context, j *fsjail.Jail, path fsjail.VPath, m *Meta) error {
	abs, err := j.AbsForCLI(path)
	if err != nil {
		return err
	}
	res, err := p.Runner.Run(ctx, exec.Spec{
		Bin:       p.FFprobe,
		Args:      []string{"-v", "error", "-print_format", "json", "-show_format", "-show_streams", abs},
		Timeout:   30 * time.Second,
		MaxOutput: 1 << 20,
	})
	if err != nil {
		return err
	}
	return ParseFFprobe(res.Stdout, m)
}

type ffprobeStream struct {
	CodecType   string            `json:"codec_type"`
	CodecName   string            `json:"codec_name"`
	Width       int               `json:"width"`
	Height      int               `json:"height"`
	Duration    string            `json:"duration"`
	BitRate     string            `json:"bit_rate"`
	Disposition map[string]int    `json:"disposition"`
	Tags        map[string]string `json:"tags"`
}

type ffprobeOutput struct {
	Streams []ffprobeStream `json:"streams"`
	Format  struct {
		Duration string            `json:"duration"`
		BitRate  string            `json:"bit_rate"`
		Tags     map[string]string `json:"tags"`
	} `json:"format"`
}

// ParseFFprobe fills m from `ffprobe -print_format json -show_format
// -show_streams` output. Attached pictures (cover art) are reported as
// HasCover and never as the video dimensions.
func ParseFFprobe(data []byte, m *Meta) error {
	var out ffprobeOutput
	if err := json.Unmarshal(data, &out); err != nil {
		return fmt.Errorf("media: ffprobe json: %w", err)
	}
	if ms := seconds(out.Format.Duration); ms > 0 {
		m.DurationMs = ms
	}
	if br, err := strconv.ParseInt(out.Format.BitRate, 10, 64); err == nil && br > 0 {
		m.Bitrate = br
	}
	tags := map[string]string{}
	for _, st := range out.Streams {
		switch st.CodecType {
		case "video":
			if st.Disposition["attached_pic"] == 1 || isStillImageCodec(st.CodecName) && m.Kind == KindAudio {
				m.HasCover = true
				continue
			}
			if m.CodecVideo == "" {
				m.CodecVideo = st.CodecName
				m.Width, m.Height = st.Width, st.Height
			}
			if m.DurationMs == 0 {
				m.DurationMs = seconds(st.Duration)
			}
		case "audio":
			if m.CodecAudio == "" {
				m.CodecAudio = st.CodecName
			}
			if m.DurationMs == 0 {
				m.DurationMs = seconds(st.Duration)
			}
		}
		for k, v := range st.Tags {
			tags[strings.ToLower(k)] = v
		}
	}
	for k, v := range out.Format.Tags { // container tags win
		tags[strings.ToLower(k)] = v
	}
	if m.Kind == KindAudio && m.CodecVideo != "" && m.Width > 0 {
		// A video stream in an audio file that was not flagged attached_pic
		// (some encoders): treat it as cover art.
		m.HasCover = true
		m.CodecVideo, m.Width, m.Height = "", 0, 0
	}
	t := &Tags{
		Title:  tags["title"],
		Artist: firstNonEmpty(tags["artist"], tags["album_artist"], tags["performer"]),
		Album:  tags["album"],
		Year:   yearOf(firstNonEmpty(tags["date"], tags["year"], tags["tdrc"])),
		Track:  firstNonEmpty(tags["track"], tags["tracknumber"]),
	}
	if *t != (Tags{}) {
		m.Tags = t
	}
	return nil
}

func isStillImageCodec(codec string) bool {
	switch codec {
	case "mjpeg", "png", "bmp", "gif", "webp", "tiff":
		return true
	}
	return false
}

func seconds(s string) int64 {
	if s == "" {
		return 0
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || f <= 0 || math.IsInf(f, 0) || math.IsNaN(f) {
		return 0
	}
	return int64(math.Round(f * 1000))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func yearOf(s string) string {
	if len(s) >= 4 {
		if _, err := strconv.Atoi(s[:4]); err == nil {
			return s[:4]
		}
	}
	return s
}

// VipsheaderPath derives the vipsheader binary next to vipsthumbnail
// (same package libvips-tools).
func VipsheaderPath(vipsthumbnail string) string {
	if vipsthumbnail == "" {
		return ""
	}
	p := filepath.Join(filepath.Dir(vipsthumbnail), "vipsheader")
	if exec.Executable(p) {
		return p
	}
	return ""
}
