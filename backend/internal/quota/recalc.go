package quota

import (
	"context"
	"log/slog"
	"path"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/fsjail"
)

// DriftHook is called for every user whose recalculation corrected more than
// DriftThreshold (audit quota.drift, SPEC §8.2).
type DriftHook func(ctx context.Context, userID string, before, after int64)

// RecalculateAll walks every account (the nightly job and the admin
// maintenance action). Users whose home does not exist yet are skipped.
// Each user is bounded by perUser (0 = no limit); errors are logged, never
// fatal, so one broken home cannot stop the others.
func (s *Service) RecalculateAll(ctx context.Context, storageRoot, dataDir string, perUser time.Duration, onDrift DriftHook) (done, failed int) {
	users, err := s.repo.ListUsers(ctx, db.UserFilter{}, 100000, 0)
	if err != nil {
		s.logger.ErrorContext(ctx, "quota recalculation: list users", slog.String("error", err.Error()))
		return 0, 1
	}
	for _, u := range users {
		if ctx.Err() != nil {
			return done, failed
		}
		if u.HomeProvisionedAt == nil {
			continue
		}
		uctx := ctx
		cancel := func() {}
		if perUser > 0 {
			uctx, cancel = context.WithTimeout(ctx, perUser)
		}
		res, rerr := s.recalculateUser(uctx, storageRoot, dataDir, u.ID, u.Username)
		cancel()
		if rerr != nil {
			failed++
			s.logger.WarnContext(ctx, "quota recalculation failed", slog.String("user", u.Username), slog.String("error", rerr.Error()))
			continue
		}
		done++
		if res.Drifted && onDrift != nil {
			onDrift(ctx, u.ID, res.Before, res.After)
		}
	}
	return done, failed
}

// RecalculateUser recalculates one account by id (admin action, dirty flag).
func (s *Service) RecalculateUser(ctx context.Context, storageRoot, dataDir, userID string) (Result, error) {
	u, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return Result{}, err
	}
	return s.recalculateUser(ctx, storageRoot, dataDir, u.ID, u.Username)
}

func (s *Service) recalculateUser(ctx context.Context, storageRoot, dataDir, userID, username string) (Result, error) {
	j, err := fsjail.OpenUser(storageRoot, username, userID)
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = j.Close() }()
	trashDir, err := fsjail.OpenSys(dataDir, path.Join("trash", userID))
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = trashDir.Close() }()
	versionsDir, err := fsjail.OpenSys(dataDir, path.Join("versions", userID))
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = versionsDir.Close() }()
	return s.Recalculate(ctx, Target{UserID: userID, Jail: j, Trash: trashDir, Versions: versionsDir})
}
