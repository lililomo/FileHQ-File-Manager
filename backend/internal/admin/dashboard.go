package admin

import (
	"context"
	"log/slog"
	"runtime"
	"time"

	"golang.org/x/sys/unix"

	"kppbwi.app/filemanager/internal/config"
	"kppbwi.app/filemanager/internal/db"
)

// Dashboard is GET /admin/dashboard (SPEC §6.3, §8.17).
type Dashboard struct {
	Users           UsersCount  `json:"users"`
	Storage         StorageInfo `json:"storage"`
	TopUsers        []TopUser   `json:"topUsers"`
	Tasks           TasksCount  `json:"tasks"`
	PublicShares    int64       `json:"publicShares"`
	FailedLogins24h int64       `json:"failedLogins24h"`
	Version         VersionInfo `json:"version"`
	Deps            []DepStatus `json:"deps"`
	Warnings        []Warning   `json:"warnings"`
	GeneratedAt     string      `json:"generatedAt"`
}

// UsersCount splits accounts by status.
type UsersCount struct {
	Active   int64 `json:"active"`
	Disabled int64 `json:"disabled"`
}

// StorageInfo compares usage, quota sum and the real disk.
type StorageInfo struct {
	UsedBytes     int64    `json:"usedBytes"`
	QuotaSumBytes int64    `json:"quotaSumBytes"`
	Disk          DiskInfo `json:"disk"`
}

// DiskInfo is statfs(STORAGE_ROOT).
type DiskInfo struct {
	Total uint64 `json:"total"`
	Free  uint64 `json:"free"`
	Used  uint64 `json:"used"`
}

// TopUser is one row of the top-10 table.
type TopUser struct {
	ID          string `json:"id"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Used        int64  `json:"used"`
	Quota       int64  `json:"quota"`
}

// TasksCount is the engine state (every user).
type TasksCount struct {
	Running int64 `json:"running"`
	Queued  int64 `json:"queued"`
}

// VersionInfo is the build + Go runtime.
type VersionInfo struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildDate string `json:"buildDate"`
	Go        string `json:"go"`
}

// DepStatus mirrors `check-deps`.
type DepStatus struct {
	Name     string `json:"name"`
	Env      string `json:"env"`
	Path     string `json:"path"`
	Present  bool   `json:"ok"`
	Version  string `json:"version,omitempty"`
	Feature  string `json:"feature"`
	Optional bool   `json:"optional"`
}

// Warning is a configuration finding; code is stable for the UI, the
// details help the operator.
type Warning struct {
	Code    string         `json:"code"`
	Details map[string]any `json:"details,omitempty"`
}

// Warning codes.
const (
	WarnInitialPasswordInEnv = "initial_password_in_env"
	WarnCrossFSDataDir       = "cross_fs_data_dir"
	WarnSwapLow              = "swap_low"
	WarnDiskLow              = "disk_low"
	WarnQuotaOverDisk        = "quota_over_disk"
	WarnDependencyMissing    = "dependency_missing"
	WarnSevenZipPasswordArgv = "sevenzip_password_argv"
	WarnAdminBrowseEnabled   = "admin_browse_enabled"
)

// diskLowRatio is the "disk < 10 %" threshold.
const diskLowRatio = 0.10

// Statfs reports the filesystem holding STORAGE_ROOT (unix.Statfs is
// allowed here: the dashboard reads operator-level facts, not user files).
func Statfs(path string) (DiskInfo, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return DiskInfo{}, err
	}
	bs := uint64(max(st.Bsize, 0)) //nolint:gosec,unconvert // non-negative block size
	total := st.Blocks * bs
	free := st.Bavail * bs
	return DiskInfo{Total: total, Free: free, Used: total - st.Bfree*bs}, nil
}

// Dashboard aggregates the overview (SPEC §8.17 "Dashboard").
func (s *Service) Dashboard(ctx context.Context) (*Dashboard, error) {
	now := s.now()
	d := &Dashboard{GeneratedAt: db.FormatTime(now), TopUsers: []TopUser{}, Deps: []DepStatus{}, Warnings: []Warning{}}
	active, disabled := "active", "disabled"
	var err error
	if d.Users.Active, err = s.repo.CountUsers(ctx, db.UserFilter{Status: &active}); err != nil {
		return nil, internalErr(err)
	}
	if d.Users.Disabled, err = s.repo.CountUsers(ctx, db.UserFilter{Status: &disabled}); err != nil {
		return nil, internalErr(err)
	}
	if d.Storage.UsedBytes, err = s.repo.SumQuotaUsed(ctx); err != nil {
		return nil, internalErr(err)
	}
	if d.Storage.QuotaSumBytes, err = s.repo.SumUserQuota(ctx); err != nil {
		return nil, internalErr(err)
	}
	if disk, derr := Statfs(s.cfg.StorageRoot); derr == nil {
		d.Storage.Disk = disk
	} else {
		s.logger.WarnContext(ctx, "statfs failed", slog.String("error", derr.Error()))
	}
	top, err := s.repo.TopQuotaUsers(ctx, 10)
	if err != nil {
		return nil, internalErr(err)
	}
	for _, t := range top {
		d.TopUsers = append(d.TopUsers, TopUser{ID: t.ID, Username: t.Username, DisplayName: t.DisplayName, Used: t.UsedBytes, Quota: t.QuotaBytes})
	}
	if s.tasks != nil {
		counts, err := s.tasks.Counts(ctx)
		if err != nil {
			return nil, internalErr(err)
		}
		d.Tasks.Running, d.Tasks.Queued = counts["running"], counts["queued"]
	}
	if d.PublicShares, err = s.repo.CountActivePublicShares(ctx, db.FormatTime(now)); err != nil {
		return nil, internalErr(err)
	}
	if d.FailedLogins24h, err = s.repo.CountFailedLoginsSince(ctx, db.FormatTime(now.Add(-24*time.Hour))); err != nil {
		return nil, internalErr(err)
	}
	d.Version = VersionInfo{Version: s.build.Version, Commit: s.build.Commit, BuildDate: s.build.BuildDate, Go: runtime.Version()}
	for _, t := range s.deps {
		d.Deps = append(d.Deps, DepStatus{Name: t.Name, Env: t.EnvVar, Path: t.Path, Present: t.Present, Version: t.Version, Feature: t.Feature, Optional: t.Optional})
		if t.Path != "" && !t.Present {
			d.Warnings = append(d.Warnings, Warning{Code: WarnDependencyMissing, Details: map[string]any{"tool": t.Name, "path": t.Path, "feature": t.Feature}})
		}
	}
	d.Warnings = append(d.Warnings, s.configWarnings(d)...)
	return d, nil
}

// configWarnings evaluates the operator findings of SPEC §8.17.
func (s *Service) configWarnings(d *Dashboard) []Warning {
	var out []Warning
	if s.cfg.AdminInitialPassword != "" {
		out = append(out, Warning{Code: WarnInitialPasswordInEnv})
	}
	if s.cfg.CrossFSTrash {
		out = append(out, Warning{Code: WarnCrossFSDataDir, Details: map[string]any{"storageRoot": s.cfg.StorageRoot, "dataDir": s.cfg.DataDir}})
	}
	if swap, ok := config.SwapTotalBytes(); ok && swap < 1<<30 {
		out = append(out, Warning{Code: WarnSwapLow, Details: map[string]any{"swapTotal": swap}})
	}
	if d.Storage.Disk.Total > 0 {
		ratio := float64(d.Storage.Disk.Free) / float64(d.Storage.Disk.Total)
		if ratio < diskLowRatio {
			out = append(out, Warning{Code: WarnDiskLow, Details: map[string]any{"free": d.Storage.Disk.Free, "total": d.Storage.Disk.Total}})
		}
		if d.Storage.QuotaSumBytes > 0 && uint64(d.Storage.QuotaSumBytes) > d.Storage.Disk.Total { //nolint:gosec // non-negative sum
			out = append(out, Warning{Code: WarnQuotaOverDisk, Details: map[string]any{"quotaSum": d.Storage.QuotaSumBytes, "diskTotal": d.Storage.Disk.Total}})
		}
	}
	if s.o.ArchiveCaps != nil {
		if sevenZip, stdin := s.o.ArchiveCaps(); sevenZip && !stdin {
			out = append(out, Warning{Code: WarnSevenZipPasswordArgv})
		}
	}
	if s.cfg.AdminCanBrowseUsers {
		out = append(out, Warning{Code: WarnAdminBrowseEnabled})
	}
	return out
}

func slogStr(k, v string) slog.Attr { return slog.String(k, v) }
