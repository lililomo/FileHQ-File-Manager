package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/scheduler"
	"kppbwi.app/filemanager/internal/tasks"
	"kppbwi.app/filemanager/internal/thumbs"
	"kppbwi.app/filemanager/internal/trash"
)

// Maintenance (Step 19b, SPEC §8.17 "Maintenance", §7.1, §7.3, §12.6):
// every action is a system task (user_id NULL) of the §7.1 type, started by
// the central schedule below or by an admin (POST /admin/maintenance/{job}),
// so progress, cancel and the JSON result use the task engine. A job never
// runs twice at once: the scheduler guards its own slots and a new run is
// refused while a task of the same type is queued or running.

// Job names = task types (SPEC §7.1).
const (
	JobRecalcQuota    = "recalc_quota"
	JobClearThumbs    = "clear_thumbs"
	JobCleanUploads   = "clean_uploads"
	JobPurgeTrash     = "purge_trash"
	JobIntegrityCheck = "integrity_check"
	JobBackupDB       = "backup_db"
	JobPruneLogs      = "prune_logs"
)

// Backups (SPEC §12.6).
const (
	BackupDir  = "backups"
	BackupKeep = 7
	// IntegrityListCap bounds each list of the integrity report.
	IntegrityListCap = 500
)

var (
	backupNameRe  = regexp.MustCompile(`^app-(\d{8}-\d{6})(?:-([a-z0-9][a-z0-9-]{0,31}))?\.db$`)
	backupLabelRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
)

// Reporter is the slice of tasks.Runtime a job uses.
type Reporter interface {
	Progress(done, total int64)
	Log(level, format string, args ...any)
}

type nopReporter struct{}

func (nopReporter) Progress(int64, int64)      {}
func (nopReporter) Log(string, string, ...any) {}

// JobParams is the task payload (no secrets).
type JobParams struct {
	UserID    string `json:"userId,omitempty"`
	Force     bool   `json:"force,omitempty"`
	Prune     bool   `json:"prune,omitempty"`
	Label     string `json:"label,omitempty"`
	Scheduled bool   `json:"scheduled,omitempty"`
}

// RunInput is the body of POST /admin/maintenance/{job}.
type RunInput struct {
	UserID  string `json:"userId"`
	Force   bool   `json:"force"`
	Confirm string `json:"confirm"`
	Prune   bool   `json:"prune"`
	Label   string `json:"label"`
}

type jobDef struct {
	name, slug, title string
	at                string
	every             time.Duration
	timeout           time.Duration
	run               func(m *Maintenance, ctx context.Context, rep Reporter, p JobParams) (any, error)
}

// jobs is the central schedule (SPEC §7.3, times in TZ_DEFAULT).
var jobs = []jobDef{
	{name: JobBackupDB, slug: "backup-db", title: "Backup database", at: "02:30", timeout: 30 * time.Minute, run: (*Maintenance).runBackup},
	{name: JobPurgeTrash, slug: "purge-trash", title: "Purge expired Recycle Bin items", at: "03:00", timeout: time.Hour, run: (*Maintenance).runPurgeTrash},
	{name: JobRecalcQuota, slug: "recalc-quota", title: "Recalculate quota", at: "03:30", timeout: time.Hour, run: (*Maintenance).runRecalcQuota},
	{name: JobPruneLogs, slug: "prune-logs", title: "Prune logs", at: "04:00", timeout: 15 * time.Minute, run: (*Maintenance).runPruneLogs},
	{name: JobClearThumbs, slug: "clear-thumbs", title: "Thumbnail cache", at: "Sun 05:00", timeout: time.Hour, run: (*Maintenance).runClearThumbs},
	{name: JobCleanUploads, slug: "clean-uploads", title: "Clean abandoned uploads", every: time.Hour, timeout: 15 * time.Minute, run: (*Maintenance).runCleanUploads},
	{name: JobIntegrityCheck, slug: "integrity-check", title: "Integrity check", every: 6 * time.Hour, timeout: time.Hour, run: (*Maintenance).runIntegrity},
}

// JitterMax spreads scheduled starts (SPEC §7.3 "jitter").
const JitterMax = 2 * time.Minute

func defBySlug(slug string) (jobDef, bool) {
	for _, d := range jobs {
		if d.slug == slug || d.name == slug {
			return d, true
		}
	}
	return jobDef{}, false
}

// ThumbCache is the thumbnail cache (thumbs.Service).
type ThumbCache interface {
	Clear(ctx context.Context, userID string) (thumbs.CacheStats, error)
	Prune(ctx context.Context, budget int64) (thumbs.CacheStats, error)
}

// MaintenanceOptions wires the jobs to the services that do the work.
type MaintenanceOptions struct {
	DB     *db.DB // VACUUM INTO
	Trash  *trash.Store
	Thumbs ThumbCache
	// ThumbBudget is the LRU limit of the weekly prune (0 = 2 GB).
	ThumbBudget int64
	// CleanUploads removes abandoned tus uploads, remote download parts and
	// archive workspaces (Step 12a/13/14a).
	CleanUploads func(ctx context.Context) (any, error)
	// PruneActivity / PruneShareAccess delete rows older than the cutoff.
	PruneActivity    func(ctx context.Context, older time.Duration) (int64, error)
	PruneShareAccess func(ctx context.Context, older time.Duration) (int64, error)
	// OnQuotaDrift records quota.drift (audit) — set by New.
	Scheduler *scheduler.Scheduler
}

// Maintenance runs the jobs.
type Maintenance struct {
	s  *Service
	o  MaintenanceOptions
	mu sync.Mutex // check-then-enqueue of one job
}

// EnableMaintenance registers the job task types and the schedule. Call
// once at start (main) or in tests.
func (s *Service) EnableMaintenance(o MaintenanceOptions) (*Maintenance, error) {
	if o.ThumbBudget <= 0 {
		o.ThumbBudget = thumbs.DefaultCacheBudget
	}
	m := &Maintenance{s: s, o: o}
	s.maint = m
	for _, d := range jobs {
		if s.tasks != nil {
			s.tasks.Register(d.name, tasks.Spec{Unit: tasks.UnitItems, Run: m.taskRunner(d)})
		}
		if o.Scheduler != nil {
			if err := o.Scheduler.Add(scheduler.Job{Name: d.name, At: d.at, Every: d.every, Jitter: JitterMax, Timeout: d.timeout,
				Run: m.scheduledRun(d)}); err != nil {
				return nil, err
			}
		}
	}
	return m, nil
}

func (m *Maintenance) taskRunner(d jobDef) tasks.Runner {
	return func(ctx context.Context, rt *tasks.Runtime) (any, error) {
		var p JobParams
		if len(rt.Task.Payload) > 0 {
			if err := json.Unmarshal(rt.Task.Payload, &p); err != nil {
				return nil, fmt.Errorf("maintenance: payload: %w", err)
			}
		}
		return d.run(m, ctx, rt, p)
	}
}

// scheduledRun enqueues the job as a system task and waits for it so the
// scheduler's "never overlapping" covers the task too. Without a task
// engine (CLI, tests) the job runs inline.
func (m *Maintenance) scheduledRun(d jobDef) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		p := JobParams{Scheduled: true}
		if m.s.tasks == nil {
			_, err := d.run(m, ctx, nopReporter{}, p)
			return err
		}
		t, err := m.enqueue(ctx, d, p)
		if err != nil {
			var ae *Error
			if errors.As(err, &ae) && ae.Status == http.StatusConflict {
				return nil // an admin run is in progress: this slot is covered
			}
			return err
		}
		if err := m.s.tasks.Wait(ctx, t.ID); err != nil {
			return err
		}
		final, err := m.s.tasks.GetAny(context.WithoutCancel(ctx), t.ID)
		if err != nil {
			return err
		}
		if final.State != tasks.Done {
			msg := string(final.State)
			if final.Error != nil {
				msg += ": " + final.Error.Code + " " + final.Error.Message
			}
			return errors.New(msg)
		}
		return nil
	}
}

// enqueue starts d unless a task of that type is queued or running.
func (m *Maintenance) enqueue(ctx context.Context, d jobDef, p JobParams) (tasks.Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	active, err := m.s.repo.ListActiveTasksOfType(ctx, d.name)
	if err != nil {
		return tasks.Task{}, internalErr(err)
	}
	if len(active) > 0 {
		return tasks.Task{}, newErr("CONFLICT", http.StatusConflict, "this maintenance job is already queued or running",
			map[string]any{"reason": "already_running", "taskId": active[0].ID})
	}
	t, err := m.s.tasks.Enqueue(ctx, tasks.EnqueueRequest{Type: d.name, Title: d.title, Payload: p})
	if err != nil {
		return tasks.Task{}, internalErr(err)
	}
	return t, nil
}

// Run validates an admin request and starts the job (202 {task}).
func (s *Service) RunMaintenance(ctx context.Context, a Actor, slug string, in RunInput) (*tasks.Task, error) {
	m := s.maint
	d, ok := defBySlug(slug)
	if m == nil || !ok {
		return nil, newErr("NOT_FOUND", http.StatusNotFound, "unknown maintenance job", map[string]any{"job": slug})
	}
	if s.tasks == nil {
		return nil, newErr("INTERNAL", http.StatusInternalServerError, "task engine unavailable", nil)
	}
	p := JobParams{}
	if in.UserID != "" {
		if d.name != JobRecalcQuota && d.name != JobClearThumbs {
			return nil, fieldErr("userId", "not_applicable", "this job runs for every user")
		}
		u, err := s.repo.GetUserByID(ctx, in.UserID)
		if err != nil || u.DeletedAt != nil {
			return nil, fieldErr("userId", "unknown", "unknown user")
		}
		p.UserID = u.ID
	}
	if in.Force {
		if d.name != JobPurgeTrash {
			return nil, fieldErr("force", "not_applicable", "only purge-trash can be forced")
		}
		if in.Confirm != "HAPUS" {
			return nil, newErr("VALIDATION_FAILED", http.StatusBadRequest, `type "HAPUS" to purge every Recycle Bin`,
				map[string]any{"fields": map[string]any{"confirm": "required"}, "word": "HAPUS"})
		}
		p.Force = true
	}
	if in.Prune {
		if d.name != JobClearThumbs || p.UserID != "" {
			return nil, fieldErr("prune", "not_applicable", "prune applies to the whole thumbnail cache")
		}
		p.Prune = true
	}
	if in.Label != "" {
		if d.name != JobBackupDB {
			return nil, fieldErr("label", "not_applicable", "only backups take a label")
		}
		if !backupLabelRe.MatchString(in.Label) {
			return nil, fieldErr("label", "invalid", "label: lowercase letters, digits and dashes (max 32)")
		}
		p.Label = in.Label
	}
	t, err := m.enqueue(ctx, d, p)
	if err != nil {
		return nil, err
	}
	s.record(ctx, nil, a, "maintenance."+d.name, "task", t.ID, map[string]any{"userId": p.UserID, "force": p.Force, "prune": p.Prune, "label": p.Label})
	return &t, nil
}

// JobView is one card of the maintenance page (GET /admin/maintenance).
type JobView struct {
	Name     string            `json:"name"`
	Slug     string            `json:"slug,omitempty"`
	Manual   bool              `json:"manual"`
	Schedule *scheduler.Status `json:"schedule,omitempty"`
	LastTask *tasks.Task       `json:"lastTask,omitempty"`
}

// MaintenanceView is GET /admin/maintenance.
type MaintenanceView struct {
	TimeZone string       `json:"timeZone"`
	Jobs     []JobView    `json:"jobs"`
	Backups  []BackupInfo `json:"backups"`
}

// MaintenanceStatus lists every scheduled job (maintenance jobs first,
// with their last task) and the backups.
func (s *Service) MaintenanceStatus(ctx context.Context) (*MaintenanceView, error) {
	m := s.maint
	out := &MaintenanceView{Jobs: []JobView{}, Backups: []BackupInfo{}}
	if m == nil {
		return out, nil
	}
	sched := map[string]scheduler.Status{}
	if m.o.Scheduler != nil {
		out.TimeZone = m.o.Scheduler.Location().String()
		for _, st := range m.o.Scheduler.Status() {
			sched[st.Name] = st
		}
	}
	seen := map[string]bool{}
	for _, d := range jobs {
		v := JobView{Name: d.name, Slug: d.slug, Manual: true}
		if st, ok := sched[d.name]; ok {
			v.Schedule = &st
		}
		if row, err := s.repo.LatestTaskOfType(ctx, d.name); err == nil && s.tasks != nil {
			if t, gerr := s.tasks.GetAny(ctx, row.ID); gerr == nil {
				v.LastTask = &t
			}
		}
		seen[d.name] = true
		out.Jobs = append(out.Jobs, v)
	}
	names := make([]string, 0, len(sched))
	for n := range sched {
		if !seen[n] {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, n := range names {
		st := sched[n]
		out.Jobs = append(out.Jobs, JobView{Name: n, Schedule: &st})
	}
	b, err := ListBackups(s.cfg.DataDir)
	if err != nil {
		return nil, internalErr(err)
	}
	out.Backups = b
	return out, nil
}

// ---- jobs -----------------------------------------------------------------

func (m *Maintenance) runRecalcQuota(ctx context.Context, rep Reporter, p JobParams) (any, error) {
	s := m.s
	if s.quota == nil {
		return nil, errors.New("maintenance: quota service unavailable")
	}
	var users []sqlc.User
	if p.UserID != "" {
		u, err := s.repo.GetUserByID(ctx, p.UserID)
		if err != nil {
			return nil, err
		}
		users = []sqlc.User{u}
	} else {
		all, err := s.repo.ListUsers(ctx, db.UserFilter{}, 100000, 0)
		if err != nil {
			return nil, err
		}
		users = all
	}
	type drift struct {
		Username string `json:"username"`
		Before   int64  `json:"before"`
		After    int64  `json:"after"`
	}
	res := struct {
		Users   int     `json:"users"`
		Skipped int     `json:"skipped"`
		Failed  int     `json:"failed"`
		Drifted []drift `json:"drifted"`
	}{Drifted: []drift{}}
	total := int64(len(users))
	for i, u := range users {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		rep.Progress(int64(i), total)
		if u.HomeProvisionedAt == nil {
			res.Skipped++
			continue
		}
		uctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		r, err := s.quota.RecalculateUser(uctx, s.cfg.StorageRoot, s.cfg.DataDir, u.ID)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			res.Failed++
			rep.Log("warn", "%s: %v", u.Username, err)
			continue
		}
		res.Users++
		if r.Drifted {
			s.record(ctx, nil, Actor{}, audit.ActionQuotaDrift, "user", u.ID, map[string]any{"before": r.Before, "after": r.After})
			if len(res.Drifted) < IntegrityListCap {
				res.Drifted = append(res.Drifted, drift{Username: u.Username, Before: r.Before, After: r.After})
			}
		}
	}
	rep.Progress(total, total)
	s.logger.InfoContext(ctx, "quota recalculated", slog.Int("users", res.Users), slog.Int("failed", res.Failed))
	return res, nil
}

func (m *Maintenance) runClearThumbs(ctx context.Context, rep Reporter, p JobParams) (any, error) {
	if m.o.Thumbs == nil {
		return nil, errors.New("maintenance: thumbnail cache unavailable")
	}
	rep.Progress(0, 1)
	var (
		st  thumbs.CacheStats
		err error
	)
	mode := "clear"
	if p.Scheduled || p.Prune {
		mode = "prune"
		st, err = m.o.Thumbs.Prune(ctx, m.o.ThumbBudget)
	} else {
		st, err = m.o.Thumbs.Clear(ctx, p.UserID)
	}
	if err != nil {
		return nil, err
	}
	rep.Progress(1, 1)
	return map[string]any{"mode": mode, "userId": p.UserID, "files": st.Files, "bytes": st.Bytes, "removedFiles": st.RemovedFiles,
		"freedBytes": st.FreedBytes, "budgetBytes": m.o.ThumbBudget}, nil
}

func (m *Maintenance) runCleanUploads(ctx context.Context, rep Reporter, _ JobParams) (any, error) {
	if m.o.CleanUploads == nil {
		return map[string]any{}, nil
	}
	rep.Progress(0, 1)
	res, err := m.o.CleanUploads(ctx)
	rep.Progress(1, 1)
	return res, err
}

func (m *Maintenance) runPurgeTrash(ctx context.Context, rep Reporter, p JobParams) (any, error) {
	ts := m.o.Trash
	if ts == nil {
		return nil, errors.New("maintenance: trash store unavailable")
	}
	if !p.Force {
		if m.s.cfg.Runtime().TrashRetentionDays == 0 {
			return map[string]any{"mode": "expired", "items": 0, "bytesFreed": 0, "retention": "forever"}, nil
		}
		rep.Progress(0, 1)
		n, freed, err := ts.PurgeExpired(ctx, 100)
		if err != nil {
			return nil, err
		}
		rep.Progress(1, 1)
		return map[string]any{"mode": "expired", "items": n, "bytesFreed": freed}, nil
	}
	users, err := m.s.repo.ListUsers(ctx, db.UserFilter{}, 100000, 0)
	if err != nil {
		return nil, err
	}
	items, freed := 0, int64(0)
	for i, u := range users {
		rep.Progress(int64(i), int64(len(users)))
		if u.HomeProvisionedAt == nil || ts.OpenJail == nil {
			continue
		}
		j, closeFn, err := ts.OpenJail(ctx, u.ID)
		if err != nil {
			rep.Log("warn", "%s: %v", u.Username, err)
			continue
		}
		n, f, err := ts.Empty(ctx, j, u.ID)
		closeFn()
		items += n
		freed += f
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			rep.Log("warn", "%s: %v", u.Username, err)
		}
	}
	rep.Progress(int64(len(users)), int64(len(users)))
	return map[string]any{"mode": "all", "items": items, "bytesFreed": freed}, nil
}

func (m *Maintenance) runPruneLogs(ctx context.Context, rep Reporter, _ JobParams) (any, error) {
	rt := m.s.cfg.Runtime()
	day := 24 * time.Hour
	res := map[string]any{"activityRetentionDays": rt.ActivityRetentionDays, "auditRetentionDays": rt.AuditRetentionDays}
	rep.Progress(0, 3)
	if m.o.PruneActivity != nil {
		n, err := m.o.PruneActivity(ctx, time.Duration(rt.ActivityRetentionDays)*day)
		if err != nil {
			return nil, err
		}
		res["activity"] = n
	}
	rep.Progress(1, 3)
	if m.o.PruneShareAccess != nil {
		n, err := m.o.PruneShareAccess(ctx, time.Duration(rt.ActivityRetentionDays)*day)
		if err != nil {
			return nil, err
		}
		res["shareAccess"] = n
	}
	rep.Progress(2, 3)
	n, err := m.s.repo.DeleteAuditOlderThan(ctx, db.FormatTime(m.s.now().Add(-time.Duration(rt.AuditRetentionDays)*day)))
	if err != nil {
		return nil, err
	}
	res["audit"] = n
	rep.Progress(3, 3)
	return res, nil
}

// ---- backup ---------------------------------------------------------------

// BackupInfo is one file of DATA_DIR/backups.
type BackupInfo struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"createdAt"`
	Label     string `json:"label,omitempty"`
}

func (m *Maintenance) runBackup(ctx context.Context, rep Reporter, p JobParams) (any, error) {
	if m.o.DB == nil {
		return nil, errors.New("maintenance: database handle unavailable")
	}
	rep.Progress(0, 1)
	info, pruned, err := BackupDB(ctx, m.o.DB, m.s.cfg.DataDir, m.s.cfg.TZDefault, m.s.now(), p.Label)
	if err != nil {
		return nil, err
	}
	rep.Progress(1, 1)
	return map[string]any{"backup": info, "pruned": pruned, "keep": BackupKeep}, nil
}

// BackupDB writes DATA_DIR/backups/app-<yyyyMMdd-HHmmss>[-label].db with
// VACUUM INTO (SPEC §12.6) and keeps the BackupKeep newest unlabelled
// backups; labelled (manual) backups are never removed automatically. It
// is also the `filemanager backup-db` command run before update.sh.
func BackupDB(ctx context.Context, d *db.DB, dataDir string, loc *time.Location, now time.Time, label string) (BackupInfo, []string, error) {
	if label != "" && !backupLabelRe.MatchString(label) {
		return BackupInfo{}, nil, fmt.Errorf("backup: invalid label %q", label)
	}
	if loc == nil {
		loc = time.UTC
	}
	sys, err := fsjail.OpenSys(dataDir, BackupDir)
	if err != nil {
		return BackupInfo{}, nil, err
	}
	defer func() { _ = sys.Close() }()
	host, err := sys.HostPath(".")
	if err != nil {
		return BackupInfo{}, nil, err
	}
	name := "app-" + now.In(loc).Format("20060102-150405")
	if label != "" {
		name += "-" + label
	}
	name += ".db"
	if err := d.Backup(ctx, filepath.Join(host, name)); err != nil {
		return BackupInfo{}, nil, err
	}
	fi, err := sys.Lstat(name)
	if err != nil {
		return BackupInfo{}, nil, err
	}
	info := BackupInfo{Name: name, Size: fi.Size(), CreatedAt: db.FormatTime(fi.ModTime()), Label: label}
	pruned, err := pruneBackups(sys)
	return info, pruned, err
}

func pruneBackups(sys *fsjail.SysDir) ([]string, error) {
	names, err := sys.ReadDirNames(".")
	if err != nil {
		return nil, err
	}
	var auto []string
	for _, n := range names {
		if mm := backupNameRe.FindStringSubmatch(n); mm != nil && mm[2] == "" {
			auto = append(auto, n)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(auto))) // newest first: the timestamp sorts lexically
	pruned := []string{}
	for _, n := range auto[min(len(auto), BackupKeep):] {
		if err := sys.Remove(n); err != nil && !errors.Is(err, fsjail.ErrNotFound) {
			return pruned, err
		}
		pruned = append(pruned, n)
	}
	return pruned, nil
}

// ListBackups lists DATA_DIR/backups newest first.
func ListBackups(dataDir string) ([]BackupInfo, error) {
	sys, err := fsjail.OpenSys(dataDir, BackupDir)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sys.Close() }()
	names, err := sys.ReadDirNames(".")
	if err != nil {
		return nil, err
	}
	out := []BackupInfo{}
	for _, n := range names {
		mm := backupNameRe.FindStringSubmatch(n)
		if mm == nil {
			continue
		}
		fi, err := sys.Lstat(n)
		if err != nil || !fi.Mode().IsRegular() {
			continue
		}
		out = append(out, BackupInfo{Name: n, Size: fi.Size(), CreatedAt: db.FormatTime(fi.ModTime()), Label: mm[2]})
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Name > out[k].Name })
	return out, nil
}

// OpenBackup opens one backup for download (admin, audited).
func (s *Service) OpenBackup(ctx context.Context, a Actor, name string) (*os.File, os.FileInfo, error) {
	if !backupNameRe.MatchString(name) {
		return nil, nil, newErr("NOT_FOUND", http.StatusNotFound, "backup not found", nil)
	}
	sys, err := fsjail.OpenSys(s.cfg.DataDir, BackupDir)
	if err != nil {
		return nil, nil, internalErr(err)
	}
	defer func() { _ = sys.Close() }()
	f, err := sys.Open(name)
	if err != nil {
		return nil, nil, newErr("NOT_FOUND", http.StatusNotFound, "backup not found", nil)
	}
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, nil, newErr("NOT_FOUND", http.StatusNotFound, "backup not found", nil)
	}
	s.record(ctx, nil, a, "maintenance.backup_download", "backup", name, map[string]any{"size": fi.Size()})
	return f, fi, nil
}

// ---- integrity ------------------------------------------------------------

// IntegrityReport is the result of integrity_check.
type IntegrityReport struct {
	CheckedAt        string           `json:"checkedAt"`
	OrphanFolders    []OrphanFolder   `json:"orphanFolders"`
	MissingHomes     []MissingHome    `json:"missingHomes"`
	BrokenRefs       []BrokenRef      `json:"brokenRefs"`
	OrphanSystemDirs []OrphanSysDir   `json:"orphanSystemDirs"`
	Counts           map[string]int64 `json:"counts"`
	Truncated        bool             `json:"truncated"`
}

// OrphanFolder is a STORAGE_ROOT entry without a live account.
type OrphanFolder struct {
	Name  string `json:"name"`
	IsDir bool   `json:"isDir"`
}

// MissingHome is an account whose home folder does not exist.
type MissingHome struct {
	UserID      string `json:"userId"`
	Username    string `json:"username"`
	Provisioned bool   `json:"provisioned"` // false: created on first login
}

// BrokenRef is a metadata row whose path is gone.
type BrokenRef struct {
	Table    string `json:"table"`
	UserID   string `json:"userId"`
	Username string `json:"username"`
	Path     string `json:"path"`
}

// OrphanSysDir is DATA_DIR/<area>/<uid> without an account.
type OrphanSysDir struct {
	Area string `json:"area"`
	Name string `json:"name"`
}

func (m *Maintenance) runIntegrity(ctx context.Context, rep Reporter, _ JobParams) (any, error) {
	return m.s.CheckIntegrity(ctx, rep)
}

// CheckIntegrity compares STORAGE_ROOT, DATA_DIR and the path-bearing tables
// with the accounts (read-only; SPEC §8.17 "integrity-check").
func (s *Service) CheckIntegrity(ctx context.Context, rep Reporter) (*IntegrityReport, error) {
	if rep == nil {
		rep = nopReporter{}
	}
	r := &IntegrityReport{CheckedAt: db.FormatTime(s.now()), OrphanFolders: []OrphanFolder{}, MissingHomes: []MissingHome{},
		BrokenRefs: []BrokenRef{}, OrphanSystemDirs: []OrphanSysDir{}, Counts: map[string]int64{}}
	users, err := s.repo.ListUsers(ctx, db.UserFilter{}, 100000, 0)
	if err != nil {
		return nil, err
	}
	byName := map[string]sqlc.User{}
	byID := map[string]sqlc.User{}
	for _, u := range users {
		byName[u.Username] = u
		byID[u.ID] = u
	}
	rep.Progress(0, 4)
	// 1. STORAGE_ROOT entries vs accounts
	homes, err := fsjail.ListHomes(s.cfg.StorageRoot)
	if err != nil {
		return nil, err
	}
	present := map[string]bool{}
	for _, h := range homes {
		present[h.Name] = true
		if _, ok := byName[h.Name]; !ok {
			r.Counts["orphanFolders"]++
			if len(r.OrphanFolders) < IntegrityListCap {
				r.OrphanFolders = append(r.OrphanFolders, OrphanFolder{Name: h.Name, IsDir: h.IsDir})
			} else {
				r.Truncated = true
			}
		}
	}
	for _, u := range users {
		if !present[u.Username] {
			r.Counts["missingHomes"]++
			if len(r.MissingHomes) < IntegrityListCap {
				r.MissingHomes = append(r.MissingHomes, MissingHome{UserID: u.ID, Username: u.Username, Provisioned: u.HomeProvisionedAt != nil})
			} else {
				r.Truncated = true
			}
		}
	}
	rep.Progress(1, 4)
	// 2. DATA_DIR/{trash,versions,thumbs}/<uid> without an account
	for _, area := range []string{"trash", "versions", "thumbs"} {
		sys, err := fsjail.OpenSys(s.cfg.DataDir, area)
		if err != nil {
			return nil, err
		}
		names, err := sys.ReadDirNames(".")
		_ = sys.Close()
		if err != nil {
			return nil, err
		}
		sort.Strings(names)
		for _, n := range names {
			if strings.HasPrefix(n, ".") {
				continue
			}
			if _, ok := byID[n]; !ok {
				r.Counts["orphanSystemDirs"]++
				if len(r.OrphanSystemDirs) < IntegrityListCap {
					r.OrphanSystemDirs = append(r.OrphanSystemDirs, OrphanSysDir{Area: area, Name: n})
				} else {
					r.Truncated = true
				}
			}
		}
	}
	rep.Progress(2, 4)
	// 3. path references whose target is gone
	jails := map[string]*fsjail.Jail{}
	defer func() {
		for _, j := range jails {
			if j != nil {
				_ = j.Close()
			}
		}
	}()
	var checked int64
	err = s.repo.EachPathRef(ctx, func(ref db.PathRef) error {
		if checked++; checked%100 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		r.Counts["refsChecked"]++
		u, ok := byID[ref.UserID]
		if !ok {
			return nil // soft-deleted account: rows go with its purge
		}
		j, opened := jails[u.ID]
		if !opened {
			if present[u.Username] {
				if oj, oerr := fsjail.OpenUser(s.cfg.StorageRoot, u.Username, u.ID); oerr == nil {
					j = oj
				}
			}
			jails[u.ID] = j
		}
		broken := j == nil
		if !broken {
			p, perr := fsjail.Clean(ref.Path)
			if perr != nil {
				broken = true
			} else if _, serr := j.Lstat(p); serr != nil && errors.Is(serr, fsjail.ErrNotFound) {
				broken = true
			}
		}
		if broken {
			r.Counts["brokenRefs"]++
			if len(r.BrokenRefs) < IntegrityListCap {
				r.BrokenRefs = append(r.BrokenRefs, BrokenRef{Table: ref.Table, UserID: u.ID, Username: u.Username, Path: ref.Path})
			} else {
				r.Truncated = true
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	rep.Progress(4, 4)
	return r, nil
}
