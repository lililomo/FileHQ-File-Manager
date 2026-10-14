package upload

import (
	"context"
	"log/slog"
	"strings"

	"kppbwi.app/filemanager/internal/db"
)

// CleanupResult reports one cleanup run.
type CleanupResult struct {
	Expired int // sessions past expires_at (files removed, quota released)
	Orphans int // files in DATA_DIR/uploads without an active session
	Pruned  int64
}

// CleanExpired is the hourly `clean_uploads` job (SPEC §6.5): active
// sessions older than the TTL are dropped together with their files, files
// without an active row (crash between tusd write and session insert,
// manual tampering) older than the TTL are removed, and finished rows older
// than FinalRetention are pruned.
func (s *Service) CleanExpired(ctx context.Context) (CleanupResult, error) {
	var res CleanupResult
	sys, err := s.sys()
	if err != nil {
		return res, err
	}
	defer func() { _ = sys.Close() }()
	now := s.now()
	for {
		rows, err := s.o.Repo.ListExpiredUploadSessions(ctx, db.FormatTime(now), 100)
		if err != nil {
			return res, err
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			if ctx.Err() != nil {
				return res, ctx.Err()
			}
			s.dropFiles(sys, row.ID)
			if _, err := s.o.Repo.SetUploadSessionState(ctx, row.ID, StateExpired, nil, db.FormatTime(now)); err != nil {
				return res, err
			}
			res.Expired++
		}
		if len(rows) < 100 {
			break
		}
	}
	active, err := s.o.Repo.ListActiveUploadIDs(ctx)
	if err != nil {
		return res, err
	}
	live := make(map[string]bool, len(active))
	for _, id := range active {
		live[id] = true
	}
	names, err := sys.ReadDirNames(".")
	if err != nil {
		return res, err
	}
	for _, name := range names {
		id := strings.TrimSuffix(name, ".info")
		if live[id] {
			continue
		}
		fi, err := sys.Lstat(name)
		if err != nil || !fi.Mode().IsRegular() || now.Sub(fi.ModTime()) < s.ttl {
			continue
		}
		if err := sys.Remove(name); err != nil {
			s.logger.WarnContext(ctx, "orphan upload not removed", slog.String("name", name), slog.String("error", err.Error()))
			continue
		}
		res.Orphans++
	}
	res.Pruned, err = s.o.Repo.DeleteUploadSessionsBefore(ctx, db.FormatTime(now.Add(-FinalRetention)))
	return res, err
}
