package files

import (
	"context"
	"errors"
	"strconv"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/fsjail"
)

// PermanentConfirmThreshold is the batch size above which the client must
// send confirm:"HAPUS" (SPEC §8.5 "Hapus permanen").
const PermanentConfirmThreshold = 50

// PermanentConfirmWord is the confirmation word.
const PermanentConfirmWord = "HAPUS"

// PermanentResult extends Result with the bytes freed.
type PermanentResult struct {
	Result
	Deleted    int   `json:"deleted"`
	BytesFreed int64 `json:"bytesFreed"`
}

// DeletePermanent removes paths without the Recycle Bin (Shift+Delete):
// RemoveAll, every reference and version row, quota −size, activity
// delete_permanent. The audit row is written by the HTTP layer (it knows
// the actor). Not undoable.
func (s *Service) DeletePermanent(ctx context.Context, j *fsjail.Jail, userID string, paths []fsjail.VPath) (*PermanentResult, error) {
	if len(paths) == 0 {
		return nil, &ValidationError{Reason: "empty", Fields: map[string]string{"paths": "required"}}
	}
	if len(paths) > MaxBatchItems {
		return nil, &ValidationError{Reason: "too_many", Fields: map[string]string{"paths": "max " + strconv.Itoa(MaxBatchItems)}}
	}
	paths = dropDescendants(dedupe(paths))
	for _, p := range paths {
		if p.IsRoot() {
			return nil, &ValidationError{Reason: "root", Path: string(p), Fields: map[string]string{"paths": "root"}}
		}
	}
	release, err := s.lock(userID, "delete_permanent", fsjail.Write, paths...)
	if err != nil {
		return nil, err
	}
	defer release()
	s.hook("delete_permanent")
	batchID := db.NewID()
	res := &PermanentResult{Result: Result{Results: []ItemResult{}, BatchID: batchID}}
	incomplete := false
	defer func() {
		if incomplete {
			s.markQuotaDirty(ctx, userID)
		}
	}()
	for _, p := range paths {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		info, err := j.Lstat(p)
		if err != nil {
			if isNotFound(err) {
				res.Results = append(res.Results, ItemResult{Path: string(p), Status: "skipped", Warnings: []string{"not_found"}})
				continue
			}
			return res, err
		}
		bytes, count, complete := s.measure(ctx, j, info)
		incomplete = incomplete || !complete
		if err := j.RemoveAll(p); err != nil && !isNotFound(err) {
			return res, err
		}
		if err := s.repo.WithTx(ctx, func(tx db.Repository) error {
			if err := DeleteReferences(ctx, tx, userID, string(p)); err != nil {
				return err
			}
			if s.quota != nil {
				if err := s.quota.Apply(ctx, tx, userID, -bytes, 0, 0); err != nil {
					return err
				}
			}
			s.logActivity(ctx, tx, userID, ActPermanent, string(p), "", map[string]any{"size": bytes, "items": count, "isDir": info.IsDir}, batchID)
			return nil
		}); err != nil {
			incomplete = true
			return res, errors.Join(err, errors.New("files: item removed but bookkeeping failed; quota marked dirty"))
		}
		s.changed(ctx, userID, p, info.IsDir)
		res.Results = append(res.Results, ItemResult{Path: string(p), Status: "ok", IsDir: info.IsDir})
		res.Deleted++
		res.BytesFreed += bytes
	}
	res.Undo = nil
	return res, nil
}
