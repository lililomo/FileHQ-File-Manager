// Package upload integrates the tus resumable upload protocol (tusd v2 as a
// library, SPEC §6.5, §8.8, ADR-03): pre-create validation and quota
// reservation, finalisation into the user's jail, precheck, and the hourly
// cleanup of abandoned uploads. The HTTP routing, session and CSRF gates live
// in httpapi; this package only sees a Principal in the request context.
package upload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tus/tusd/v2/pkg/filestore"
	"github.com/tus/tusd/v2/pkg/handler"
	"github.com/tus/tusd/v2/pkg/memorylocker"
	expslog "golang.org/x/exp/slog"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/quota"
)

const (
	// BasePath is the tus endpoint below APP_BASE_URL.
	BasePath = "/api/v1/upload/tus/"
	// SysDir is the DATA_DIR subfolder holding partial uploads.
	SysDir = "uploads"
	// DefaultSessionTTL is how long an upload may stay untouched before the
	// cleanup job drops it (SPEC §6.5 "24 h").
	DefaultSessionTTL = 24 * time.Hour
	// FinalRetention keeps finished/aborted rows for diagnostics.
	FinalRetention = 7 * 24 * time.Hour
	// MaxPrecheckFiles bounds one precheck request (the client batches per
	// 200, SPEC §8.8).
	MaxPrecheckFiles = 1000
	// MaxRelativeDepth bounds folder uploads.
	MaxRelativeDepth = 32
	// DefaultPolicy applies when the client sends no onConflict: it never
	// destroys data (SPEC §8.8 precheck asks the user first).
	DefaultPolicy = files.PolicyRename
)

// Session states.
const (
	StateActive    = "active"
	StateFinalized = "finalized"
	StateAborted   = "aborted"
	StateExpired   = "expired"
)

// Principal is the authenticated user attached to the request context by
// httpapi before the tus handler runs.
type Principal struct {
	UserID   string
	Username string
}

type ctxKey int

const ctxPrincipal ctxKey = 1

// WithPrincipal stores p in ctx.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, ctxPrincipal, p)
}

// PrincipalFrom returns the principal stored by WithPrincipal.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(ctxPrincipal).(Principal)
	return p, ok && p.UserID != ""
}

// OpenJailFunc opens the owner's root; the release func must be deferred.
type OpenJailFunc func(ctx context.Context, userID string) (*fsjail.Jail, func(), error)

// Options configures the Service.
type Options struct {
	DataDir       string
	MaxUploadSize int64
	ChunkSize     int64
	// BaseURL is APP_BASE_URL (no trailing slash); the tus Location header is
	// absolute and built from it (SPEC §6.5).
	BaseURL  string
	CrossFS  bool
	Repo     db.Repository
	Quota    *quota.Service
	Files    *files.Service
	Locks    *fsjail.LockManager
	OpenJail OpenJailFunc
	// Invalidate drops the listing cache of a folder.
	Invalidate func(userID string, dir fsjail.VPath)
	// OnChange is the thumbnail/media invalidation hook.
	OnChange func(ctx context.Context, userID string, path fsjail.VPath, isDir bool)
	// Publish sends an SSE event to the user (file.changed).
	Publish func(userID, name string, data any)
	// RequestID returns the request id for error envelopes ("" when none).
	RequestID  func(ctx context.Context) string
	Logger     *slog.Logger
	Now        func() time.Time
	SessionTTL time.Duration
}

// Service owns the tusd handler and the upload sessions.
type Service struct {
	o       Options
	tus     *handler.UnroutedHandler
	store   filestore.FileStore
	logger  *slog.Logger
	now     func() time.Time
	ttl     time.Duration
	crossFS bool
}

// New builds the Service and the tusd handler (filestore in
// DATA_DIR/uploads, in-memory upload locks, CORS disabled: same origin).
func New(o Options) (*Service, error) {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.SessionTTL <= 0 {
		o.SessionTTL = DefaultSessionTTL
	}
	if o.Repo == nil || o.OpenJail == nil {
		return nil, errors.New("upload: Repo and OpenJail are required")
	}
	if o.Locks == nil {
		o.Locks = fsjail.NewLockManager()
	}
	s := &Service{o: o, logger: o.Logger, now: o.Now, ttl: o.SessionTTL, crossFS: o.CrossFS}
	dir := filepath.Join(o.DataDir, SysDir)
	if _, err := fsjail.OpenSys(o.DataDir, SysDir); err != nil { // creates the folder (0750)
		return nil, err
	}
	s.store = filestore.New(dir)
	s.store.DirModePerm = 0o750
	s.store.FileModePerm = 0o640
	composer := handler.NewStoreComposer()
	s.store.UseIn(composer)
	memorylocker.New().UseIn(composer)
	h, err := handler.NewUnroutedHandler(handler.Config{
		StoreComposer:              composer,
		MaxSize:                    o.MaxUploadSize,
		BasePath:                   strings.TrimRight(o.BaseURL, "/") + BasePath,
		RespectForwardedHeaders:    true,
		DisableDownload:            true,
		DisableConcatenation:       true,
		Cors:                       &handler.CorsConfig{Disable: true},
		Logger:                     expslog.New(&levelFilter{h: o.Logger.Handler(), min: slog.LevelWarn}),
		PreUploadCreateCallback:    s.preCreate,
		PreFinishResponseCallback:  s.preFinish,
		PreUploadTerminateCallback: s.preTerminate,
	})
	if err != nil {
		return nil, fmt.Errorf("upload: tus handler: %w", err)
	}
	s.tus = h
	if o.Quota != nil {
		o.Quota.AddReservedSource(s.ActiveReserved)
	}
	return s, nil
}

// Tus exposes the tusd handler for routing (PostFile, HeadFile, PatchFile,
// DelFile wrapped in Middleware).
func (s *Service) Tus() *handler.UnroutedHandler { return s.tus }

// ChunkSize is UPLOAD_CHUNK_SIZE (0 = unlimited).
func (s *Service) ChunkSize() int64 { return s.o.ChunkSize }

// ActiveReserved is the durable quota reservation: Σ size of active
// sessions (SPEC §4.2).
func (s *Service) ActiveReserved(ctx context.Context, userID string) (int64, error) {
	return s.o.Repo.SumActiveUploadSizes(ctx, userID)
}

// OwnedActive loads an active session of userID (ErrNotFound otherwise):
// HEAD/PATCH/DELETE on another user's upload id answer 404 (SPEC §8.1).
func (s *Service) OwnedActive(ctx context.Context, userID, id string) (bool, error) {
	row, err := s.o.Repo.GetUserUploadSession(ctx, userID, id)
	if err != nil {
		if db.IsNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return row.State == StateActive, nil
}

// levelFilter bridges tusd's logger (golang.org/x/exp/slog, still a
// distinct type in v2.10) to the application's log/slog handler and drops
// its per-request Info lines (RequestIncoming/ResponseOutgoing) that the
// access log already covers.
type levelFilter struct {
	h   slog.Handler
	min slog.Level
}

func (f *levelFilter) Enabled(ctx context.Context, l expslog.Level) bool {
	return slog.Level(l) >= f.min && f.h.Enabled(ctx, slog.Level(l))
}

func (f *levelFilter) Handle(ctx context.Context, r expslog.Record) error {
	rec := slog.NewRecord(r.Time, slog.Level(r.Level), "tus: "+r.Message, r.PC)
	r.Attrs(func(a expslog.Attr) bool {
		rec.AddAttrs(slog.Any(a.Key, a.Value.Any()))
		return true
	})
	return f.h.Handle(ctx, rec)
}

func (f *levelFilter) WithAttrs(attrs []expslog.Attr) expslog.Handler {
	std := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		std = append(std, slog.Any(a.Key, a.Value.Any()))
	}
	return &levelFilter{h: f.h.WithAttrs(std), min: f.min}
}

func (f *levelFilter) WithGroup(n string) expslog.Handler {
	return &levelFilter{h: f.h.WithGroup(n), min: f.min}
}

// Error is an API error produced by the hooks (code/status as SPEC §6.2).
type Error struct {
	Code    string
	Status  int
	Message string
	Details map[string]any
	// terminal marks failures after which the partial upload is useless
	// (destination gone, type mismatch): the files are dropped.
	terminal bool
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func newErr(code string, status int, msg string, details map[string]any) *Error {
	if details == nil {
		details = map[string]any{}
	}
	return &Error{Code: code, Status: status, Message: msg, Details: details}
}

// Error codes used by this package (strings of httpapi.ErrorCode).
const (
	CodeUnauthenticated     = "UNAUTHENTICATED"
	CodeValidationFailed    = "VALIDATION_FAILED"
	CodeNameInvalid         = "NAME_INVALID"
	CodePathInvalid         = "PATH_INVALID"
	CodeNotFound            = "NOT_FOUND"
	CodeUnsupportedType     = "UNSUPPORTED_TYPE"
	CodeQuotaExceeded       = "QUOTA_EXCEEDED"
	CodePayloadTooLarge     = "PAYLOAD_TOO_LARGE"
	CodeAlreadyExists       = "ALREADY_EXISTS"
	CodeOperationInProgress = "OPERATION_IN_PROGRESS"
	CodeConflict            = "CONFLICT"
	CodeInternal            = "INTERNAL"
)

// tusError converts e into the tusd error carrying our JSON envelope.
func (s *Service) tusError(ctx context.Context, e *Error) handler.Error {
	details := map[string]any{}
	for k, v := range e.Details {
		details[k] = v
	}
	if s.o.RequestID != nil {
		if id := s.o.RequestID(ctx); id != "" {
			details["requestId"] = id
		}
	}
	body, _ := json.Marshal(map[string]any{"error": map[string]any{"code": e.Code, "message": e.Message, "details": details}})
	return handler.Error{ErrorCode: "ERR_" + e.Code, Message: e.Message, HTTPResponse: handler.HTTPResponse{
		StatusCode: e.Status, Body: string(body) + "\n",
		Header: handler.HTTPHeader{"Content-Type": "application/json; charset=utf-8", "Cache-Control": "no-store"},
	}}
}

// metadata is the parsed Upload-Metadata (SPEC §6.3: canonical keys
// filename, path, relativePath, onConflict, lastModified; the SPEC aliases
// name, dest, relPath, policy are accepted).
type metadata struct {
	Dest         fsjail.VPath
	Name         string
	RelPath      []string
	Policy       files.Policy
	LastModified *int64
	Warnings     []string
}

func pick(md handler.MetaData, keys ...string) string {
	for _, k := range keys {
		if v, ok := md[k]; ok && v != "" {
			return v
		}
	}
	return ""
}

// splitRelative validates a folder path relative to the destination
// ("a/b/c"; empty = none).
func splitRelative(rel string) ([]string, *Error) {
	rel = strings.Trim(strings.ReplaceAll(rel, "\\", "/"), "/")
	if rel == "" {
		return nil, nil
	}
	segs := strings.Split(rel, "/")
	if len(segs) > MaxRelativeDepth {
		return nil, newErr(CodeValidationFailed, http.StatusBadRequest, "folder upload too deep", map[string]any{"fields": map[string]string{"relativePath": "too_deep"}, "max": MaxRelativeDepth})
	}
	for _, seg := range segs {
		if _, err := fsjail.ValidateName(seg); err != nil {
			reason := "invalid"
			if code, d, ok := fsjail.APIError(err); ok && code == "NAME_INVALID" {
				if r, ok := d["reason"].(string); ok {
					reason = r
				}
			}
			return nil, newErr(CodeNameInvalid, http.StatusBadRequest, "invalid folder name in relativePath", map[string]any{"reason": reason, "name": seg, "fields": map[string]string{"relativePath": reason}})
		}
	}
	return segs, nil
}

func (s *Service) parseMetadata(md handler.MetaData) (metadata, *Error) {
	var m metadata
	name := pick(md, "filename", "name")
	if name == "" {
		return m, newErr(CodeValidationFailed, http.StatusBadRequest, "Upload-Metadata filename is required", map[string]any{"fields": map[string]string{"filename": "required"}})
	}
	warnings, err := fsjail.ValidateName(name)
	if err != nil {
		reason := "invalid"
		if _, d, ok := fsjail.APIError(err); ok {
			if r, ok := d["reason"].(string); ok {
				reason = r
			}
		}
		return m, newErr(CodeNameInvalid, http.StatusBadRequest, "invalid file name", map[string]any{"reason": reason, "name": name, "fields": map[string]string{"filename": reason}})
	}
	m.Name, m.Warnings = name, warnings
	dest, err := fsjail.Clean(pick(md, "path", "dest"))
	if err != nil {
		return m, newErr(CodePathInvalid, http.StatusBadRequest, "invalid destination path", map[string]any{"fields": map[string]string{"path": "invalid"}})
	}
	m.Dest = dest
	segs, perr := splitRelative(pick(md, "relativePath", "relPath"))
	if perr != nil {
		return m, perr
	}
	m.RelPath = segs
	policy := DefaultPolicy
	if raw := pick(md, "onConflict", "policy"); raw != "" {
		p, ok := files.ParsePolicy(raw)
		if !ok || p == files.PolicyFail {
			return m, newErr(CodeValidationFailed, http.StatusBadRequest, "onConflict must be overwrite, skip or rename", map[string]any{"fields": map[string]string{"onConflict": "invalid"}})
		}
		policy = p
	}
	m.Policy = policy
	if raw := pick(md, "lastModified"); raw != "" {
		ms, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || ms < 0 {
			return m, newErr(CodeValidationFailed, http.StatusBadRequest, "lastModified must be epoch milliseconds", map[string]any{"fields": map[string]string{"lastModified": "invalid"}})
		}
		m.LastModified = &ms
	}
	return m, nil
}

// targetDir is dest + relative segments.
func targetDir(dest fsjail.VPath, segs []string) (fsjail.VPath, error) {
	dir := dest
	for _, seg := range segs {
		next, err := fsjail.Join(dir, seg)
		if err != nil {
			return "", err
		}
		dir = next
	}
	return dir, nil
}

// blocked reports a BLOCKED_EXTENSIONS hit (SPEC §5.2).
func (s *Service) blocked(name string) bool {
	return s.o.Files != nil && s.o.Files.Blocked(name)
}

func (s *Service) sys() (*fsjail.SysDir, error) {
	return fsjail.OpenSys(s.o.DataDir, SysDir)
}

// dropFiles removes the partial upload and its .info (missing = fine).
func (s *Service) dropFiles(sys *fsjail.SysDir, id string) {
	for _, name := range []string{id, id + ".info"} {
		if err := sys.Remove(name); err != nil && !errors.Is(err, fsjail.ErrNotFound) {
			s.logger.Warn("upload cleanup failed", slog.String("id", id), slog.String("error", err.Error()))
		}
	}
}
