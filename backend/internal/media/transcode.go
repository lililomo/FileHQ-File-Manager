package media

// On-the-fly HLS transcoding (SPEC §8.14 "Transcoding HLS", §7.1
// transcode_hls, Step 23d): a video the browser cannot decode is converted
// by ffmpeg into H.264/AAC HLS segments under DATA_DIR/tmp/hls/<task id>/
// (system directory, never user storage). The playlist is an EVENT
// playlist, so playback starts as soon as the first segments exist while
// ffmpeg keeps appending. CPU is bounded by the admin settings
// (transcode.*): disabled by default, nice/ionice through internal/exec, a
// global concurrency cap enforced by the task engine, a duration cap, a disk
// budget, and one active conversion per user (HTTP layer). A sweeper removes
// output folders one hour after the task finished and was last played.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sys/unix"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/exec"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/tasks"
)

// Transcoding constants (SPEC §8.14).
const (
	// TaskTranscodeHLS is the task type (SPEC §7.1).
	TaskTranscodeHLS = "transcode_hls"
	// PlaylistName is the playlist file of an output folder.
	PlaylistName = "playlist.m3u8"
	// HLSSegmentSeconds is the target segment length (keyframes forced).
	HLSSegmentSeconds = 4
	// HLSReadySegments: the player starts once this many segments exist.
	HLSReadySegments = 2
	// DefaultHLSDiskBudget bounds DATA_DIR/tmp/hls; new tasks are refused above it.
	DefaultHLSDiskBudget int64 = 10 << 30
	// DefaultHLSIdleTTL: an output folder is removed this long after the task
	// finished and after the last playlist/segment request.
	DefaultHLSIdleTTL = time.Hour
	// MaxTranscodeDuration is the hard cap of transcode.max_duration_minutes.
	MaxTranscodeDuration = 3 * time.Hour
	// DefaultHLSMinDiskFree: below this free space no task starts (QUOTA_EXCEEDED disk).
	DefaultHLSMinDiskFree int64 = 512 << 20
	// MaxPlaylistBytes bounds the playlist read for rewriting (3 h ≈ 110 KB).
	MaxPlaylistBytes = 4 << 20

	hlsSub = "tmp/hls"
)

// Refusal reasons (details.reason of the API error, task error message).
const (
	ReasonDisabled        = "transcode_disabled"
	ReasonNotVideo        = "not_video"
	ReasonTooLong         = "too_long"
	ReasonUnknownDuration = "unknown_duration"
	ReasonDiskBudget      = "disk_budget"
	ReasonDiskFull        = "disk"
	ReasonSourceChanged   = "source_changed"
	ReasonBadPayload      = "bad_payload"
	ReasonFailed          = "ffmpeg_failed"
	ReasonTimeout         = "timeout"
)

var (
	hlsIDRe      = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	hlsSegmentRe = regexp.MustCompile(`^seg_[0-9]{5}\.ts$`)
)

// Serving errors.
var (
	// ErrHLSNotFound: unknown, removed or foreign task id, or a bad file name.
	ErrHLSNotFound = errors.New("hls: not found")
	// ErrHLSNotReady: the task runs but ffmpeg has not written the playlist yet.
	ErrHLSNotReady = errors.New("hls: not ready")
)

// ValidHLSID reports whether id has the shape of a task id (UUIDv7).
func ValidHLSID(id string) bool { return hlsIDRe.MatchString(id) }

// ValidHLSSegment reports whether name is a segment file name (seg_NNNNN.ts).
func ValidHLSSegment(name string) bool { return hlsSegmentRe.MatchString(name) }

// TranscodeSettings are the admin settings transcode.* (live).
type TranscodeSettings struct {
	Enabled       bool
	MaxConcurrent int
	MaxHeight     int // 480, 720 or 1080
	MaxDuration   time.Duration
	Nice          int
}

// ResolutionHeight maps transcode.max_resolution to a frame height.
func ResolutionHeight(s string) int {
	switch s {
	case "480p":
		return 480
	case "1080p":
		return 1080
	}
	return 720
}

// TranscodePayload is the task payload (no secrets): the source file and
// the facts checked at enqueue time.
type TranscodePayload struct {
	Path       string `json:"path"`
	Size       int64  `json:"size"`
	MtimeNs    int64  `json:"mtimeNs"`
	DurationMs int64  `json:"durationMs"`
}

// TranscodeResult is the task result.
type TranscodeResult struct {
	Dir      string  `json:"dir"`
	Playlist string  `json:"playlist"`
	Duration float64 `json:"duration"`
	Width    int     `json:"width,omitempty"`
	Height   int     `json:"height,omitempty"`
	Segments int     `json:"segments"`
}

// TranscodeError is a refusal or a failed conversion; the HTTP layer maps
// Reason to the SPEC §6.2 code and the task engine reads TaskCode.
type TranscodeError struct {
	Reason     string
	DurationMs int64
	MaxMinutes int
}

func (e *TranscodeError) Error() string { return "transcode: " + e.Reason }

// TaskCode maps the reason to the task error code (files.TaskErrorCode).
func (e *TranscodeError) TaskCode() (string, string) {
	switch e.Reason {
	case ReasonDisabled:
		return "FORBIDDEN", e.Reason
	case ReasonNotVideo:
		return "UNSUPPORTED_TYPE", e.Reason
	case ReasonTooLong, ReasonUnknownDuration, ReasonSourceChanged, ReasonBadPayload:
		return "VALIDATION_FAILED", e.Reason
	case ReasonDiskBudget:
		return "CONFLICT", e.Reason
	case ReasonDiskFull:
		return "QUOTA_EXCEEDED", e.Reason
	case ReasonTimeout:
		return "TIMEOUT", "timed out"
	}
	return "INTERNAL", "conversion failed"
}

// TaskGetter reads a task row by id (the sweeper adopts folders of finished
// tasks after a restart; db.Store implements it).
type TaskGetter interface {
	GetTask(ctx context.Context, id string) (sqlc.Task, error)
}

// TranscoderOptions configures a Transcoder.
type TranscoderOptions struct {
	Runner  *exec.Runner
	Prober  *Prober // FFprobe must be set
	FFmpeg  string
	DataDir string
	// Settings supplies transcode.* live; nil = disabled.
	Settings func() TranscodeSettings
	Tasks    TaskGetter
	Logger   *slog.Logger
	Now      func() time.Time
	// Threads of the encoder; 0 = max(1, NumCPU/2).
	Threads int
	// DiskBudget, IdleTTL, MinDiskFree: 0 = the defaults above (tests shrink them).
	DiskBudget  int64
	IdleTTL     time.Duration
	MinDiskFree int64
	// SweepEvery is the sweeper period; 0 = min(5 min, IdleTTL/2).
	SweepEvery time.Duration
}

type hlsEntry struct {
	userID     string
	path       string
	size       int64
	mtimeNs    int64
	done       bool
	finished   time.Time
	lastAccess time.Time
	segments   int
}

// Transcoder runs transcode_hls tasks and serves/cleans their output.
type Transcoder struct {
	o       TranscoderOptions
	logger  *slog.Logger
	now     func() time.Time
	mu      sync.Mutex
	entries map[string]*hlsEntry // task id → output folder state
}

// NewTranscoder builds a Transcoder (call Register, then Start).
func NewTranscoder(o TranscoderOptions) *Transcoder {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Threads <= 0 {
		o.Threads = max(1, runtime.NumCPU()/2)
	}
	if o.DiskBudget <= 0 {
		o.DiskBudget = DefaultHLSDiskBudget
	}
	if o.IdleTTL <= 0 {
		o.IdleTTL = DefaultHLSIdleTTL
	}
	if o.MinDiskFree <= 0 {
		o.MinDiskFree = DefaultHLSMinDiskFree
	}
	if o.SweepEvery <= 0 {
		o.SweepEvery = min(5*time.Minute, max(time.Second, o.IdleTTL/2))
	}
	return &Transcoder{o: o, logger: o.Logger, now: o.Now, entries: map[string]*hlsEntry{}}
}

// Available reports whether ffmpeg and ffprobe can be run.
func (t *Transcoder) Available() bool {
	return t != nil && exec.Executable(t.o.FFmpeg) && t.o.Prober != nil && exec.Executable(t.o.Prober.FFprobe)
}

// Settings returns the live settings (disabled when none are configured).
func (t *Transcoder) Settings() TranscodeSettings {
	if t == nil || t.o.Settings == nil {
		return TranscodeSettings{MaxConcurrent: 1, MaxHeight: 720, MaxDuration: MaxTranscodeDuration, Nice: 19}
	}
	s := t.o.Settings()
	if s.MaxConcurrent <= 0 {
		s.MaxConcurrent = 1
	}
	if s.MaxHeight <= 0 {
		s.MaxHeight = 720
	}
	if s.MaxDuration <= 0 || s.MaxDuration > MaxTranscodeDuration {
		s.MaxDuration = MaxTranscodeDuration
	}
	return s
}

// Enabled reports transcode.enabled with the tools present.
func (t *Transcoder) Enabled() bool { return t.Available() && t.Settings().Enabled }

// Register adds transcode_hls to the engine; running tasks of the type are
// capped by transcode.max_concurrent (the rest stays queued).
func (t *Transcoder) Register(e *tasks.Engine) {
	e.Register(TaskTranscodeHLS, tasks.Spec{
		Unit:          tasks.UnitItems, // one item = one second of media (no migration of the progress_unit CHECK)
		Run:           t.run,
		MaxConcurrent: func() int { return t.Settings().MaxConcurrent },
		Link:          transcodeLink,
	})
}

func transcodeLink(task tasks.Task) string {
	var p TranscodePayload
	if json.Unmarshal(task.Payload, &p) != nil || p.Path == "" {
		return ""
	}
	vp, err := fsjail.Clean(p.Path)
	if err != nil {
		return ""
	}
	return "/files" + strings.TrimSuffix(string(vp.Dir()), "/") + "?view=" + url.QueryEscape(string(vp))
}

// Check validates a source at enqueue time: transcoding enabled, a probed
// video stream with a known duration within transcode.max_duration_minutes.
// It returns the task payload.
func (t *Transcoder) Check(ctx context.Context, j *fsjail.Jail, info fsjail.Info) (TranscodePayload, error) {
	set := t.Settings()
	if !set.Enabled {
		return TranscodePayload{}, &TranscodeError{Reason: ReasonDisabled}
	}
	m, err := t.probe(ctx, j, info)
	if err != nil {
		return TranscodePayload{}, err
	}
	if err := validateMeta(m, set); err != nil {
		return TranscodePayload{}, err
	}
	return TranscodePayload{Path: string(info.Path), Size: info.Size, MtimeNs: info.ModTime.UnixNano(), DurationMs: m.DurationMs}, nil
}

func (t *Transcoder) probe(ctx context.Context, j *fsjail.Jail, info fsjail.Info) (Meta, error) {
	if !info.IsRegular() {
		return Meta{}, &TranscodeError{Reason: ReasonNotVideo}
	}
	if t.o.Prober == nil || t.o.Prober.FFprobe == "" {
		return Meta{}, &exec.MissingError{Bin: "ffprobe"}
	}
	m, err := t.o.Prober.Probe(ctx, j, info.Path, info.Name)
	if err != nil {
		return m, err
	}
	return m, nil
}

func validateMeta(m Meta, set TranscodeSettings) error {
	if m.Kind != KindVideo || m.CodecVideo == "" {
		return &TranscodeError{Reason: ReasonNotVideo}
	}
	maxMin := int(set.MaxDuration / time.Minute)
	if m.DurationMs <= 0 {
		return &TranscodeError{Reason: ReasonUnknownDuration, MaxMinutes: maxMin}
	}
	if time.Duration(m.DurationMs)*time.Millisecond > set.MaxDuration {
		return &TranscodeError{Reason: ReasonTooLong, DurationMs: m.DurationMs, MaxMinutes: maxMin}
	}
	return nil
}

// CheckDisk refuses a new conversion when DATA_DIR/tmp/hls exceeds the
// budget (CONFLICT disk_budget) or the disk is nearly full (QUOTA_EXCEEDED).
func (t *Transcoder) CheckDisk() error {
	if t.DiskUsage() >= t.o.DiskBudget {
		return &TranscodeError{Reason: ReasonDiskBudget}
	}
	var st unix.Statfs_t
	if err := unix.Statfs(filepath.Join(t.o.DataDir, "tmp"), &st); err == nil {
		if free := int64(st.Bavail) * st.Bsize; free < t.o.MinDiskFree { //nolint:gosec // block counts fit int64
			return &TranscodeError{Reason: ReasonDiskFull}
		}
	}
	return nil
}

// DiskUsage sums the bytes under DATA_DIR/tmp/hls.
func (t *Transcoder) DiskUsage() int64 {
	root, err := fsjail.OpenSys(t.o.DataDir, hlsSub)
	if err != nil {
		return 0
	}
	defer func() { _ = root.Close() }()
	var total int64
	_ = fs.WalkDir(root.FS(), ".", func(_ string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil //nolint:nilerr // unreadable entries are skipped
		}
		if info, ierr := d.Info(); ierr == nil {
			total += info.Size()
		}
		return nil
	})
	return total
}

// HLSArgs is the fixed ffmpeg argv (no user input other than the two
// absolute paths, which cannot start with "-").
func HLSArgs(in, outDir string, maxHeight, threads int) []string {
	return []string{
		"-nostdin", "-hide_banner", "-loglevel", "error", "-y",
		"-progress", "pipe:1", "-nostats",
		"-i", in,
		"-map", "0:v:0", "-map", "0:a:0?",
		// cap the height, keep the aspect ratio, even dimensions for yuv420p
		"-vf", fmt.Sprintf("scale=w=-2:h='trunc(min(ih,%d)/2)*2'", maxHeight),
		"-c:v", "libx264", "-preset", "veryfast", "-crf", "23", "-profile:v", "main", "-pix_fmt", "yuv420p",
		"-force_key_frames", fmt.Sprintf("expr:gte(t,n_forced*%d)", HLSSegmentSeconds),
		"-c:a", "aac", "-b:a", "128k", "-ac", "2",
		"-sn", "-dn",
		"-threads", strconv.Itoa(threads),
		"-f", "hls", "-hls_time", strconv.Itoa(HLSSegmentSeconds), "-hls_playlist_type", "event",
		"-hls_flags", "temp_file",
		"-hls_segment_filename", filepath.Join(outDir, "seg_%05d.ts"),
		filepath.Join(outDir, PlaylistName),
	}
}

// TranscodeTimeout is min(3 h, duration × 4 + 60 s).
func TranscodeTimeout(durationMs int64) time.Duration {
	d := time.Duration(durationMs)*time.Millisecond*4 + time.Minute
	return min(d, MaxTranscodeDuration)
}

// ScaledSize is the output frame size of HLSArgs' scale filter.
func ScaledSize(w, h, maxHeight int) (int, int) {
	if w <= 0 || h <= 0 {
		return 0, 0
	}
	oh := min(h, maxHeight) / 2 * 2
	ow := int(math.Round(float64(w)*float64(oh)/float64(h)/2)) * 2
	return ow, oh
}

func (t *Transcoder) run(ctx context.Context, rt *tasks.Runtime) (any, error) {
	var p TranscodePayload
	if json.Unmarshal(rt.Task.Payload, &p) != nil || p.Path == "" {
		return nil, &TranscodeError{Reason: ReasonBadPayload}
	}
	set := t.Settings()
	if !set.Enabled {
		return nil, &TranscodeError{Reason: ReasonDisabled}
	}
	vp, err := fsjail.Clean(p.Path)
	if err != nil {
		return nil, err
	}
	j, release, err := rt.Jail(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	info, err := j.Stat(vp)
	if err != nil {
		return nil, err
	}
	if info.Size != p.Size || info.ModTime.UnixNano() != p.MtimeNs {
		return nil, &TranscodeError{Reason: ReasonSourceChanged}
	}
	// probe again: the settings or the file may have changed while queued
	m, err := t.probe(ctx, j, info)
	if err != nil {
		return nil, err
	}
	if err := validateMeta(m, set); err != nil {
		return nil, err
	}
	if err := t.CheckDisk(); err != nil {
		return nil, err
	}
	in, err := j.AbsForCLI(vp)
	if err != nil {
		return nil, err
	}
	id := rt.Task.ID
	// register before the folder exists so the sweeper never sees an
	// unowned folder of a running task
	t.mu.Lock()
	t.entries[id] = &hlsEntry{userID: rt.Task.UserID, path: string(vp), size: p.Size, mtimeNs: p.MtimeNs}
	t.mu.Unlock()
	ok := false
	defer func() {
		if !ok {
			t.remove(id)
		}
	}()
	dir, err := fsjail.OpenSys(t.o.DataDir, hlsSub+"/"+id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	outDir, err := dir.HostPath(".")
	if err != nil {
		return nil, err
	}
	total := (m.DurationMs + 999) / 1000
	rt.Progress(0, total)
	rt.Log("info", "transcoding %s (%s, %dx%d, %ds) to HLS ≤ %dp, nice %d", vp.Base(), m.CodecVideo, m.Width, m.Height, total, set.MaxHeight, set.Nice)

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()
	var (
		disabled  atomic.Bool // set by the ffmpeg stdout goroutine
		lastCheck time.Time
	)
	pw := &progressWriter{onBlock: func(outUs int64) {
		if outUs >= 0 {
			rt.Progress(min(outUs/1_000_000, total), total)
		}
		now := t.now()
		if now.Sub(lastCheck) < time.Second {
			return
		}
		lastCheck = now
		t.countSegments(id, dir)
		if !t.Settings().Enabled { // admin switched transcoding off: stop now
			disabled.Store(true)
			cancelRun()
		}
	}}
	nice := set.Nice
	start := t.now()
	_, err = t.o.Runner.Run(runCtx, exec.Spec{
		Bin: t.o.FFmpeg, Args: HLSArgs(in, outDir, set.MaxHeight, t.o.Threads), Dir: outDir,
		Timeout: TranscodeTimeout(m.DurationMs), Nice: &nice, Stdout: pw, MaxOutput: 64 << 10,
	})
	if err != nil {
		switch {
		case disabled.Load():
			rt.Log("warn", "stopped: transcoding was disabled by an administrator")
			return nil, &TranscodeError{Reason: ReasonDisabled}
		case ctx.Err() != nil:
			return nil, ctx.Err()
		case errors.Is(err, exec.ErrTimeout):
			rt.Log("error", "ffmpeg timed out after %s", TranscodeTimeout(m.DurationMs))
			return nil, &TranscodeError{Reason: ReasonTimeout}
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			msg := j.Redact(strings.ReplaceAll(ee.Stderr, outDir, "<hls>"))
			rt.Log("error", "ffmpeg failed (status %d): %s", ee.Code, trim(strings.TrimSpace(msg)))
			return nil, &TranscodeError{Reason: ReasonFailed}
		}
		return nil, err
	}
	segments := t.countSegments(id, dir)
	if segments == 0 {
		rt.Log("error", "ffmpeg produced no segment")
		return nil, &TranscodeError{Reason: ReasonFailed}
	}
	now := t.now()
	t.mu.Lock()
	if e := t.entries[id]; e != nil {
		e.done, e.finished, e.lastAccess, e.segments = true, now, now, segments
	}
	t.mu.Unlock()
	ok = true
	w, h := ScaledSize(m.Width, m.Height, set.MaxHeight)
	rt.Log("info", "%d segments in %s", segments, now.Sub(start).Round(time.Second))
	return TranscodeResult{Dir: id, Playlist: "/files/hls/" + id + "/" + PlaylistName, Duration: float64(m.DurationMs) / 1000,
		Width: w, Height: h, Segments: segments}, nil
}

// countSegments counts finished segment files (ffmpeg's temp_file flag
// writes *.ts.tmp and renames when complete) and stores the count.
func (t *Transcoder) countSegments(id string, dir *fsjail.SysDir) int {
	names, err := dir.ReadDirNames(".")
	if err != nil {
		return 0
	}
	n := 0
	for _, name := range names {
		if hlsSegmentRe.MatchString(name) {
			n++
		}
	}
	t.mu.Lock()
	if e := t.entries[id]; e != nil {
		e.segments = n
	}
	t.mu.Unlock()
	return n
}

// remove deletes an output folder and forgets it.
func (t *Transcoder) remove(id string) {
	t.mu.Lock()
	delete(t.entries, id)
	t.mu.Unlock()
	root, err := fsjail.OpenSys(t.o.DataDir, hlsSub)
	if err != nil {
		return
	}
	defer func() { _ = root.Close() }()
	if err := root.RemoveAll(id); err != nil {
		t.logger.Warn("hls folder removal failed", slog.String("task", id), slog.String("error", err.Error()))
	}
}

// Ready reports whether playback of task id can start (≥ 2 segments or done).
func (t *Transcoder) Ready(id string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.entries[id]
	return e != nil && (e.done || e.segments >= HLSReadySegments)
}

// FindDone returns a finished conversion of the same file version (the
// player reuses it instead of converting again).
func (t *Transcoder) FindDone(userID, path string, size, mtimeNs int64) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for id, e := range t.entries {
		if e.done && e.userID == userID && e.path == path && e.size == size && e.mtimeNs == mtimeNs {
			return id, true
		}
	}
	return "", false
}

// Open returns a playlist or segment of task id for its owner and records
// the access (idle clock). finished reports whether ffmpeg is done.
func (t *Transcoder) Open(userID, id, name string) (f *os.File, info os.FileInfo, finished bool, err error) {
	if !hlsIDRe.MatchString(id) || (name != PlaylistName && !hlsSegmentRe.MatchString(name)) {
		return nil, nil, false, ErrHLSNotFound
	}
	t.mu.Lock()
	e := t.entries[id]
	if e == nil || e.userID != userID {
		t.mu.Unlock()
		return nil, nil, false, ErrHLSNotFound
	}
	e.lastAccess = t.now()
	finished = e.done
	t.mu.Unlock()
	root, err := fsjail.OpenSys(t.o.DataDir, hlsSub)
	if err != nil {
		return nil, nil, false, err
	}
	defer func() { _ = root.Close() }()
	f, err = root.Open(id + "/" + name)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fsjail.ErrNotFound) {
			if name == PlaylistName && !finished {
				return nil, nil, false, ErrHLSNotReady
			}
			return nil, nil, false, ErrHLSNotFound
		}
		return nil, nil, false, err
	}
	info, err = f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, false, ErrHLSNotFound
	}
	return f, info, finished, nil
}

// RewritePlaylist appends the signed query to every segment URI (hls.js and
// Safari resolve segments relative to the playlist URL, so they inherit the
// task signature) and asks players to start at the beginning of an EVENT
// playlist (EXT-X-START), not at its live edge. A URI that is not a segment
// name of ours is an error (the playlist is never passed through blindly).
func RewritePlaylist(data []byte, query string) ([]byte, error) {
	var out bytes.Buffer
	out.Grow(len(data) + len(data)/2)
	hasStart := bytes.Contains(data, []byte("#EXT-X-START:"))
	for i, line := range strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n") {
		switch {
		case i == 0:
			if line != "#EXTM3U" {
				return nil, errors.New("hls: not a playlist")
			}
			out.WriteString(line + "\n")
			if !hasStart {
				out.WriteString("#EXT-X-START:TIME-OFFSET=0\n")
			}
		case line == "" || strings.HasPrefix(line, "#"):
			if line != "" {
				out.WriteString(line + "\n")
			}
		case hlsSegmentRe.MatchString(line):
			out.WriteString(line + "?" + query + "\n")
		default:
			return nil, fmt.Errorf("hls: unexpected playlist entry")
		}
	}
	return out.Bytes(), nil
}

// Start sweeps once (folders left by a previous process) and then every
// SweepEvery until ctx ends.
func (t *Transcoder) Start(ctx context.Context) {
	go func() {
		tick := time.NewTicker(t.o.SweepEvery)
		defer tick.Stop()
		for {
			if _, err := t.Sweep(ctx); err != nil && ctx.Err() == nil {
				t.logger.Warn("hls cleanup failed", slog.String("error", err.Error()))
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
}

// Sweep removes output folders that are no longer needed: stray names,
// folders of tasks that are gone or did not finish (failed, cancelled,
// interrupted by a restart), every finished folder while transcoding is
// disabled, and finished folders idle for IdleTTL (since the task finished
// and since the last playlist/segment request; after a restart the folder
// mtime stands in for the last access). Running conversions are left to
// their runner.
func (t *Transcoder) Sweep(ctx context.Context) (int, error) {
	root, err := fsjail.OpenSys(t.o.DataDir, hlsSub)
	if err != nil {
		return 0, err
	}
	defer func() { _ = root.Close() }()
	names, err := root.ReadDirNames(".")
	if err != nil {
		return 0, err
	}
	enabled := t.Settings().Enabled
	now := t.now()
	removed, kept := 0, 0
	present := make(map[string]bool, len(names))
	for _, name := range names {
		if ctx.Err() != nil {
			return removed, ctx.Err()
		}
		present[name] = true
		drop, why := t.sweepVerdict(ctx, root, name, enabled, now)
		if !drop {
			kept++
			continue
		}
		t.mu.Lock()
		delete(t.entries, name)
		t.mu.Unlock()
		if err := root.RemoveAll(name); err != nil {
			t.logger.Warn("hls folder removal failed", slog.String("dir", name), slog.String("error", err.Error()))
			continue
		}
		removed++
		t.logger.Debug("hls folder removed", slog.String("dir", name), slog.String("reason", why))
	}
	t.mu.Lock()
	for id, e := range t.entries {
		if e.done && !present[id] {
			delete(t.entries, id)
		}
	}
	t.mu.Unlock()
	if removed > 0 {
		t.logger.Info("hls cleanup", slog.Int("removed", removed), slog.Int("kept", kept))
	}
	return removed, nil
}

func (t *Transcoder) sweepVerdict(ctx context.Context, root *fsjail.SysDir, name string, enabled bool, now time.Time) (bool, string) {
	if !hlsIDRe.MatchString(name) {
		return true, "stray"
	}
	t.mu.Lock()
	e := t.entries[name]
	running := e != nil && !e.done
	t.mu.Unlock()
	if running {
		return false, ""
	}
	if !enabled {
		return true, "disabled"
	}
	if t.o.Tasks != nil {
		row, err := t.o.Tasks.GetTask(ctx, name)
		switch {
		case err != nil && db.IsNotFound(err):
			return true, "task_gone"
		case err != nil:
			return false, "" // database trouble: keep, retry next sweep
		case row.Type != TaskTranscodeHLS || row.UserID == nil:
			return true, "not_task"
		case row.State == string(tasks.Queued) || row.State == string(tasks.Running):
			// the engine still owns it: the runner has marked the entry done
			// but finish() has not written the row yet (a restart marks stale
			// running rows interrupted before the first sweep)
			return false, ""
		case row.State != string(tasks.Done):
			return true, "not_done"
		}
		if e == nil {
			if e = t.adopt(root, name, row); e == nil {
				return true, "bad_row"
			}
		}
	} else if e == nil {
		return true, "unknown"
	}
	t.mu.Lock()
	idle := now.Sub(e.finished) >= t.o.IdleTTL && now.Sub(e.lastAccess) >= t.o.IdleTTL
	t.mu.Unlock()
	if idle {
		return true, "idle"
	}
	return false, ""
}

// adopt registers the folder of a finished task found on disk (restart).
func (t *Transcoder) adopt(root *fsjail.SysDir, name string, row sqlc.Task) *hlsEntry {
	var p TranscodePayload
	if json.Unmarshal([]byte(row.PayloadJson), &p) != nil || p.Path == "" {
		return nil
	}
	info, err := root.Lstat(name)
	if err != nil || !info.IsDir() {
		return nil
	}
	e := &hlsEntry{userID: *row.UserID, path: p.Path, size: p.Size, mtimeNs: p.MtimeNs, done: true, lastAccess: info.ModTime()}
	e.finished = info.ModTime()
	if row.FinishedAt != nil {
		if ft, err := db.ParseTime(*row.FinishedAt); err == nil {
			e.finished = ft
		}
	}
	if names, err := root.ReadDirNames(name); err == nil {
		for _, n := range names {
			if hlsSegmentRe.MatchString(n) {
				e.segments++
			}
		}
	}
	t.mu.Lock()
	if cur := t.entries[name]; cur != nil {
		e = cur
	} else {
		t.entries[name] = e
	}
	t.mu.Unlock()
	return e
}

// progressWriter parses ffmpeg's "-progress pipe:1" key=value stream; each
// block ends with "progress=continue|end" and reports out_time_us.
type progressWriter struct {
	onBlock func(outUs int64)
	buf     []byte
	outUs   int64
}

func (w *progressWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "out_time_us":
			if v, err := strconv.ParseInt(val, 10, 64); err == nil && v >= 0 {
				w.outUs = v
			}
		case "progress":
			if w.onBlock != nil {
				w.onBlock(w.outUs)
			}
		}
	}
	if len(w.buf) > 4096 { // a runaway line without newline: drop it
		w.buf = w.buf[:0]
	}
	return len(p), nil
}
