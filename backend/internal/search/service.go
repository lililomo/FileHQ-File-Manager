package search

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	fmexec "kppbwi.app/filemanager/internal/exec"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/tasks"
	"kppbwi.app/filemanager/internal/versions"
)

// Limits (SPEC §8.12, §10.5).
const (
	MaxNameResults     = 5000
	NameTimeout        = 20 * time.Second
	NamePageDefault    = 500
	NamePageMax        = 1000
	NameCacheTTL       = 60 * time.Second
	MaxMatches         = 2000
	PerFileMatches     = 200
	ContentTimeout     = 30 * time.Second
	MaxLineChars       = 500
	MaxQueryLen        = 512
	MaxHistory         = 50
	NameRateLimit      = 30
	ContentRateLimit   = 10
	RateWindow         = time.Minute
	PreviewTTL         = 10 * time.Minute
	PreviewFullFiles   = 20
	PreviewHunksPerFil = 50
	FallbackWorkers    = 2
	BinaryProbeBytes   = 8 << 10
	ScanBufferBytes    = 1 << 20
)

// Task types (SPEC §7.1).
const (
	TaskPreview = "replace_preview"
	TaskApply   = "replace_apply"
)

// Options configure the service.
type Options struct {
	DataDir string
	// RgPath is RG_PATH ("" or missing binary → Go fallback).
	RgPath string
	Runner *fmexec.Runner
	// MaxFileSize bounds files read by content search (EDITOR_MAX_FILE_SIZE).
	MaxFileSize int64
	Repo        db.Repository
	Quota       *quota.Service
	Files       *files.Service
	Locks       *fsjail.LockManager
	Tasks       *tasks.Engine
	Versions    *versions.Store
	// Less orders name results (natural sort of the listing).
	Less func(a, b string) bool
	// TagPaths lists the paths carrying a tag colour (name search tag filter).
	TagPaths func(ctx context.Context, userID, color string) ([]string, error)
	// Invalidate drops the listing cache of a folder (replace apply).
	Invalidate func(userID string, dir fsjail.VPath)
	// OnChange is the thumbnail/media invalidation hook.
	OnChange func(ctx context.Context, userID string, path fsjail.VPath, isDir bool)
	Logger   *slog.Logger
	Now      func() time.Time
	// Engine forces "rg" or "go" (tests); "" = rg when available.
	Engine string
	// Workers of the Go fallback (default 2).
	Workers int
	// Timeouts/limits overridable by tests.
	NameTimeout    time.Duration
	ContentTimeout time.Duration
	NameRateLimit  int
	ContentRate    int
	RateWindow     time.Duration
}

// Service is the search service.
type Service struct {
	o      Options
	logger *slog.Logger

	nameLimiter    *windowLimiter
	contentLimiter *windowLimiter

	cacheMu sync.Mutex
	cache   map[string]*nameCache

	activeMu sync.Mutex
	active   map[string]*activeSearch

	previewMu sync.Mutex
	previews  map[string]*previewMeta
}

// New builds the service and registers the replace tasks.
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Workers <= 0 {
		o.Workers = FallbackWorkers
	}
	if o.NameTimeout <= 0 {
		o.NameTimeout = NameTimeout
	}
	if o.ContentTimeout <= 0 {
		o.ContentTimeout = ContentTimeout
	}
	if o.NameRateLimit <= 0 {
		o.NameRateLimit = NameRateLimit
	}
	if o.ContentRate <= 0 {
		o.ContentRate = ContentRateLimit
	}
	if o.RateWindow <= 0 {
		o.RateWindow = RateWindow
	}
	if o.MaxFileSize <= 0 {
		o.MaxFileSize = 5 << 20
	}
	if o.Less == nil {
		o.Less = func(a, b string) bool { return a < b }
	}
	s := &Service{o: o, logger: o.Logger,
		nameLimiter:    newWindowLimiter(o.NameRateLimit, o.RateWindow, o.Now),
		contentLimiter: newWindowLimiter(o.ContentRate, o.RateWindow, o.Now),
		cache:          map[string]*nameCache{}, active: map[string]*activeSearch{}, previews: map[string]*previewMeta{}}
	if o.Tasks != nil {
		o.Tasks.Register(TaskPreview, tasks.Spec{Unit: tasks.UnitItems, Run: s.runPreview})
		o.Tasks.Register(TaskApply, tasks.Spec{Unit: tasks.UnitItems, Run: s.runApply})
	}
	return s
}

// Engine reports which content engine is in use ("rg" | "go").
func (s *Service) Engine() string {
	if s.o.Engine == "go" {
		return "go"
	}
	if s.o.Engine == "rg" || (s.o.RgPath != "" && s.o.Runner != nil && s.o.Runner.Available(s.o.RgPath)) {
		return "rg"
	}
	return "go"
}

// --- rate limiter (sliding window per key) ---

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

// reserve records one event unless the window is full; retry says how
// long until the oldest event leaves the window.
func (w *windowLimiter) reserve(key string) (bool, time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()
	now := w.now()
	cut := now.Add(-w.window)
	ev := w.events[key][:0]
	for _, t := range w.events[key] {
		if t.After(cut) {
			ev = append(ev, t)
		}
	}
	if len(ev) >= w.max {
		w.events[key] = ev
		return false, ev[0].Sub(cut)
	}
	w.events[key] = append(ev, now)
	return true, 0
}

func rateErr(what string, retry time.Duration, limit int, window time.Duration) *Error {
	secs := int(retry.Seconds())
	if secs < 1 {
		secs = 1
	}
	return newErr(CodeRateLimited, "too many "+what+" searches; try again later", http.StatusTooManyRequests, map[string]any{"retryAfter": secs, "limit": limit, "window": window.String()})
}

// --- history (SPEC §4.2 search_history) ---

// HistoryEntry is the API view of a search_history row.
type HistoryEntry struct {
	ID      int64           `json:"id"`
	Kind    string          `json:"kind"`
	Query   string          `json:"query"`
	Path    string          `json:"path"`
	Filters json.RawMessage `json:"filters,omitempty"`
	At      string          `json:"at"`
}

func (s *Service) record(ctx context.Context, userID, kind, query, scope string, filters any) {
	if s.o.Repo == nil || query == "" {
		return
	}
	var fj *string
	if filters != nil {
		if raw, err := json.Marshal(filters); err == nil && string(raw) != "{}" && string(raw) != "null" {
			fj = db.Ptr(string(raw))
		}
	}
	if _, err := s.o.Repo.RecordSearch(ctx, sqlc.InsertSearchHistoryParams{UserID: userID, Kind: kind, Query: query, ScopePath: scope, FiltersJson: fj, At: db.FormatTime(s.o.Now())}, MaxHistory); err != nil {
		s.logger.WarnContext(ctx, "search history insert failed", slog.String("error", err.Error()))
	}
}

// History returns the newest entries (kind "" = both).
func (s *Service) History(ctx context.Context, userID, kind string) ([]HistoryEntry, error) {
	rows, err := s.o.Repo.ListSearchHistory(ctx, userID, kind, MaxHistory)
	if err != nil {
		return nil, err
	}
	out := make([]HistoryEntry, 0, len(rows))
	for _, r := range rows {
		e := HistoryEntry{ID: r.ID, Kind: r.Kind, Query: r.Query, Path: r.ScopePath, At: r.At}
		if r.FiltersJson != nil && json.Valid([]byte(*r.FiltersJson)) {
			e.Filters = json.RawMessage(*r.FiltersJson)
		}
		out = append(out, e)
	}
	return out, nil
}

// ClearHistory deletes the user's history.
func (s *Service) ClearHistory(ctx context.Context, userID string) error {
	_, err := s.o.Repo.DeleteSearchHistory(ctx, userID)
	return err
}

// --- shared helpers ---

func cleanScope(raw string) (fsjail.VPath, error) {
	if raw == "" {
		return fsjail.Root, nil
	}
	vp, err := fsjail.Clean(raw)
	if err != nil {
		return "", newErr(CodePathInvalid, "path is not valid", http.StatusBadRequest, map[string]any{"fields": map[string]any{"path": "invalid"}})
	}
	return vp, nil
}

// scopeFolder validates that vp is an existing folder (symlinked folders
// are refused: the walk never follows links).
func scopeFolder(j *fsjail.Jail, vp fsjail.VPath) error {
	info, err := j.Lstat(vp)
	if err != nil {
		if errors.Is(err, fsjail.ErrNotFound) {
			return newErr(CodeNotFound, "folder not found", http.StatusNotFound, map[string]any{"path": string(vp)})
		}
		return err
	}
	if !info.IsDir {
		return validationErr("path is not a folder", map[string]any{"path": "not_a_folder"})
	}
	return nil
}

// skipName hides reserved/staging entries from every walk.
func skipName(vp fsjail.VPath, name string) bool {
	if vp == fsjail.VPath("/"+fsjail.ReservedRootName) {
		return true
	}
	return files.IsTempName(name)
}
