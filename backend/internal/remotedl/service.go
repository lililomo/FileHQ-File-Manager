package remotedl

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/netguard"
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/tasks"
)

// TaskType is the engine type (SPEC §7.1).
const TaskType = "remote_download"

// Limits (SPEC §8.10, §10.5).
const (
	DefaultMaxURLs    = 10
	DefaultRateLimit  = 10
	DefaultRateWindow = time.Hour
	// TmpPrefix names DATA_DIR/tmp/<prefix><task_id> working folders.
	TmpPrefix = "rd-"
	// OrphanAge is how old an abandoned working folder must be before the
	// hourly cleanup removes it (longer than the 24 h total timeout).
	OrphanAge = 48 * time.Hour
	// TotalTimeout bounds one download (SPEC §8.10 step 6).
	TotalTimeout = 24 * time.Hour
)

// Payload is the task payload (SPEC §7.1; no secrets).
type Payload struct {
	URL      string `json:"url"`
	Dest     string `json:"dest"`
	Filename string `json:"filename,omitempty"`
	Policy   string `json:"policy,omitempty"`
}

// Result is the task result.
type Result struct {
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	FinalURL string `json:"finalUrl"`
	Status   string `json:"status"` // created|renamed|overwritten|skipped
	Resumed  int64  `json:"resumedFrom,omitempty"`
}

// Options configures the Service.
type Options struct {
	DataDir string
	// Enabled / MaxSize are REMOTE_DOWNLOAD_ENABLED / REMOTE_DOWNLOAD_MAX_SIZE;
	// the settings table can lower them, never raise them.
	Enabled       bool
	MaxSize       int64
	MaxUploadSize int64
	// MaxUploadSizeFn, when set, supplies MAX_UPLOAD_SIZE live (Step 19a).
	MaxUploadSizeFn func() int64
	// Version and BaseURL build the User-Agent (SPEC §8.10 step 6).
	Version string
	BaseURL string
	Repo    db.Repository
	Quota   *quota.Service
	Files   *files.Service
	Locks   *fsjail.LockManager
	Tasks   *tasks.Engine
	// Guard supplies the base network policy (AllowLoopback in development
	// only); allow/denylists come from the settings per call.
	Guard *netguard.Guard
	// Invalidate drops the listing cache of a folder.
	Invalidate func(userID string, dir fsjail.VPath)
	// OnChange is the thumbnail/media invalidation hook.
	OnChange func(ctx context.Context, userID string, path fsjail.VPath, isDir bool)
	Logger   *slog.Logger
	Now      func() time.Time
	// RetryBackoff between in-task GET attempts after a network error
	// (default 5 s, 15 s, 45 s; tests shorten it).
	RetryBackoff []time.Duration
	MaxURLs      int
	RateLimit    int
	RateWindow   time.Duration
	// ProgressStep is the byte interval between progress reports/ctx checks.
	ProgressStep int64
}

// Service validates and enqueues remote downloads and runs them.
type Service struct {
	o        Options
	logger   *slog.Logger
	now      func() time.Time
	settings *settingsCache
	guard    *netguard.Guard
	limiter  *windowLimiter
	backoff  []time.Duration
	step     int64
}

// New builds a Service and registers the task runner when Tasks is set.
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Guard == nil {
		o.Guard = netguard.New(netguard.Policy{})
	}
	if o.MaxURLs <= 0 {
		o.MaxURLs = DefaultMaxURLs
	}
	if o.RateLimit <= 0 {
		o.RateLimit = DefaultRateLimit
	}
	if o.RateWindow <= 0 {
		o.RateWindow = DefaultRateWindow
	}
	if o.RetryBackoff == nil {
		o.RetryBackoff = []time.Duration{5 * time.Second, 15 * time.Second, 45 * time.Second}
	}
	if o.ProgressStep <= 0 {
		o.ProgressStep = 256 << 10
	}
	s := &Service{o: o, logger: o.Logger, now: o.Now, guard: o.Guard, backoff: o.RetryBackoff, step: o.ProgressStep,
		limiter: newWindowLimiter(o.RateLimit, o.RateWindow, o.Now)}
	var repo db.SettingsRepo
	if o.Repo != nil {
		repo = o.Repo
	}
	s.settings = &settingsCache{repo: repo, logger: o.Logger, now: o.Now, base: Settings{Enabled: o.Enabled, MaxSize: o.MaxSize}}
	s.guard.OnBlocked = func(err *netguard.BlockedError) {
		// SPEC §8.10 step 10: WARN with host and IP, never the full URL.
		s.logger.Warn("remote download blocked", slog.String("reason", err.Reason), slog.String("host", err.Host), slog.String("ip", err.IP), slog.Int("hop", err.Hop))
	}
	if o.Tasks != nil {
		o.Tasks.Register(TaskType, tasks.Spec{Unit: tasks.UnitBytes, Run: s.Run, Link: taskLink})
	}
	return s
}

func taskLink(t tasks.Task) string {
	p, err := decodePayload(t.Payload)
	if err != nil || p.Dest == "" {
		return ""
	}
	return "/files" + strings.TrimSuffix(p.Dest, "/")
}

// Settings returns the effective settings (cached 5 s).
func (s *Service) Settings(ctx context.Context) Settings { return s.settings.get(ctx) }

func (s *Service) maxUploadSize() int64 {
	if s.o.MaxUploadSizeFn != nil {
		if n := s.o.MaxUploadSizeFn(); n > 0 {
			return n
		}
	}
	return s.o.MaxUploadSize
}

// InvalidateSettings drops the settings cache (admin writes, tests).
func (s *Service) InvalidateSettings() { s.settings.Invalidate() }

// guardFor returns a guard carrying the settings' lists on top of the base
// network policy.
func (s *Service) guardFor(st Settings) *netguard.Guard {
	g := *s.guard
	g.Policy.Allowlist = st.Allowlist
	g.Policy.Denylist = st.Denylist
	return &g
}

func (s *Service) userAgent() string {
	v := s.o.Version
	if v == "" {
		v = "dev"
	}
	base := s.o.BaseURL
	if base == "" {
		base = "https://file.kppbwi.app"
	}
	return fmt.Sprintf("FileManager/%s (+%s)", v, base)
}

// ValidateURL parses and checks one URL against the policy (no DNS).
func (s *Service) ValidateURL(ctx context.Context, raw string) (*url.URL, error) {
	st := s.Settings(ctx)
	if !st.Enabled {
		return nil, ErrDisabled
	}
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return nil, newErr(CodeValidation, "url is empty or too long", http.StatusBadRequest, map[string]any{"reason": "url"})
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, newErr(CodeValidation, "url is not valid", http.StatusBadRequest, map[string]any{"reason": "url"})
	}
	// the guard reports scheme/host problems as SSRF_BLOCKED (file:, ftp:,
	// userinfo, empty host), so it runs before any other shape check
	if err := s.guardFor(st).ValidateURL(u); err != nil {
		return nil, blockedToError(err)
	}
	return u, nil
}

func blockedToError(err error) error {
	be, ok := netguard.Unwrap(err)
	if !ok {
		return err
	}
	if be.Listed() {
		return newErr(CodeForbidden, "host is not allowed by the remote download policy", http.StatusForbidden, map[string]any{"reason": be.Reason, "host": be.Host})
	}
	d := map[string]any{"reason": be.Reason, "host": be.Host}
	if be.IP != "" {
		d["ip"] = be.IP
	}
	if be.Hop > 0 {
		d["hop"] = be.Hop
	}
	return newErr(CodeSSRF, "destination address is not allowed", http.StatusUnprocessableEntity, d)
}

// CreateRequest is the API body of POST /remote-downloads.
type CreateRequest struct {
	UserID    string
	URLs      []string
	Dest      string
	Filename  string            // one URL only
	Filenames map[string]string // url → name (SPEC §6.3)
	Policy    string
}

// Create validates every URL, applies the per-user rate limit and enqueues
// one task per URL. Validation errors name the offending URL index.
func (s *Service) Create(ctx context.Context, req CreateRequest) ([]tasks.Task, error) {
	if s.o.Tasks == nil {
		return nil, newErr(CodeInternal, "task engine unavailable", http.StatusServiceUnavailable, nil)
	}
	if !s.Settings(ctx).Enabled {
		return nil, ErrDisabled
	}
	if len(req.URLs) == 0 || len(req.URLs) > s.o.MaxURLs {
		return nil, newErr(CodeValidation, fmt.Sprintf("1..%d urls per request", s.o.MaxURLs), http.StatusBadRequest, map[string]any{"fields": map[string]any{"urls": fmt.Sprintf("1..%d", s.o.MaxURLs)}})
	}
	dest, err := fsjail.Clean(req.Dest)
	if err != nil {
		return nil, newErr(CodeValidation, "destination path is not valid", http.StatusBadRequest, map[string]any{"fields": map[string]any{"path": "invalid"}})
	}
	if _, ok := files.ParsePolicy(req.Policy); !ok {
		return nil, newErr(CodeValidation, "unknown conflict policy", http.StatusBadRequest, map[string]any{"fields": map[string]any{"onConflict": "fail, overwrite, skip or rename"}})
	}
	if len(req.URLs) > 1 && req.Filename != "" {
		return nil, newErr(CodeValidation, "filename applies to a single url; use filenames for several", http.StatusBadRequest, map[string]any{"fields": map[string]any{"filename": "single url only"}})
	}
	type item struct {
		u    *url.URL
		name string
	}
	items := make([]item, 0, len(req.URLs))
	for i, raw := range req.URLs {
		u, err := s.ValidateURL(ctx, raw)
		if err != nil {
			var e *Error
			if errors.As(err, &e) {
				e.Details["index"] = i
			}
			return nil, err
		}
		name := req.Filename
		if n, ok := req.Filenames[raw]; ok {
			name = n
		}
		if name != "" {
			if _, err := fsjail.ValidateName(name); err != nil || strings.ContainsAny(name, "/\\") {
				return nil, newErr(CodeValidation, "file name is not valid", http.StatusBadRequest, map[string]any{"fields": map[string]any{"filename": "invalid"}, "index": i})
			}
			if s.o.Files != nil && s.o.Files.Blocked(name) {
				return nil, newErr(CodeUnsupported, "file extension is blocked", http.StatusUnsupportedMediaType, map[string]any{"reason": "blocked_extension", "index": i})
			}
		}
		items = append(items, item{u: u, name: name})
	}
	if ok, retry := s.limiter.reserve(req.UserID, len(items)); !ok {
		return nil, newErr(CodeRateLimited, "too many remote downloads; try again later", http.StatusTooManyRequests, map[string]any{"retryAfter": int(retry.Seconds()), "limit": s.o.RateLimit, "window": s.o.RateWindow.String()})
	}
	out := make([]tasks.Task, 0, len(items))
	for _, it := range items {
		title := "Unduh " + displayName(it.u, it.name) + " ke " + string(dest)
		payload := Payload{URL: it.u.String(), Dest: string(dest), Filename: it.name, Policy: req.Policy}
		t, err := s.o.Tasks.Enqueue(ctx, tasks.EnqueueRequest{UserID: req.UserID, Type: TaskType, Title: title, Payload: payload, Locks: []string{string(dest)}})
		if err != nil {
			return out, err
		}
		s.logger.InfoContext(ctx, "remote download queued", slog.String("task", t.ID), slog.String("host", netguard.SafeHost(it.u)), slog.String("dest", string(dest)))
		out = append(out, t)
	}
	return out, nil
}

func displayName(u *url.URL, explicit string) string {
	if explicit != "" {
		return explicit
	}
	if seg := lastSegment(u); seg != "" {
		return seg
	}
	return u.Host
}

func lastSegment(u *url.URL) string {
	p := strings.TrimSuffix(u.Path, "/")
	if p == "" {
		return ""
	}
	seg := path.Base(p)
	if unescaped, err := url.PathUnescape(seg); err == nil {
		seg = unescaped
	}
	if seg == "." || seg == "/" || seg == ".." {
		return ""
	}
	return seg
}

// windowLimiter counts tasks per user over a sliding window; reserve takes
// n slots atomically so a batch either fits or is refused as a whole.
type windowLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	now    func() time.Time
	events map[string][]time.Time
}

func newWindowLimiter(maxEvents int, window time.Duration, now func() time.Time) *windowLimiter {
	return &windowLimiter{max: maxEvents, window: window, now: now, events: map[string][]time.Time{}}
}

func (w *windowLimiter) reserve(key string, n int) (bool, time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	cutoff := now.Add(-w.window)
	ev := w.events[key][:0]
	for _, t := range w.events[key] {
		if t.After(cutoff) {
			ev = append(ev, t)
		}
	}
	if len(ev)+n > w.max {
		w.events[key] = ev
		if len(ev) == 0 {
			return false, w.window
		}
		return false, ev[0].Add(w.window).Sub(now).Round(time.Second) + time.Second
	}
	for i := 0; i < n; i++ {
		ev = append(ev, now)
	}
	w.events[key] = ev
	return true, 0
}

// CleanOrphans removes working folders older than OrphanAge (abandoned by
// a crash, or kept for a retry that never came). Hourly job.
func (s *Service) CleanOrphans(ctx context.Context) (int, error) {
	if s.o.DataDir == "" {
		return 0, nil
	}
	tmp, err := fsjail.OpenSys(s.o.DataDir, "tmp")
	if err != nil {
		return 0, err
	}
	defer func() { _ = tmp.Close() }()
	names, err := tmp.ReadDirNames(".")
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, name := range names {
		if ctx.Err() != nil {
			return removed, ctx.Err()
		}
		if !strings.HasPrefix(name, TmpPrefix) {
			continue
		}
		info, err := tmp.Lstat(name)
		if err != nil || !info.IsDir() {
			continue
		}
		if s.now().Sub(info.ModTime()) < OrphanAge {
			continue
		}
		if err := tmp.RemoveAll(name); err == nil {
			removed++
		}
	}
	return removed, nil
}
