package files

import (
	"context"
	"fmt"
	"log/slog"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/quota"
)

// Copy copies paths into folder to (SPEC §8.5 "Copy / Move"). Metadata
// (notes, tags, favorites) is not copied. The copied items can be undone
// (moved to the Recycle Bin).
func (s *Service) Copy(ctx context.Context, j *fsjail.Jail, userID string, paths []fsjail.VPath, to fsjail.VPath, pol ConflictPolicy) (*Result, error) {
	return s.copy(ctx, j, userID, paths, to, pol, ActCopy)
}

func (s *Service) copy(ctx context.Context, j *fsjail.Jail, userID string, paths []fsjail.VPath, to fsjail.VPath, pol ConflictPolicy, action string) (*Result, error) {
	items, err := s.plan(j, paths, to, pol, ActCopy)
	if err != nil {
		return nil, err
	}
	srcs := make([]fsjail.VPath, 0, len(items))
	dsts := make([]fsjail.VPath, 0, len(items))
	for _, it := range items {
		srcs = append(srcs, it.src)
		dsts = append(dsts, it.dst)
	}
	// Read locks on sources, write locks on destinations. A destination
	// inside a source (copy /a into /a/b is rejected by plan) cannot occur.
	releaseR, err := s.lock(userID, action, fsjail.Read, srcs...)
	if err != nil {
		return nil, err
	}
	defer releaseR()
	releaseW, err := s.lock(userID, action, fsjail.Write, dsts...)
	if err != nil {
		return nil, err
	}
	defer releaseW()
	s.hook("copy")
	_, bytes, err := s.estimate(ctx, j, srcs)
	if err != nil {
		return nil, err
	}
	// Quota: reserve the estimated bytes before writing (SPEC §8.2
	// "copy/duplicate"); the real delta per item is committed afterwards.
	var rsv *quota.Reservation
	if s.quota != nil {
		rsv, err = s.quota.Reserve(ctx, j, userID, bytes)
		if err != nil {
			return nil, err
		}
	}
	var applied int64
	incomplete := false
	defer func() {
		if rsv != nil {
			if cerr := rsv.Commit(ctx, nil, applied); cerr != nil {
				s.logger.ErrorContext(ctx, "quota commit failed", slog.String("user", userID), slog.String("error", cerr.Error()))
			}
		}
		if incomplete {
			s.markQuotaDirty(ctx, userID)
		}
	}()
	res := &Result{Results: []ItemResult{}}
	var created []string
	for _, it := range items {
		r := ItemResult{Path: string(it.src), IsDir: it.info.IsDir}
		r.Status = decide(it)
		if r.Status == "skipped" {
			res.Results = append(res.Results, r)
			continue
		}
		dst := it.dst
		switch r.Status {
		case "renamed":
			if !it.renamed {
				dst, err = UniqueName(j, to, it.src.Base(), it.info.IsDir)
				if err != nil {
					return res, err
				}
			}
		case "type_mismatch":
			return res, &ValidationError{Reason: "type_mismatch", Path: string(dst), Fields: map[string]string{"on_conflict": "cannot overwrite a folder with a file or vice versa"}}
		}
		r.To = string(dst)
		var before int64
		if r.Status == "merged" || r.Status == "overwritten" {
			var ok bool
			before, ok = s.sizeOf(ctx, j, dst)
			incomplete = incomplete || !ok
		}
		if err := s.copyOne(ctx, j, userID, it, dst, r.Status); err != nil {
			return res, err
		}
		after, ok := s.sizeOf(ctx, j, dst)
		incomplete = incomplete || !ok
		applied += after - before
		s.logActivity(ctx, s.repo, userID, action, string(it.src), string(dst), map[string]any{"op": action, "status": r.Status, "isDir": it.info.IsDir, "size": after}, "")
		if r.Status == "overwritten" || r.Status == "merged" {
			s.changed(ctx, userID, dst, it.info.IsDir)
		}
		s.invalidateDir(userID, to)
		s.dirSizes.invalidate(userID, dst)
		res.Results = append(res.Results, r)
		if r.Status == "ok" || r.Status == "renamed" {
			created = append(created, string(dst))
		}
	}
	if len(created) > 0 {
		s.withUndo(res, userID, UndoInfo{Kind: "delete_created", Created: created})
	}
	return res, nil
}

// copyOne performs a single planned copy; a fresh destination is removed
// again when the copy fails or ctx is cancelled (no partial results).
func (s *Service) copyOne(ctx context.Context, j *fsjail.Jail, userID string, it plannedItem, dst fsjail.VPath, status string) error {
	switch status {
	case "merged":
		return s.mergeCopy(ctx, j, userID, it.src, dst, it.policy)
	case "overwritten":
		// file over file: write to a temporary name, then rename over
		tmp := fsjail.VPath(string(dst) + ".fm-partial-" + db.NewID()[:8])
		if err := j.CopyFile(ctx, it.src, tmp, nil); err != nil {
			_ = j.Remove(tmp)
			return err
		}
		if err := j.Rename(tmp, dst); err != nil {
			_ = j.Remove(tmp)
			return err
		}
		return s.repo.WithTx(ctx, func(tx db.Repository) error {
			return DeleteReferences(ctx, tx, userID, string(dst))
		})
	}
	var err error
	if it.info.IsDir {
		err = j.CopyTree(ctx, it.src, dst, nil)
	} else {
		err = j.CopyFile(ctx, it.src, dst, nil)
	}
	if err != nil {
		if rerr := j.RemoveAll(dst); rerr != nil && !isNotFound(rerr) {
			s.logger.WarnContext(ctx, "partial copy cleanup failed", slog.String("path", string(dst)), slog.String("error", rerr.Error()))
		}
		return err
	}
	return nil
}

// mergeCopy copies src's entries into the existing folder dst with the
// per-file policy (overwrite replaces, skip keeps, rename adds "(n)").
func (s *Service) mergeCopy(ctx context.Context, j *fsjail.Jail, userID string, src, dst fsjail.VPath, pol Policy) error {
	entries, err := j.ReadDir(src)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		from := e.Path
		target, err := fsjail.Join(dst, e.Name)
		if err != nil {
			return err
		}
		existing, lerr := j.Lstat(target)
		status := "ok"
		switch {
		case lerr != nil && !isNotFound(lerr):
			return lerr
		case lerr == nil && e.IsDir && existing.IsDir:
			if err := s.mergeCopy(ctx, j, userID, from, target, pol); err != nil {
				return err
			}
			continue
		case lerr == nil && pol == PolicySkip:
			continue
		case lerr == nil && pol == PolicyRename:
			target, err = UniqueName(j, dst, e.Name, e.IsDir)
			if err != nil {
				return err
			}
		case lerr == nil && e.IsDir != existing.IsDir:
			return &ValidationError{Reason: "type_mismatch", Path: string(target)}
		case lerr == nil:
			status = "overwritten"
		}
		if err := s.copyOne(ctx, j, userID, plannedItem{src: from, dst: target, info: e}, target, status); err != nil {
			return fmt.Errorf("files: merge %s: %w", from, err)
		}
		if status == "overwritten" {
			s.changed(ctx, userID, target, false)
		}
	}
	return nil
}
