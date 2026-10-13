// Package audit appends security-relevant events to audit_logs (SPEC §8.16).
// Recording never fails the calling request: errors are logged instead.
package audit

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"kppbwi.app/filemanager/internal/config"
	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Action names (SPEC §8.16). Keep in sync with the admin audit filter UI.
const (
	ActionSeedAdmin       = "system.seed_admin"
	ActionLogin           = "auth.login"
	ActionLoginFailed     = "auth.login_failed"
	ActionAccountLocked   = "auth.account_locked"
	ActionLogout          = "auth.logout"
	ActionPasswordChanged = "auth.password_changed"
	ActionHomeProvisioned = "user.home_provisioned"
	ActionHomeReused      = "user.home_reused"
	ActionRevokeSessions  = "user.revoke_sessions"
)

// Entry is one audit event. Details must never contain secrets; keys that
// look sensitive are dropped defensively.
type Entry struct {
	ActorID       string
	ActorUsername string
	Action        string
	TargetType    string
	TargetID      string
	IP            string
	UserAgent     string
	Details       map[string]any
}

// Recorder writes entries through an AuditRepo.
type Recorder struct {
	repo   db.AuditRepo
	logger *slog.Logger
	now    func() time.Time
}

// New builds a Recorder. now is injectable for tests.
func New(repo db.AuditRepo, logger *slog.Logger, now func() time.Time) *Recorder {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Recorder{repo: repo, logger: logger, now: now}
}

// Record appends e using the recorder's repository.
func (r *Recorder) Record(ctx context.Context, e Entry) {
	r.RecordWith(ctx, r.repo, e)
}

// RecordWith appends e using repo (a transaction-bound repository when the
// event must be atomic with the change it describes).
func (r *Recorder) RecordWith(ctx context.Context, repo db.AuditRepo, e Entry) {
	p := sqlc.InsertAuditParams{
		At:        db.FormatTime(r.now()),
		Action:    e.Action,
		Ip:        orDash(e.IP),
		UserAgent: truncate(orDash(e.UserAgent), 256), // audit_logs.user_agent <= 256 (SPEC §4.2)
	}
	if e.ActorID != "" {
		p.ActorUserID = db.Ptr(e.ActorID)
	}
	if e.ActorUsername != "" {
		p.ActorUsername = db.Ptr(e.ActorUsername)
	}
	if e.TargetType != "" {
		p.TargetType = db.Ptr(e.TargetType)
	}
	if e.TargetID != "" {
		p.TargetID = db.Ptr(e.TargetID)
	}
	if len(e.Details) > 0 {
		clean := make(map[string]any, len(e.Details))
		for k, v := range e.Details {
			if config.IsSensitiveKey(k) {
				continue
			}
			clean[k] = v
		}
		if raw, err := json.Marshal(clean); err == nil {
			p.DetailsJson = db.Ptr(string(raw))
		}
	}
	if _, err := repo.InsertAudit(ctx, p); err != nil {
		r.logger.ErrorContext(ctx, "audit write failed",
			slog.String("action", e.Action), slog.String("error", err.Error()))
	}
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
