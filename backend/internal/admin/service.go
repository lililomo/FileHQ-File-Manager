// Package admin implements the administration features of SPEC §8.17:
// dashboard aggregates, user management (create, update, reset password,
// revoke sessions, disable, delete/archive), live settings overrides on top
// of the environment and branding. HTTP handlers live in httpapi/admin_*.go.
package admin

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/auth"
	"kppbwi.app/filemanager/internal/config"
	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/tasks"
)

// BuildInfo is what `filemanager version` prints (dashboard "versi aplikasi").
type BuildInfo struct {
	Version   string
	Commit    string
	BuildDate string
}

// Options wires the service.
type Options struct {
	Cfg    *config.Config
	Repo   db.Repository
	Auth   *auth.Service
	Audit  *audit.Recorder
	Quota  *quota.Service
	Tasks  *tasks.Engine // delete_user task; nil = synchronous deletion (tests)
	Build  BuildInfo
	Logger *slog.Logger
	Now    func() time.Time
	// OnSettingsChanged runs after a new snapshot is installed (caches of
	// share/remotedl settings are dropped so the change is visible at once).
	OnSettingsChanged func(rt *config.Runtime)
	// OnUserChanged runs after a user row changed status/role/was deleted
	// (jail cache eviction, listing cache).
	OnUserChanged func(userID string)
	// Deps is the dependency table shown on the dashboard; computed once at
	// start (SPEC §8.17 "versi dari --version saat start, di-cache").
	Deps []config.ToolStatus
	// ArchiveCaps reports 7-Zip password handling (dashboard warning).
	ArchiveCaps func() (sevenZip bool, passwordStdin bool)
}

// Service is the admin domain service.
type Service struct {
	cfg    *config.Config
	repo   db.Repository
	auth   *auth.Service
	audit  *audit.Recorder
	quota  *quota.Service
	tasks  *tasks.Engine
	build  BuildInfo
	logger *slog.Logger
	now    func() time.Time
	o      Options
	deps   []config.ToolStatus
}

// New builds the service and installs the effective settings snapshot from
// the settings table (overrides persisted by earlier runs apply at start).
func New(ctx context.Context, o Options) (*Service, error) {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	s := &Service{cfg: o.Cfg, repo: o.Repo, auth: o.Auth, audit: o.Audit, quota: o.Quota, tasks: o.Tasks, build: o.Build,
		logger: o.Logger, now: o.Now, o: o, deps: o.Deps}
	if _, err := s.reloadSnapshot(ctx); err != nil {
		return nil, err
	}
	if s.tasks != nil {
		s.tasks.Register(TaskDeleteUser, tasks.Spec{Unit: tasks.UnitItems, Run: s.runDeleteUser})
	}
	return s, nil
}

// Error is an admin failure with its API mapping (same shape as share.Error).
type Error struct {
	Code    string
	Status  int
	Message string
	Details map[string]any
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func newErr(code string, status int, msg string, details map[string]any) *Error {
	return &Error{Code: code, Status: status, Message: msg, Details: details}
}

// fieldErr is a 400 VALIDATION_FAILED with details.fields[field]=reason.
func fieldErr(field, reason, msg string) *Error {
	return newErr("VALIDATION_FAILED", http.StatusBadRequest, msg, map[string]any{"fields": map[string]any{field: reason}})
}

// keyErr is a 400 VALIDATION_FAILED for one settings key (details.key).
func keyErr(key, reason, msg string) *Error {
	return newErr("VALIDATION_FAILED", http.StatusBadRequest, msg, map[string]any{"key": key, "reason": reason, "fields": map[string]any{key: reason}})
}

func internalErr(err error) *Error {
	return newErr("INTERNAL", http.StatusInternalServerError, "internal error: "+err.Error(), nil)
}

// Actor is the admin performing an action (audit).
type Actor struct {
	ID        string
	Username  string
	IP        string
	UserAgent string
}

func (s *Service) record(ctx context.Context, repo db.AuditRepo, a Actor, action, targetType, targetID string, details map[string]any) {
	if s.audit == nil {
		return
	}
	e := audit.Entry{ActorID: a.ID, ActorUsername: a.Username, Action: action, TargetType: targetType, TargetID: targetID, IP: a.IP, UserAgent: a.UserAgent, Details: details}
	if repo != nil {
		s.audit.RecordWith(ctx, repo, e)
		return
	}
	s.audit.Record(ctx, e)
}
