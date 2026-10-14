package files

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
)

// Activity actions written by this package (SPEC §4.2 activity_logs).
const (
	ActRename      = "rename"
	ActCopy        = "copy"
	ActMove        = "move"
	ActDuplicate   = "duplicate"
	ActSymlink     = "symlink"
	ActChmod       = "chmod"
	ActDelete      = "delete"
	ActBatchRename = "batch_rename"
	ActUndo        = "undo"
	ActRestore     = "restore"
	ActPurge       = "purge"
	ActPurgeAuto   = "purge_auto"
	ActEmpty       = "trash_empty"
	ActPermanent   = "delete_permanent"
	ActDirSize     = "dir_size"
	ActChecksum    = "checksum"
	ActOpen        = "open"
	ActCreate      = "create"
	ActEdit        = "edit"
	ActUpload      = "upload"
	ActMkdir       = "mkdir"
	ActDownload    = "download"
	ActRemoteDL    = "remote_download"
)

// RecentActions feed the Recent page (SPEC §8.7): opened, edited, uploaded,
// created or restored items, 30 days back, one entry per path.
var RecentActions = []string{ActOpen, ActEdit, ActUpload, ActCreate, ActRestore}

// RecentWindow bounds the Recent derivation.
const RecentWindow = 30 * 24 * time.Hour

// MaxRecent is the upper bound of GET /me/recent?limit.
const MaxRecent = 200

// MaxActivityPage bounds GET /me/activity?limit.
const MaxActivityPage = 200

// RecordActivity writes one row from outside the package (handlers: create,
// mkdir, open; later upload/edit).
func (s *Service) RecordActivity(ctx context.Context, userID, action, p, p2 string, details map[string]any) {
	s.logActivity(ctx, s.repo, userID, action, p, p2, details, "")
}

// RecentEntry is one deduplicated Recent row before the handler stats it.
type RecentEntry struct {
	Path   string
	Action string
	At     string
}

// Recent returns up to limit distinct paths touched by RecentActions in the
// last RecentWindow, newest first. Existence is checked by the caller (it
// owns the jail); the scan reads at most 10×limit rows so a burst of events
// on one path cannot hide older files.
func (s *Service) Recent(ctx context.Context, userID string, limit int) ([]RecentEntry, error) {
	if limit <= 0 || limit > MaxRecent {
		limit = MaxRecent
	}
	since := db.FormatTime(s.now().Add(-RecentWindow))
	rows, err := s.repo.ListRecentActivity(ctx, userID, since, int64(limit*10))
	if err != nil {
		return nil, err
	}
	seen := make(map[string]struct{}, limit)
	out := make([]RecentEntry, 0, limit)
	for _, r := range rows {
		if r.Path == nil {
			continue
		}
		if _, ok := seen[*r.Path]; ok {
			continue
		}
		seen[*r.Path] = struct{}{}
		out = append(out, RecentEntry{Path: *r.Path, Action: r.Action, At: r.At})
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// ActivityEntry is one row of GET /me/activity.
type ActivityEntry struct {
	ID      int64          `json:"id"`
	At      string         `json:"at"`
	Action  string         `json:"action"`
	Path    *string        `json:"path"`
	Path2   *string        `json:"path2"`
	Details map[string]any `json:"details"`
	BatchID *string        `json:"batchId"`
}

// ActivityPage is a cursor page of the user's journal.
type ActivityPage struct {
	Items      []ActivityEntry `json:"items"`
	NextCursor *string         `json:"nextCursor"`
}

// ListActivity pages the journal newest first (cursor = id of the last row).
func (s *Service) ListActivity(ctx context.Context, userID string, f db.ActivityFilter) (*ActivityPage, error) {
	if f.Limit <= 0 || f.Limit > MaxActivityPage {
		f.Limit = 100
	}
	rows, err := s.repo.ListActivityFiltered(ctx, userID, db.ActivityFilter{
		Action: f.Action, From: f.From, To: f.To, Query: f.Query, BeforeID: f.BeforeID, Limit: f.Limit + 1,
	})
	if err != nil {
		return nil, err
	}
	page := &ActivityPage{Items: make([]ActivityEntry, 0, len(rows))}
	more := int64(len(rows)) > f.Limit
	if more {
		rows = rows[:f.Limit]
	}
	for _, r := range rows {
		e := ActivityEntry{ID: r.ID, At: r.At, Action: r.Action, Path: r.Path, Path2: r.Path2, BatchID: r.BatchID, Details: map[string]any{}}
		if r.DetailsJson != nil {
			_ = json.Unmarshal([]byte(*r.DetailsJson), &e.Details)
		}
		page.Items = append(page.Items, e)
	}
	if more && len(page.Items) > 0 {
		c := strconv.FormatInt(page.Items[len(page.Items)-1].ID, 10)
		page.NextCursor = &c
	}
	return page, nil
}

// ClearActivity deletes the user's whole journal ("Hapus riwayat saya";
// audit rows are untouched).
func (s *Service) ClearActivity(ctx context.Context, userID string) (int64, error) {
	return s.repo.DeleteActivityForUser(ctx, userID)
}

// PruneActivity deletes rows older than retention (daily job).
func (s *Service) PruneActivity(ctx context.Context, retention time.Duration) (int64, error) {
	return s.repo.DeleteActivityOlderThan(ctx, db.FormatTime(s.now().Add(-retention)))
}

// logActivity writes one activity row through repo (which may be a
// transaction). Failures are logged, never fatal for the file operation.
func (s *Service) logActivity(ctx context.Context, repo db.Repository, userID, action string, p, p2 string, details map[string]any, batchID string) {
	params := sqlc.InsertActivityParams{UserID: userID, At: db.FormatTime(s.now()), Action: action}
	if p != "" {
		params.Path = db.Ptr(p)
	}
	if p2 != "" {
		params.Path2 = db.Ptr(p2)
	}
	if batchID != "" {
		params.BatchID = db.Ptr(batchID)
	}
	if len(details) > 0 {
		if raw, err := json.Marshal(details); err == nil {
			params.DetailsJson = db.Ptr(string(raw))
		}
	}
	if _, err := repo.InsertActivity(ctx, params); err != nil {
		s.logger.WarnContext(ctx, "activity insert failed", slog.String("action", action), slog.String("error", err.Error()))
	}
}
