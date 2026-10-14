// Package quota keeps the per-user storage accounting of SPEC §8.2 and
// ADR-23: incremental counters in quota_usage updated inside the same
// transaction as the mutation, reservations for operations that are about to
// write, enforcement against users.quota_bytes and the free disk space, and a
// full recalculation (walk) that corrects drift.
package quota

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"sync"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/fsjail"
)

// DefaultMinDiskFree is the free space every writer must leave on the
// storage filesystem (SPEC §8.2 "Disk penuh").
const DefaultMinDiskFree int64 = 512 << 20

// DriftThreshold is the relative difference between the incremental counters
// and a recalculation that is logged and audited (SPEC §8.2 "> 1 %").
const DriftThreshold = 0.01

// Usage is the API view of a user's quota (GET /me/quota).
type Usage struct {
	Limit        int64   `json:"quota"`
	Used         int64   `json:"used"`
	Trash        int64   `json:"trash"`
	Versions     int64   `json:"versions"`
	Reserved     int64   `json:"reserved"`
	Free         int64   `json:"free"`
	Percent      float64 `json:"percent"`
	FileCount    int64   `json:"fileCount"`
	DirCount     int64   `json:"dirCount"`
	Dirty        bool    `json:"dirty"`
	CalculatedAt *string `json:"calculatedAt"`
}

// Effective is what counts against the limit.
func (u Usage) Effective() int64 { return u.Used + u.Trash + u.Versions }

// ExceededError is returned by Reserve (API: 413 QUOTA_EXCEEDED).
type ExceededError struct {
	Reason string // "quota" | "disk"
	Used   int64  // effective usage (+ reservations)
	Limit  int64
	Needed int64
	Free   int64 // disk free (reason disk) or quota free (reason quota)
}

func (e *ExceededError) Error() string {
	return fmt.Sprintf("quota: %s exceeded (used %d of %d, needed %d)", e.Reason, e.Used, e.Limit, e.Needed)
}

// Service is the accounting façade shared by file operations, uploads,
// archives and the Recycle Bin.
type Service struct {
	repo        db.Repository
	logger      *slog.Logger
	now         func() time.Time
	MinDiskFree int64

	mu       sync.Mutex
	reserved map[string]int64
	// extra reports reservations persisted outside this process (active
	// upload sessions, SPEC §4.2): they survive restarts and are counted
	// like in-memory reservations by Usage and Reserve.
	extra []ReservedSource
}

// ReservedSource returns bytes a user has reserved durably (e.g. the sum of
// active upload sessions). Errors are logged and treated as zero.
type ReservedSource func(ctx context.Context, userID string) (int64, error)

// AddReservedSource registers a durable reservation source.
func (s *Service) AddReservedSource(src ReservedSource) {
	s.mu.Lock()
	s.extra = append(s.extra, src)
	s.mu.Unlock()
}

// New builds a Service.
func New(repo db.Repository, logger *slog.Logger, now func() time.Time) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Service{repo: repo, logger: logger, now: now, MinDiskFree: DefaultMinDiskFree, reserved: map[string]int64{}}
}

// Usage reads the limit from users and the counters from quota_usage (zero
// when the user has none yet).
func (s *Service) Usage(ctx context.Context, userID string) (Usage, error) {
	u, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		return Usage{}, err
	}
	return s.usageFor(ctx, u)
}

// UsageFor is Usage for an already loaded user row.
func (s *Service) UsageFor(ctx context.Context, u sqlc.User) (Usage, error) {
	return s.usageFor(ctx, u)
}

func (s *Service) usageFor(ctx context.Context, u sqlc.User) (Usage, error) {
	out := Usage{Limit: u.QuotaBytes}
	row, err := s.repo.GetQuotaUsage(ctx, u.ID)
	switch {
	case err == nil:
		out.Used, out.Trash, out.Versions = row.UsedBytes, row.TrashBytes, row.VersionsBytes
		out.FileCount, out.DirCount = row.FileCount, row.DirCount
		out.Dirty = row.Dirty != 0
		out.CalculatedAt = row.CalculatedAt
	case db.IsNotFound(err):
		out.Dirty = true
	default:
		return Usage{}, err
	}
	s.mu.Lock()
	out.Reserved = s.reserved[u.ID]
	extra := s.extra
	s.mu.Unlock()
	for _, src := range extra {
		n, err := src(ctx, u.ID)
		if err != nil {
			s.logger.WarnContext(ctx, "reservation source failed", slog.String("error", err.Error()))
			continue
		}
		out.Reserved += n
	}
	eff := out.Effective() + out.Reserved
	if out.Limit > 0 {
		out.Free = max(0, out.Limit-eff)
		out.Percent = float64(int(float64(eff)/float64(out.Limit)*1000+0.5)) / 10
	}
	return out, nil
}

// Reservation holds bytes against the user's quota until Commit or Release.
type Reservation struct {
	s      *Service
	userID string
	bytes  int64
	done   bool
}

// Bytes returns the reserved amount.
func (r *Reservation) Bytes() int64 { return r.bytes }

// Release drops the reservation without touching the counters.
func (r *Reservation) Release() {
	if r == nil || r.done {
		return
	}
	r.done = true
	r.s.mu.Lock()
	r.s.reserved[r.userID] -= r.bytes
	if r.s.reserved[r.userID] <= 0 {
		delete(r.s.reserved, r.userID)
	}
	r.s.mu.Unlock()
}

// Commit releases the reservation and adds actual bytes to used_bytes
// through repo (the operation's transaction when one is open).
func (r *Reservation) Commit(ctx context.Context, repo db.Repository, actual int64) error {
	r.Release()
	if actual == 0 {
		return nil
	}
	return r.s.Apply(ctx, repo, r.userID, actual, 0, 0)
}

// Reserve checks that bytes more fit into the user's quota (counting other
// live reservations) and that the storage filesystem keeps MinDiskFree
// afterwards (j nil skips the disk check). The returned reservation must be
// committed or released.
func (s *Service) Reserve(ctx context.Context, j *fsjail.Jail, userID string, bytes int64) (*Reservation, error) {
	if bytes < 0 {
		bytes = 0
	}
	usage, err := s.Usage(ctx, userID)
	if err != nil {
		return nil, err
	}
	if usage.Limit > 0 && usage.Effective()+usage.Reserved+bytes > usage.Limit {
		return nil, &ExceededError{Reason: "quota", Used: usage.Effective() + usage.Reserved, Limit: usage.Limit, Needed: bytes, Free: usage.Free}
	}
	if j != nil && bytes > 0 {
		free, _, serr := j.Statfs()
		if serr != nil {
			s.logger.WarnContext(ctx, "statfs failed; skipping disk check", slog.String("error", serr.Error()))
		} else if int64(free)-bytes < s.MinDiskFree { //nolint:gosec // free fits int64 on every real disk
			return nil, &ExceededError{Reason: "disk", Used: usage.Effective(), Limit: usage.Limit, Needed: bytes, Free: int64(free)} //nolint:gosec
		}
	}
	s.mu.Lock()
	s.reserved[userID] += bytes
	s.mu.Unlock()
	return &Reservation{s: s, userID: userID, bytes: bytes}, nil
}

// Apply adds the deltas to the counters through repo (SPEC §8.2
// "Inkremental"). Counters are clamped at zero by the query.
func (s *Service) Apply(ctx context.Context, repo db.Repository, userID string, dUsed, dTrash, dVersions int64) error {
	if dUsed == 0 && dTrash == 0 && dVersions == 0 {
		return nil
	}
	if repo == nil {
		repo = s.repo
	}
	return repo.ApplyQuotaDelta(ctx, userID, dUsed, dTrash, dVersions, db.FormatTime(s.now()))
}

// MarkDirty flags the counters as unreliable (partial failure, walk budget
// exceeded); the nightly recalculation also picks dirty users up.
func (s *Service) MarkDirty(ctx context.Context, userID string) error {
	return s.repo.SetQuotaDirty(ctx, userID, db.FormatTime(s.now()))
}

// Target describes what Recalculate walks for one user.
type Target struct {
	UserID   string
	Jail     *fsjail.Jail
	Trash    *fsjail.SysDir // nil = 0
	Versions *fsjail.SysDir // nil = 0
}

// Result reports a recalculation.
type Result struct {
	Usage   Usage
	Before  int64 // effective bytes before
	After   int64 // effective bytes after
	Drifted bool  // |after-before| > DriftThreshold of max(after, before)
}

// Recalculate walks the user's root (Lstat sizes, symlinks count as their
// link size), the trash folder and the versions folder and stores the
// absolute numbers (SPEC §8.2 "Rekalkulasi").
func (s *Service) Recalculate(ctx context.Context, t Target) (Result, error) {
	before, err := s.Usage(ctx, t.UserID)
	if err != nil {
		return Result{}, err
	}
	st, err := t.Jail.DirSize(ctx, fsjail.Root, nil)
	if err != nil {
		return Result{}, fmt.Errorf("quota: walk root: %w", err)
	}
	trashBytes, err := sysSize(ctx, t.Trash)
	if err != nil {
		return Result{}, fmt.Errorf("quota: walk trash: %w", err)
	}
	versionBytes, err := sysSize(ctx, t.Versions)
	if err != nil {
		return Result{}, fmt.Errorf("quota: walk versions: %w", err)
	}
	now := db.FormatTime(s.now())
	if err := s.repo.SetQuotaUsage(ctx, sqlc.SetQuotaUsageParams{
		UserID: t.UserID, UsedBytes: st.Bytes, TrashBytes: trashBytes, VersionsBytes: versionBytes,
		FileCount: st.Files, DirCount: st.Dirs, CalculatedAt: &now, UpdatedAt: now,
	}); err != nil {
		return Result{}, err
	}
	after, err := s.Usage(ctx, t.UserID)
	if err != nil {
		return Result{}, err
	}
	res := Result{Usage: after, Before: before.Effective(), After: after.Effective()}
	diff := res.After - res.Before
	if diff < 0 {
		diff = -diff
	}
	base := max(res.After, res.Before)
	if base > 0 && !before.Dirty && float64(diff)/float64(base) > DriftThreshold {
		res.Drifted = true
		s.logger.WarnContext(ctx, "quota drift corrected", slog.String("user", t.UserID), slog.Int64("before", res.Before), slog.Int64("after", res.After))
	}
	return res, nil
}

// sysSize sums regular file (and symlink) sizes below a SysDir.
func sysSize(ctx context.Context, d *fsjail.SysDir) (int64, error) {
	if d == nil {
		return 0, nil
	}
	var total int64
	err := fs.WalkDir(d.FS(), ".", func(_ string, e fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if e.IsDir() {
			return nil
		}
		info, ierr := e.Info()
		if ierr != nil {
			if errors.Is(ierr, fs.ErrNotExist) {
				return nil
			}
			return ierr
		}
		total += info.Size()
		return nil
	})
	return total, err
}

// SumAll returns Σ(used+trash+versions) over every user (admin dashboard).
func (s *Service) SumAll(ctx context.Context) (int64, error) { return s.repo.SumQuotaUsed(ctx) }

// QuotaEvent is the quota.updated SSE payload (SPEC §6.4) after a task or
// background job changed the counters; ok is false when usage is unknown.
func (s *Service) QuotaEvent(ctx context.Context, userID string) (any, bool) {
	u, err := s.Usage(ctx, userID)
	if err != nil {
		return nil, false
	}
	return map[string]any{"used": u.Used, "trash": u.Trash, "versions": u.Versions, "limit": u.Limit, "free": u.Free, "percent": u.Percent, "dirty": u.Dirty}, true
}
