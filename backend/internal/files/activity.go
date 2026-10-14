package files

import (
	"context"
	"encoding/json"
	"log/slog"

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
)

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
