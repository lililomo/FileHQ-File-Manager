package files

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/trash"
)

// Synchronous limits (PROMPT Step 9a task 5): larger copy/move/chmod
// operations need the task engine of Step 11 and answer TooLargeError.
const (
	DefaultSyncMaxBytes   int64 = 200 << 20
	DefaultSyncMaxEntries       = 2000
	// EstimateBudget bounds the pre-walk that counts entries and bytes.
	EstimateBudget = 5 * time.Second
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
	return &Service{repo: o.Repo, locks: o.Locks, trash: o.Trash, logger: o.Logger, now: o.Now,
		syncMaxBytes: o.SyncMaxBytes, syncMaxEntries: o.SyncMaxEntries, onChange: o.OnChange,
		invalidate: o.Invalidate, undo: NewUndoStore(o.Now)}
}

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
func (s *Service) lock(userID, owner string, mode fsjail.Mode, paths ...fsjail.VPath) (func(), error) {
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

func (s *Service) withUndo(res *Result, userID string, info UndoInfo) {
	res.UndoToken = s.undo.Put(userID, info)
	cp := info
	res.Undo = &cp
}

// estimate walks paths and refuses operations beyond the synchronous limits.
func (s *Service) estimate(ctx context.Context, j *fsjail.Jail, paths []fsjail.VPath) (entries int, bytes int64, err error) {
	wctx, cancel := context.WithTimeout(ctx, EstimateBudget)
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
				if entries > s.syncMaxEntries || bytes > s.syncMaxBytes {
					return stop
				}
				return nil
			})
			if err != nil {
				switch {
				case errors.Is(err, stop):
					// limits checked below
				case errors.Is(err, context.DeadlineExceeded):
					return entries, bytes, tooLarge("estimate_timeout")
				default:
					return 0, 0, err
				}
			}
		}
		if entries > s.syncMaxEntries {
			return entries, bytes, tooLarge("entries")
		}
		if bytes > s.syncMaxBytes {
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
