package thumbs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/exec"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/media"
)

// Status of a thumbnail request (SPEC §6.3 GET /files/thumb).
type Status string

// Statuses.
const (
	StatusReady       Status = "ready"       // 200
	StatusPending     Status = "pending"     // 202 Retry-After
	StatusFailed      Status = "failed"      // 404 (until the file changes)
	StatusUnsupported Status = "unsupported" // 404
)

// Config of the pipeline (from config.Config).
type Config struct {
	Workers     int
	Vips        string // vipsthumbnail
	FFmpeg      string
	FFprobe     string
	Pdftoppm    string
	StorageRoot string
	DataDir     string
	// JobTimeout bounds one job (all sizes); 0 = 3 minutes.
	JobTimeout time.Duration
}

// Service renders and serves thumbnails.
type Service struct {
	cfg    Config
	runner *exec.Runner
	store  db.MediaRepo
	media  *media.Service
	sys    *fsjail.SysDir
	logger *slog.Logger
	q      *queue
	wg     sync.WaitGroup
	now    func() time.Time

	rendered atomic.Int64
	failed   atomic.Int64
}

// New opens DATA_DIR/thumbs and prepares the queue (workers start with Start).
func New(cfg Config, runner *exec.Runner, store db.MediaRepo, mediaSvc *media.Service, logger *slog.Logger) (*Service, error) {
	if cfg.Workers <= 0 {
		cfg.Workers = 1
	}
	if cfg.JobTimeout <= 0 {
		cfg.JobTimeout = 3 * time.Minute
	}
	if logger == nil {
		logger = slog.Default()
	}
	sys, err := fsjail.OpenSys(cfg.DataDir, "thumbs")
	if err != nil {
		return nil, err
	}
	s := &Service{cfg: cfg, runner: runner, store: store, media: mediaSvc, sys: sys, logger: logger, q: newQueue(), now: time.Now}
	if mediaSvc != nil {
		mediaSvc.SetEnqueue(func(userID, username string, path fsjail.VPath) {
			s.q.push(Job{UserID: userID, Username: username, Path: path, ProbeOnly: true, Low: true})
		})
	}
	return s, nil
}

// Start launches the worker pool; it stops when ctx is cancelled.
func (s *Service) Start(ctx context.Context) {
	for range s.cfg.Workers {
		s.wg.Add(1)
		go s.worker(ctx)
	}
	go func() {
		<-ctx.Done()
		s.q.close()
	}()
}

// Wait blocks until every worker has exited (after Start's ctx is cancelled).
func (s *Service) Wait() { s.wg.Wait() }

// Close releases the cache directory handle.
func (s *Service) Close() error { return s.sys.Close() }

// Stats reports counters for the admin dashboard.
func (s *Service) Stats() (queued int, rendered, failed int64) {
	return s.q.Len(), s.rendered.Load(), s.failed.Load()
}

// Workers reports the pool size.
func (s *Service) Workers() int { return s.cfg.Workers }

// MissingTools lists the external tools that are not available; each
// disables part of the pipeline (readiness reports them as warnings).
func (s *Service) MissingTools() []string {
	var out []string
	for _, t := range []struct{ name, path string }{
		{"vipsthumbnail", s.cfg.Vips}, {"ffmpeg", s.cfg.FFmpeg}, {"ffprobe", s.cfg.FFprobe}, {"pdftoppm", s.cfg.Pdftoppm},
	} {
		if !exec.Executable(t.path) {
			out = append(out, t.name)
		}
	}
	return out
}

// Thumbable reports whether the listing category can have a thumbnail at
// all (type-based, before probing).
func Thumbable(category string) bool {
	switch files.Category(category) {
	case files.CatImage, files.CatVideo, files.CatPDF, files.CatAudio:
		return true
	}
	return false
}

// toolsFor reports whether the tools needed for kind are present.
func (s *Service) toolsFor(kind media.Kind) bool {
	if !exec.Executable(s.cfg.Vips) {
		return false
	}
	switch kind {
	case media.KindVideo, media.KindAudio:
		return exec.Executable(s.cfg.FFmpeg)
	case media.KindPDF:
		return exec.Executable(s.cfg.Pdftoppm)
	case media.KindImage, media.KindSVG:
		return true
	}
	return false
}

// Lookup reports whether the cache holds (key, size).
func (s *Service) Lookup(userID, key string, size Size) (os.FileInfo, bool) {
	fi, err := s.sys.Lstat(CacheRel(userID, key, size))
	if err != nil || !fi.Mode().IsRegular() {
		return nil, false
	}
	return fi, true
}

// Open opens a cached thumbnail for serving.
func (s *Service) Open(userID, key string, size Size) (*os.File, error) {
	return s.sys.Open(CacheRel(userID, key, size))
}

// Request answers a GET /files/thumb for info: ready when cached, the
// persisted failed/unsupported state, or pending after queueing a job.
func (s *Service) Request(ctx context.Context, job Job, info fsjail.Info, size Size) Status {
	key := Key(string(info.Path), info.Size, info.ModTime.UnixNano())
	if _, ok := s.Lookup(job.UserID, key, size); ok {
		return StatusReady
	}
	if row, ok := s.media.Get(ctx, job.UserID, info); ok {
		switch row.ThumbState {
		case media.ThumbFailed:
			return StatusFailed
		case media.ThumbUnsupported:
			return StatusUnsupported
		}
		if !s.toolsFor(media.KindOfRow(row)) {
			return StatusFailed
		}
	} else {
		ti := files.Lookup(info.Name)
		if !Thumbable(string(ti.Category)) {
			return StatusUnsupported
		}
		if !s.toolsFor(media.KindOf("", info.Name)) {
			return StatusFailed
		}
	}
	s.q.push(job)
	return StatusPending
}

// Enqueue adds background work (folder batches, probes).
func (s *Service) Enqueue(job Job) { s.q.push(job) }

// Invalidate removes cached files and rows for path (recursively when
// isDir) and drops queued jobs below it. Called after delete/move/overwrite
// (hook OnChange of SPEC §8.4).
func (s *Service) Invalidate(ctx context.Context, userID string, path fsjail.VPath, isDir bool) error {
	s.q.drop(userID, path)
	var rows []sqlc.MediaMetadatum
	if isDir {
		var err error
		rows, err = s.store.ListMediaPrefix(ctx, userID, string(path))
		if err != nil {
			return err
		}
	} else if row, err := s.store.GetMedia(ctx, userID, string(path)); err == nil {
		rows = []sqlc.MediaMetadatum{row}
	}
	for _, row := range rows {
		s.removeFiles(userID, Key(row.Path, row.Size, row.MtimeUnix))
	}
	return s.media.Invalidate(ctx, userID, path, isDir)
}

func (s *Service) removeFiles(userID, key string) {
	for _, size := range Sizes {
		if err := s.sys.Remove(CacheRel(userID, key, size)); err != nil && !errors.Is(err, fsjail.ErrNotFound) && !errors.Is(err, os.ErrNotExist) {
			s.logger.Debug("thumb remove failed", slog.String("key", key), slog.String("error", err.Error()))
		}
	}
}

func (s *Service) worker(ctx context.Context) {
	defer s.wg.Done()
	for {
		job, ok := s.q.pop()
		if !ok {
			return
		}
		s.process(ctx, job)
		s.q.done(job)
	}
}

// process runs one job end to end; errors are persisted as thumb_state so a
// broken file is not retried until it changes.
func (s *Service) process(ctx context.Context, job Job) {
	jctx, cancel := context.WithTimeout(ctx, s.cfg.JobTimeout)
	defer cancel()
	j, err := fsjail.OpenUser(s.cfg.StorageRoot, job.Username, job.UserID)
	if err != nil {
		s.logger.Debug("thumb job: open user root failed", slog.String("user", job.Username), slog.String("error", err.Error()))
		return
	}
	defer func() { _ = j.Close() }()
	info, err := j.Lstat(job.Path)
	if err != nil || !info.IsRegular() {
		_ = s.store.DeleteMedia(jctx, job.UserID, string(job.Path))
		return
	}
	row, _, err := s.media.Ensure(jctx, j, job.UserID, info)
	if err != nil {
		s.logger.Debug("thumb job: probe failed", slog.String("path", string(job.Path)), slog.String("error", j.Redact(err.Error())))
		return
	}
	if job.ProbeOnly || row.ThumbState == media.ThumbFailed || row.ThumbState == media.ThumbUnsupported {
		return
	}
	key := Key(string(info.Path), info.Size, info.ModTime.UnixNano())
	if row.ThumbState == media.ThumbReady && s.allSizesCached(job.UserID, key) {
		return
	}
	kind := media.KindOfRow(row)
	state, thumbErr := media.ThumbReady, (*string)(nil)
	switch {
	case kind == media.KindOther || (kind == media.KindAudio && row.HasCover == 0):
		state = media.ThumbUnsupported
	case !s.toolsFor(kind):
		// do not persist: the tool may be installed later
		return
	default:
		if err := s.render(jctx, j, job, info, row, kind, key); err != nil {
			if ctx.Err() != nil {
				return // shutdown: leave the state untouched, the next request retries
			}
			state = media.ThumbFailed
			msg := j.Redact(err.Error())
			if len(msg) > 500 {
				msg = msg[:500]
			}
			thumbErr = db.Ptr(msg)
			s.failed.Add(1)
			s.logger.Info("thumbnail failed", slog.String("path", string(job.Path)), slog.String("kind", string(kind)), slog.String("error", msg))
		} else {
			s.rendered.Add(1)
		}
	}
	if err := s.store.SetMediaThumbState(jctx, sqlc.SetMediaThumbStateParams{
		ThumbState: state, ThumbError: thumbErr, ComputedAt: db.FormatTime(s.now()), UserID: job.UserID, Path: string(job.Path),
	}); err != nil {
		s.logger.Warn("thumb state update failed", slog.String("path", string(job.Path)), slog.String("error", err.Error()))
	}
}

func (s *Service) allSizesCached(userID, key string) bool {
	for _, size := range Sizes {
		if _, ok := s.Lookup(userID, key, size); !ok {
			return false
		}
	}
	return true
}

// render produces every size for one file: an intermediate PNG/JPEG for
// video/PDF/audio (ffmpeg, pdftoppm, ffmpeg cover) then vipsthumbnail →
// WebP, written to a temp name and renamed into place.
func (s *Service) render(ctx context.Context, j *fsjail.Jail, job Job, info fsjail.Info, row sqlc.MediaMetadatum, kind media.Kind, key string) error {
	src, err := j.AbsForCLI(job.Path)
	if err != nil {
		return err
	}
	dir := CacheDir(job.UserID, key)
	if err := s.sys.MkdirAll(dir); err != nil {
		return err
	}
	hostDir, err := s.sys.HostPath(dir)
	if err != nil {
		return err
	}
	nonce := randHex(6)
	var temps []string
	defer func() {
		for _, t := range temps {
			_ = s.sys.Remove(t)
		}
	}()
	tmp := func(suffix string) (rel, host string) {
		rel = dir + "/." + key[:16] + "-" + nonce + suffix
		temps = append(temps, rel)
		return rel, filepath.Join(hostDir, filepath.Base(rel))
	}

	var input string
	switch kind {
	case media.KindVideo:
		_, frame := tmp("-frame.png")
		ss := 0.0
		if row.DurationMs != nil && *row.DurationMs > 0 {
			ss = float64(*row.DurationMs) / 1000 * 0.1
		}
		if err := s.ffmpegFrame(ctx, src, frame, ss); err != nil {
			if ss == 0 {
				return err
			}
			if err2 := s.ffmpegFrame(ctx, src, frame, 0); err2 != nil {
				return err2
			}
		}
		input = frame
	case media.KindAudio:
		_, cover := tmp("-cover.jpg")
		if _, err := s.runner.Run(ctx, exec.Spec{
			Bin:     s.cfg.FFmpeg,
			Args:    []string{"-nostdin", "-v", "error", "-i", src, "-an", "-c:v", "copy", "-f", "image2", "-frames:v", "1", "-y", cover},
			Timeout: 30 * time.Second,
		}); err != nil {
			return fmt.Errorf("cover: %w", err)
		}
		input = cover
	case media.KindPDF:
		rel, prefix := tmp("-page")
		temps = append(temps, rel+".png")
		if _, err := s.runner.Run(ctx, exec.Spec{
			Bin:     s.cfg.Pdftoppm,
			Args:    []string{"-singlefile", "-f", "1", "-l", "1", "-scale-to", strconv.Itoa(int(Size512)), "-png", "--", src, prefix},
			Timeout: 60 * time.Second,
		}); err != nil {
			return fmt.Errorf("pdf: %w", err)
		}
		input = prefix + ".png"
	case media.KindImage, media.KindSVG:
		input = src
	default:
		return fmt.Errorf("thumbs: unsupported kind %q", kind)
	}
	// Render every size to a temp name first, then publish them together so a
	// request for any size never sees a half-finished job.
	rendered := make([]string, 0, len(Sizes))
	for _, size := range Sizes {
		outRel, outHost := tmp("-" + strconv.Itoa(int(size)) + ".webp")
		if _, err := s.runner.Run(ctx, exec.Spec{
			Bin:     s.cfg.Vips,
			Args:    []string{"-s", fmt.Sprintf("%dx%d", size, size), "-o", outHost + "[Q=80,strip]", input},
			Timeout: 45 * time.Second,
		}); err != nil {
			return fmt.Errorf("vips %d: %w", size, err)
		}
		rendered = append(rendered, outRel)
	}
	for i, size := range Sizes {
		final := CacheRel(job.UserID, key, size)
		_ = s.sys.Remove(final)
		if err := s.sys.Rename(rendered[i], final); err != nil {
			return err
		}
	}
	_ = info
	return nil
}

func (s *Service) ffmpegFrame(ctx context.Context, src, out string, ss float64) error {
	args := []string{"-nostdin", "-v", "error"}
	if ss > 0 {
		args = append(args, "-ss", strconv.FormatFloat(ss, 'f', 3, 64))
	}
	args = append(args, "-i", src, "-frames:v", "1",
		"-vf", fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease", Size512, Size512),
		"-f", "image2", "-c:v", "png", "-y", out)
	_, err := s.runner.Run(ctx, exec.Spec{Bin: s.cfg.FFmpeg, Args: args, Timeout: 60 * time.Second})
	if err != nil {
		return fmt.Errorf("frame: %w", err)
	}
	return nil
}

func randHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b)
}

// Preview renders (or serves from cache) the 2048 px variant of a raster or
// SVG image synchronously for GET /files/raw?variant=preview (Step 12b).
// Other kinds answer StatusUnsupported so the caller falls back to the 512
// thumbnail; a render failure answers StatusFailed without persisting.
func (s *Service) Preview(ctx context.Context, j *fsjail.Jail, job Job, info fsjail.Info) Status {
	key := Key(string(info.Path), info.Size, info.ModTime.UnixNano())
	if _, ok := s.Lookup(job.UserID, key, SizePreview); ok {
		return StatusReady
	}
	row, _, err := s.media.Ensure(ctx, j, job.UserID, info)
	if err != nil {
		return StatusFailed
	}
	kind := media.KindOfRow(row)
	if kind != media.KindImage && kind != media.KindSVG {
		return StatusUnsupported
	}
	if s.cfg.Vips == "" || !exec.Executable(s.cfg.Vips) {
		return StatusFailed
	}
	src, err := j.AbsForCLI(job.Path)
	if err != nil {
		return StatusFailed
	}
	dir := CacheDir(job.UserID, key)
	if err := s.sys.MkdirAll(dir); err != nil {
		return StatusFailed
	}
	hostDir, err := s.sys.HostPath(dir)
	if err != nil {
		return StatusFailed
	}
	tmpRel := dir + "/." + key[:16] + "-" + randHex(6) + "-preview.webp"
	tmpHost := filepath.Join(hostDir, filepath.Base(tmpRel))
	if _, err := s.runner.Run(ctx, exec.Spec{
		Bin:     s.cfg.Vips,
		Args:    []string{"-s", fmt.Sprintf("%dx%d", SizePreview, SizePreview), "-o", tmpHost + "[Q=85,strip]", src},
		Timeout: 60 * time.Second,
	}); err != nil {
		_ = s.sys.Remove(tmpRel)
		s.logger.Info("preview failed", slog.String("path", string(job.Path)), slog.String("error", j.Redact(err.Error())))
		return StatusFailed
	}
	final := CacheRel(job.UserID, key, SizePreview)
	_ = s.sys.Remove(final)
	if err := s.sys.Rename(tmpRel, final); err != nil {
		_ = s.sys.Remove(tmpRel)
		return StatusFailed
	}
	return StatusReady
}
