package share

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
)

// refUpdater keeps shares.path in step with rename/move and removes rows
// whose item was overwritten or deleted for good (SPEC §8.15 lifecycle,
// §5.7). It runs inside the file operation's transaction.
type refUpdater struct{ s *Service }

func (u refUpdater) Rewrite(ctx context.Context, tx db.Repository, userID, oldPath, newPath string) error {
	_, err := tx.RewriteSharesPath(ctx, userID, oldPath, newPath, db.FormatTime(u.s.now()))
	return err
}

func (u refUpdater) Delete(ctx context.Context, tx db.Repository, userID, path string) error {
	_, err := tx.DeleteSharesPrefix(ctx, userID, path)
	return err
}

var _ files.ReferenceUpdater = refUpdater{}

// RegisterRefs hooks the service into the file operations: path rewrites
// and permanent deletes through files.UpdateReferences/DeleteReferences,
// Recycle Bin through files.Service.TrashHook (set by the caller).
func (s *Service) RegisterRefs() {
	files.RegisterReferenceUpdater("shares", refUpdater{s: s})
}

// Trashed implements files.TrashHook: every share at path or below is
// disabled with reason item_trashed (the link answers SHARE_DISABLED).
func (s *Service) Trashed(ctx context.Context, userID, path string) {
	rows, err := s.repo.ListSharesUnderPath(ctx, userID, path)
	if err != nil {
		s.logger.WarnContext(ctx, "share trash hook: list failed", slog.String("error", err.Error()))
		return
	}
	now := db.FormatTime(s.now())
	reason := ReasonItemTrashed
	for _, row := range rows {
		if row.DisabledAt != nil {
			continue
		}
		if _, err := s.repo.SetShareDisabled(ctx, sqlc.SetShareDisabledParams{DisabledAt: &now, DisabledReason: &reason, UpdatedAt: now, ID: row.ID, OwnerUserID: userID}); err != nil {
			s.logger.WarnContext(ctx, "share trash hook: disable failed", slog.String("share", row.ID), slog.String("error", err.Error()))
		}
	}
}

// Restored implements files.TrashHook: shares disabled by the deletion of
// path (or of something above it) are reported so the client can offer
// "aktifkan kembali" (SPEC §8.15; POST /trash/restore reactivateShares).
// Nothing is re-enabled automatically.
func (s *Service) Restored(ctx context.Context, userID, path string) []string {
	rows, err := s.repo.ListSharesUnderPath(ctx, userID, path)
	if err != nil {
		s.logger.WarnContext(ctx, "share restore hook: list failed", slog.String("error", err.Error()))
		return nil
	}
	var ids []string
	for _, row := range rows {
		if row.DisabledAt != nil && row.DisabledReason != nil && *row.DisabledReason == ReasonItemTrashed {
			ids = append(ids, row.ID)
		}
	}
	return ids
}

var _ files.TrashHook = (*Service)(nil)

// OpenJailFunc opens the owner's jail for the broken-link check.
type OpenJailFunc func(ctx context.Context, userID string) (*fsjail.Jail, func(), error)

// CheckBroken walks every share and sets/clears broken_at depending on
// whether the item still exists (daily job; also done lazily on access).
// Returns how many rows changed.
func (s *Service) CheckBroken(ctx context.Context, open OpenJailFunc) (changed int, err error) {
	after := ""
	jails := map[string]*fsjail.Jail{}
	releases := []func(){}
	defer func() {
		for _, r := range releases {
			r()
		}
	}()
	for {
		if err := ctx.Err(); err != nil {
			return changed, err
		}
		rows, lerr := s.repo.ListSharesForCheck(ctx, after, 500)
		if lerr != nil {
			return changed, fmt.Errorf("share: check: %w", lerr)
		}
		if len(rows) == 0 {
			return changed, nil
		}
		for _, row := range rows {
			after = row.ID
			j, ok := jails[row.OwnerUserID]
			if !ok {
				var release func()
				var oerr error
				j, release, oerr = open(ctx, row.OwnerUserID)
				if oerr != nil {
					// owner home missing/unreadable: leave the row alone
					jails[row.OwnerUserID] = nil
					continue
				}
				jails[row.OwnerUserID] = j
				releases = append(releases, release)
			}
			if j == nil {
				continue
			}
			missing := false
			if p, perr := fsjail.Clean(row.Path); perr != nil {
				missing = true
			} else if _, lerr := j.Lstat(p); lerr != nil {
				var fe *fsjail.Error
				missing = errors.As(lerr, &fe) && fe.Code == fsjail.CodeNotFound
			}
			switch {
			case missing && row.BrokenAt == nil:
				now := db.FormatTime(s.now())
				if serr := s.repo.SetShareBroken(ctx, row.ID, &now); serr == nil {
					changed++
				}
			case !missing && row.BrokenAt != nil:
				if serr := s.repo.SetShareBroken(ctx, row.ID, nil); serr == nil {
					changed++
				}
			}
		}
		if len(rows) < 500 {
			return changed, nil
		}
	}
}

// MarkBroken is the lazy variant used by the public endpoints when the
// item is gone: broken_at is set (cleared again by rename/move or the job).
func (s *Service) MarkBroken(ctx context.Context, row sqlc.Share, missing bool) {
	switch {
	case missing && row.BrokenAt == nil:
		now := db.FormatTime(s.now())
		_ = s.repo.SetShareBroken(ctx, row.ID, &now)
	case !missing && row.BrokenAt != nil:
		_ = s.repo.SetShareBroken(ctx, row.ID, nil)
	}
}

// Retention helper for the prune job.
func Days(n int) time.Duration { return time.Duration(n) * 24 * time.Hour }
