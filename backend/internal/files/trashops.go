package files

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/trash"
)

// RestoreRequest restores Recycle Bin items (POST /trash/restore).
type RestoreRequest struct {
	IDs     []string
	Dest    fsjail.VPath // folder to restore into; "" = each item's original location
	Default trash.Policy
	PerItem map[string]trash.Policy // keyed by trash id
}

// RestoreOutcome is the response of a restore batch.
type RestoreOutcome struct {
	Results []trash.RestoreResult `json:"results"`
	// ReactivateShares lists shares disabled by the deletion that the client
	// may offer to re-enable; always empty until Step 18a.
	ReactivateShares []string `json:"reactivateShares"`
}

// RestoreConflictError carries every destination that is occupied under the
// fail policy (API: 409 ALREADY_EXISTS details.conflicts[]).
type RestoreConflictError struct {
	Conflicts []trash.ConflictError
}

func (e *RestoreConflictError) Error() string {
	return fmt.Sprintf("files: %d restore destination(s) already exist", len(e.Conflicts))
}

func (r RestoreRequest) policy(id string) trash.Policy {
	if p, ok := r.PerItem[id]; ok && p != "" {
		return p
	}
	if r.Default == "" {
		return trash.PolicyFail
	}
	return r.Default
}

// RestoreFromTrash brings items back (SPEC §8.6 "Restore"). Conflicts are
// collected before anything moves so the client can answer them in one
// dialog; then each item is restored under a write lock on its destination,
// with activity, change hooks and listing invalidation.
func (s *Service) RestoreFromTrash(ctx context.Context, j *fsjail.Jail, userID string, req RestoreRequest) (*RestoreOutcome, error) {
	if s.trash == nil {
		return nil, errors.New("files: trash store not configured")
	}
	if len(req.IDs) == 0 {
		return nil, &ValidationError{Reason: "empty", Fields: map[string]string{"ids": "required"}}
	}
	if len(req.IDs) > MaxBatchItems {
		return nil, &ValidationError{Reason: "too_many", Fields: map[string]string{"ids": "max " + strconv.Itoa(MaxBatchItems)}}
	}
	if req.Dest != "" {
		info, err := j.Stat(req.Dest)
		if err != nil {
			return nil, err
		}
		if !info.IsDir {
			return nil, &ValidationError{Reason: "not_a_directory", Path: string(req.Dest), Fields: map[string]string{"to": "not_a_directory"}}
		}
	}
	type planned struct {
		row trash.Item
		dst fsjail.VPath
	}
	var plan []planned
	var conflicts []trash.ConflictError
	seen := map[string]bool{}
	for _, id := range req.IDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		row, err := s.trash.Get(ctx, userID, id)
		if err != nil {
			return nil, err
		}
		var dst fsjail.VPath
		if req.Dest != "" {
			dst, err = fsjail.Join(req.Dest, row.Name)
		} else {
			dst, err = fsjail.Clean(row.OriginalPath)
		}
		if err != nil {
			return nil, err
		}
		if existing, lerr := j.Lstat(dst); lerr == nil && req.policy(id) == trash.PolicyFail {
			conflicts = append(conflicts, trash.ConflictError{ID: id, Path: string(dst), Source: id,
				Existing: trash.Stat{Size: existing.Size, Mtime: iso(existing.ModTime), IsDir: existing.IsDir},
				Incoming: trash.Stat{Size: row.SizeBytes, IsDir: row.IsDir != 0, Mtime: row.DeletedAt}})
		}
		plan = append(plan, planned{row: trash.Item{ID: row.ID, Name: row.Name, OriginalPath: row.OriginalPath, IsDir: row.IsDir != 0, Size: row.SizeBytes}, dst: dst})
	}
	if len(conflicts) > 0 {
		return nil, &RestoreConflictError{Conflicts: conflicts}
	}
	out := &RestoreOutcome{Results: []trash.RestoreResult{}, ReactivateShares: []string{}}
	for _, p := range plan {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		release, err := s.lock(userID, "restore", fsjail.Write, p.dst)
		if err != nil {
			return out, err
		}
		s.hook("restore")
		r, rerr := s.trash.Restore(ctx, j, userID, trash.RestoreRequest{ID: p.row.ID, Dest: p.dst, Policy: req.policy(p.row.ID)})
		release()
		if rerr != nil {
			return out, rerr
		}
		if r.Status != "skipped" {
			s.logActivity(ctx, s.repo, userID, ActRestore, string(p.dst), p.row.OriginalPath, map[string]any{"trashId": p.row.ID, "status": r.Status, "size": r.Size, "isDir": r.IsDir}, "")
			s.changed(ctx, userID, p.dst, r.IsDir)
			if r.To != "" && fsjail.VPath(r.To) != p.dst {
				s.invalidateDir(userID, fsjail.VPath(r.To).Dir())
			}
		}
		out.Results = append(out.Results, r)
	}
	return out, nil
}

// PurgeOutcome is the response of purge/empty.
type PurgeOutcome struct {
	Results    []trash.PurgeResult `json:"results"`
	Deleted    int                 `json:"deleted"`
	BytesFreed int64               `json:"bytesFreed"`
}

// PurgeTrash deletes Recycle Bin items for good (SPEC §8.6 "Hapus permanen").
func (s *Service) PurgeTrash(ctx context.Context, j *fsjail.Jail, userID string, ids []string) (*PurgeOutcome, error) {
	if s.trash == nil {
		return nil, errors.New("files: trash store not configured")
	}
	if len(ids) == 0 {
		return nil, &ValidationError{Reason: "empty", Fields: map[string]string{"ids": "required"}}
	}
	if len(ids) > MaxBatchItems {
		return nil, &ValidationError{Reason: "too_many", Fields: map[string]string{"ids": "max " + strconv.Itoa(MaxBatchItems)}}
	}
	results, freed, err := s.trash.Purge(ctx, j, userID, ids)
	out := &PurgeOutcome{Results: results, BytesFreed: freed}
	if out.Results == nil {
		out.Results = []trash.PurgeResult{}
	}
	batchID := ""
	for _, r := range results {
		if r.Status != "ok" {
			continue
		}
		if batchID == "" {
			batchID = r.ID
		}
		out.Deleted++
		s.logActivity(ctx, s.repo, userID, ActPurge, r.OriginalPath, "", map[string]any{"trashId": r.ID, "size": r.Size, "isDir": r.IsDir}, batchID)
	}
	return out, err
}

// EmptyTrash purges everything (SPEC §8.6 "Kosongkan").
func (s *Service) EmptyTrash(ctx context.Context, j *fsjail.Jail, userID string) (*PurgeOutcome, error) {
	if s.trash == nil {
		return nil, errors.New("files: trash store not configured")
	}
	count, freed, err := s.trash.Empty(ctx, j, userID)
	out := &PurgeOutcome{Results: []trash.PurgeResult{}, Deleted: count, BytesFreed: freed}
	if count > 0 {
		s.logActivity(ctx, s.repo, userID, ActEmpty, "", "", map[string]any{"items": count, "bytesFreed": freed}, "")
	}
	return out, err
}

func iso(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }
