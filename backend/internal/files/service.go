package files

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"slices"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/trash"
)

// Synchronous limits (PROMPT Step 9a task 5): larger copy/move/chmod
// operations need the task engine of Step 11 and answer TooLargeError.
const (
	DefaultSyncMaxBytes   int64 = 200 << 20
	DefaultSyncMaxEntries       = 2000
	// EstimateBudget bounds the pre-walk that counts entries and bytes.
	EstimateBudget = 5 * time.Second
	// TaskEstimateBudget is the same pre-walk inside a task (progress total).
	TaskEstimateBudget = 60 * time.Second
	// MaxBatchItems bounds paths per request (batch rename, delete, copy, move).
	MaxBatchItems = 2000
	// MaxBatchRenameItems is the synchronous batch rename limit (SPEC §8.5).
	MaxBatchRenameItems = 200
)

// ChangeFunc is the mutation hook (thumbnail/media invalidation, SPEC §8.4).
type ChangeFunc func(ctx context.Context, userID string, path fsjail.VPath, isDir bool)

// InvalidateFunc drops the listing cache of a folder.
type InvalidateFunc func(userID string, dir fsjail.VPath)

// Options configures a Service.
type Options struct {
	Repo           db.Repository
	Locks          *fsjail.LockManager
	Trash          *trash.Store
	Logger         *slog.Logger
	Now            func() time.Time
	SyncMaxBytes   int64
	SyncMaxEntries int
	OnChange       ChangeFunc
	Invalidate     InvalidateFunc
	// Quota enables reservation/accounting (Step 10a); nil = no limits.
	Quota *quota.Service
	// BlockedExtensions (SPEC §5.2 BLOCKED_EXTENSIONS, lowercase without the
	// dot): batch rename refuses file names ending in one of them, like the
	// single rename endpoint does (review B, F-1).
	BlockedExtensions []string
}

// Service implements the file operations of SPEC §8.5 on top of fsjail,
// the lock manager, the Recycle Bin and the metadata tables.
type Service struct {
	repo           db.Repository
	locks          *fsjail.LockManager
	trash          *trash.Store
	logger         *slog.Logger
	now            func() time.Time
	syncMaxBytes   int64
	syncMaxEntries int
	onChange       ChangeFunc
	invalidate     InvalidateFunc
	undo           *UndoStore
	quota          *quota.Service
	blocked        func(name string) bool
	dirSizes       *dirSizeCache
	checksums      *checksumCache
	analyses       *analysisCache

	// TestHook, when set, runs after locks are taken and before the
	// filesystem is touched (concurrency tests).
	TestHook func(op string)
}

// New builds a Service.
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Locks == nil {
		o.Locks = fsjail.NewLockManager()
	}
	if o.SyncMaxBytes <= 0 {
		o.SyncMaxBytes = DefaultSyncMaxBytes
	}
	if o.SyncMaxEntries <= 0 {
		o.SyncMaxEntries = DefaultSyncMaxEntries
	}
	s := &Service{repo: o.Repo, locks: o.Locks, trash: o.Trash, logger: o.Logger, now: o.Now,
		syncMaxBytes: o.SyncMaxBytes, syncMaxEntries: o.SyncMaxEntries, onChange: o.OnChange,
		invalidate: o.Invalidate, undo: NewUndoStore(o.Now), quota: o.Quota,
		dirSizes: newDirSizeCache(o.Now), checksums: newChecksumCache(o.Now), analyses: newAnalysisCache(o.Now)}
	if len(o.BlockedExtensions) > 0 {
		blocked := slices.Clone(o.BlockedExtensions)
		s.blocked = func(name string) bool {
			ext := Ext(name)
			return ext != "" && slices.Contains(blocked, ext)
		}
	}
	return s
}

// Blocked reports whether a file name ends in a blocked extension.
func (s *Service) Blocked(name string) bool { return s.blocked != nil && s.blocked(name) }

// Quota exposes the accounting service (nil when disabled).
func (s *Service) Quota() *quota.Service { return s.quota }

// Trash exposes the Recycle Bin store.
func (s *Service) Trash() *trash.Store { return s.trash }

// Undo exposes the token store (tests, metrics).
func (s *Service) UndoStore() *UndoStore { return s.undo }

// Locks exposes the lock manager shared with handlers and tasks.
func (s *Service) Locks() *fsjail.LockManager { return s.locks }

func (s *Service) hook(op string) {
	if s.TestHook != nil {
		s.TestHook(op)
	}
}

func (s *Service) changed(ctx context.Context, userID string, p fsjail.VPath, isDir bool) {
	if s.onChange != nil {
		s.onChange(ctx, userID, p, isDir)
	}
	s.invalidateDir(userID, p.Dir())
	s.dirSizes.invalidate(userID, p)
	s.checksums.invalidate(userID, p)
	s.analyses.invalidate(userID)
}

// PurgeReferences is the trash.PurgeHook: references of a purged item are
// removed unless a new item lives at the original path now (its metadata
// must survive). j is nil in the expiry job when no jail could be opened.
func PurgeReferences(ctx context.Context, tx db.Repository, j *fsjail.Jail, userID, originalPath string) error {
	if j != nil {
		if p, err := fsjail.Clean(originalPath); err == nil {
			if _, err := j.Lstat(p); err == nil {
				return nil
			}
		}
	}
	return DeleteReferences(ctx, tx, userID, originalPath)
}

// measure walks p with SizeWalkBudget (files only count bytes; symlinks count
// their link size). complete is false when the budget ran out.
func (s *Service) measure(ctx context.Context, j *fsjail.Jail, info fsjail.Info) (bytes, count int64, complete bool) {
	if !info.IsDir {
		return info.Size, 0, true
	}
	wctx, cancel := context.WithTimeout(ctx, trash.SizeWalkBudget)
	defer cancel()
	var last fsjail.DirStats
	st, err := j.DirSize(wctx, info.Path, func(files, dirs, b int64) { last = fsjail.DirStats{Files: files, Dirs: dirs, Bytes: b} })
	if err != nil {
		return last.Bytes, last.Files + last.Dirs, false
	}
	return st.Bytes, st.Files + st.Dirs, true
}

// sizeOf returns the byte size of p (0 when missing) with the walk budget.
func (s *Service) sizeOf(ctx context.Context, j *fsjail.Jail, p fsjail.VPath) (int64, bool) {
	info, err := j.Lstat(p)
	if err != nil {
		return 0, true
	}
	b, _, complete := s.measure(ctx, j, info)
	return b, complete
}

func (s *Service) markQuotaDirty(ctx context.Context, userID string) {
	if s.quota == nil {
		return
	}
	if err := s.quota.MarkDirty(ctx, userID); err != nil {
		s.logger.WarnContext(ctx, "quota dirty flag failed", slog.String("user", userID), slog.String("error", err.Error()))
	}
}

func (s *Service) invalidateDir(userID string, dir fsjail.VPath) {
	if s.invalidate != nil {
		s.invalidate(userID, dir)
	}
}

func isNotFound(err error) bool {
	return errors.Is(err, fsjail.ErrNotFound) || errors.Is(err, fs.ErrNotExist)
}

// lock takes write locks on all paths at once (sorted, deduplicated by the
// manager); it fails fast with fsjail.ErrLocked (OPERATION_IN_PROGRESS).
// Inside a task (WithTask) it waits for the paths instead, so a queued
// task simply runs after the synchronous operation holding them.
func (s *Service) lock(ctx context.Context, userID, owner string, mode fsjail.Mode, paths ...fsjail.VPath) (func(), error) {
	if unbounded(ctx) {
		return s.locks.Acquire(ctx, userID, mode, "task:"+owner, paths...)
	}
	return s.locks.TryAcquire(userID, mode, owner, paths...)
}

// ItemResult is the per-item outcome of a batch operation.
type ItemResult struct {
	Path     string   `json:"path"`
	To       string   `json:"to,omitempty"`
	Status   string   `json:"status"` // ok | skipped | overwritten | renamed | merged
	IsDir    bool     `json:"isDir"`
	TrashID  string   `json:"trashId,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// Result is returned by every mutating operation.
type Result struct {
	Results   []ItemResult `json:"results"`
	BatchID   string       `json:"batchId,omitempty"`
	UndoToken string       `json:"undoToken,omitempty"`
	Undo      *UndoInfo    `json:"undo"`
	Warnings  []string     `json:"warnings,omitempty"`
}

func (s *Service) withUndo(ctx context.Context, res *Result, userID string, info UndoInfo) {
	if unbounded(ctx) {
		return // tasks are not undoable (SPEC §8.5)
	}
	res.UndoToken = s.undo.Put(userID, info)
	cp := info
	res.Undo = &cp
}

// estimate walks paths and refuses operations beyond the synchronous limits.
func (s *Service) estimate(ctx context.Context, j *fsjail.Jail, paths []fsjail.VPath) (entries int, bytes int64, err error) {
	budget, limits := EstimateBudget, true
	if unbounded(ctx) {
		// task mode: count everything for the progress total; a walk that
		// outlives the budget leaves the total unknown (0) instead of failing
		budget, limits = TaskEstimateBudget, false
	}
	wctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	tooLarge := func(reason string) error {
		return &TooLargeError{Entries: entries, Bytes: bytes, MaxEntries: s.syncMaxEntries, MaxBytes: s.syncMaxBytes, Reason: reason}
	}
	stop := errors.New("stop")
	for _, p := range paths {
		info, err := j.Lstat(p)
		if err != nil {
			return 0, 0, err
		}
		entries++
		if !info.IsDir {
			bytes += info.Size
		} else {
			err = j.WalkDir(wctx, p, func(vp string, d fs.DirEntry, werr error) error {
				if werr != nil {
					return werr
				}
				if fsjail.VPath(vp) == p {
					return nil
				}
				entries++
				if !d.IsDir() {
					if fi, ierr := d.Info(); ierr == nil {
						bytes += fi.Size()
					}
				}
				if limits && (entries > s.syncMaxEntries || bytes > s.syncMaxBytes) {
					return stop
				}
				return nil
			})
			if err != nil {
				switch {
				case errors.Is(err, stop):
					// limits checked below
				case errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil:
					if !limits {
						return entries, 0, nil
					}
					return entries, bytes, tooLarge("estimate_timeout")
				default:
					return 0, 0, err
				}
			}
		}
		if limits && entries > s.syncMaxEntries {
			return entries, bytes, tooLarge("entries")
		}
		if limits && bytes > s.syncMaxBytes {
			return entries, bytes, tooLarge("bytes")
		}
	}
	return entries, bytes, nil
}

// dedupe removes duplicate paths while keeping order.
func dedupe(paths []fsjail.VPath) []fsjail.VPath {
	seen := map[fsjail.VPath]struct{}{}
	out := make([]fsjail.VPath, 0, len(paths))
	for _, p := range paths {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}

// TrashKind maps a name/type to trash_items.kind (SPEC §4.2).
func TrashKind(name string, isDir bool) string {
	if isDir {
		return "folder"
	}
	switch Lookup(name).Category {
	case CatImage:
		return "image"
	case CatVideo:
		return "video"
	case CatAudio:
		return "audio"
	case CatArchive:
		return "archive"
	case CatDocument, CatPDF, CatSpreadsheet, CatPresentation, CatText:
		return "document"
	case CatCode:
		return "file"
	}
	return "other"
}
