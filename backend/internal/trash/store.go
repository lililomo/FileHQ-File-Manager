// Package trash is the per-user Recycle Bin of SPEC §8.6 and ADR-17: items
// are renamed into DATA_DIR/trash/<user_id>/<trash_item_id> with a
// trash_items row that remembers the original path, and come back with
// Restore. Listing, purge, empty and the expiry job live here too; the
// orchestration with locks, activity and listing caches is in internal/files.
package trash

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/quota"
)

// Kinds accepted by trash_items.kind (SPEC §4.2).
var Kinds = map[string]bool{"file": true, "folder": true, "image": true, "document": true, "video": true, "audio": true, "archive": true, "other": true}

// SizeWalkBudget bounds the size/count walk of a folder being deleted; when
// exceeded the partial numbers are stored and the quota is flagged dirty
// (SPEC §8.5 "batas 20 s").
const SizeWalkBudget = 20 * time.Second

// PurgedRowRetention keeps purged rows for the activity page before they are
// pruned (SPEC §4.2 trash_items).
const PurgedRowRetention = 7 * 24 * time.Hour

// MaxList caps the rows List loads for one user before paging in memory.
const MaxList = 20000

// DefaultPageSize is the List page when the client sends no limit.
const DefaultPageSize = 500

// Errors.
var (
	ErrNotFound = errors.New("trash: item not found")
	ErrGone     = errors.New("trash: item already restored or purged")
)

// Policy decides what happens when the restore destination exists.
type Policy string

// Restore policies (same vocabulary as file operations, SPEC §8.5).
const (
	PolicyFail      Policy = "fail"
	PolicyOverwrite Policy = "overwrite"
	PolicySkip      Policy = "skip"
	PolicyRename    Policy = "rename"
)

// ParsePolicy accepts "", fail, ask, overwrite, skip, rename.
func ParsePolicy(s string) (Policy, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "fail", "ask":
		return PolicyFail, true
	case "overwrite":
		return PolicyOverwrite, true
	case "skip":
		return PolicySkip, true
	case "rename":
		return PolicyRename, true
	}
	return "", false
}

// Stat is the size/mtime pair shown in conflict dialogs.
type Stat struct {
	Size  int64  `json:"size"`
	Mtime string `json:"mtime"`
	IsDir bool   `json:"isDir"`
}

// ConflictError: the restore destination exists and the policy is fail
// (API: 409 ALREADY_EXISTS, details.conflicts[] keyed by trash id).
type ConflictError struct {
	ID       string `json:"id"`
	Path     string `json:"path"`   // destination
	Source   string `json:"source"` // trash id (key of perItem)
	Existing Stat   `json:"existing"`
	Incoming Stat   `json:"incoming"`
}

func (e *ConflictError) Error() string { return "trash: destination exists: " + e.Path }

// UniqueFunc picks a free "name (n).ext" in dir (files.UniqueName).
type UniqueFunc func(j *fsjail.Jail, dir fsjail.VPath, name string, isDir bool) (fsjail.VPath, error)

// PurgeHook runs inside the purge transaction for every item removed for
// good: reference cleanup (SPEC §5.7). j is nil when no jail is available
// (expiry job without an open session).
type PurgeHook func(ctx context.Context, tx db.Repository, j *fsjail.Jail, userID, originalPath string) error

// OpenJailFunc opens a user's jail for background jobs.
type OpenJailFunc func(ctx context.Context, userID string) (*fsjail.Jail, func(), error)

// LessFunc orders names in List (natural sort from listing when wired).
type LessFunc func(a, b string) bool

// Store moves items to and from the Recycle Bin.
type Store struct {
	dataDir   string
	crossFS   bool
	retention time.Duration // 0 = never expires
	repo      db.Repository
	logger    *slog.Logger
	now       func() time.Time

	// Optional collaborators (set before use).
	Quota    *quota.Service
	Unique   UniqueFunc
	OnPurge  PurgeHook
	OpenJail OpenJailFunc
	Less     LessFunc

	mu  sync.Mutex
	sys map[string]*fsjail.SysDir
}

// New builds a Store. retentionDays 0 disables expiry; crossFS only
// changes logging (fsjail.MoveOut handles EXDEV itself).
func New(dataDir string, crossFS bool, retentionDays int, repo db.Repository, logger *slog.Logger, now func() time.Time) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	return &Store{dataDir: dataDir, crossFS: crossFS, retention: time.Duration(retentionDays) * 24 * time.Hour,
		repo: repo, logger: logger, now: now, sys: map[string]*fsjail.SysDir{}, Unique: defaultUnique,
		Less: func(a, b string) bool { return strings.ToLower(a) < strings.ToLower(b) }}
}

// RetentionDays reports the configured auto-purge delay (0 = never).
func (s *Store) RetentionDays() int { return int(s.retention / (24 * time.Hour)) }

// Sys returns the user's trash directory handle (created on first use).
func (s *Store) Sys(userID string) (*fsjail.SysDir, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d, ok := s.sys[userID]; ok {
		return d, nil
	}
	d, err := fsjail.OpenSys(s.dataDir, path.Join("trash", userID))
	if err != nil {
		return nil, err
	}
	s.sys[userID] = d
	return d, nil
}

// Close releases every cached handle.
func (s *Store) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, d := range s.sys {
		_ = d.Close()
		delete(s.sys, id)
	}
}

// Measure walks p (a folder) with SizeWalkBudget and returns bytes and the
// number of entries below it; a file returns its own size and 0 entries.
// complete is false when the budget ran out (numbers are partial).
func (s *Store) Measure(ctx context.Context, j *fsjail.Jail, info fsjail.Info) (bytes, count int64, complete bool) {
	if !info.IsDir {
		return info.Size, 0, true
	}
	wctx, cancel := context.WithTimeout(ctx, SizeWalkBudget)
	defer cancel()
	var last fsjail.DirStats
	st, err := j.DirSize(wctx, info.Path, func(files, dirs, b int64) { last = fsjail.DirStats{Files: files, Dirs: dirs, Bytes: b} })
	if err != nil {
		if !errors.Is(err, context.DeadlineExceeded) {
			s.logger.Warn("trash size walk failed", slog.String("path", string(info.Path)), slog.String("error", err.Error()))
		}
		return last.Bytes, last.Files + last.Dirs, false
	}
	return st.Bytes, st.Files + st.Dirs, true
}

func (s *Store) applyQuota(ctx context.Context, repo db.Repository, userID string, dUsed, dTrash int64) {
	if s.Quota == nil {
		return
	}
	if err := s.Quota.Apply(ctx, repo, userID, dUsed, dTrash, 0); err != nil {
		s.logger.ErrorContext(ctx, "quota update failed", slog.String("user", userID), slog.String("error", err.Error()))
	}
}

func (s *Store) markDirty(ctx context.Context, userID string) {
	if s.Quota == nil {
		return
	}
	if err := s.Quota.MarkDirty(ctx, userID); err != nil {
		s.logger.WarnContext(ctx, "quota dirty flag failed", slog.String("user", userID), slog.String("error", err.Error()))
	}
}

// MoveToTrash moves p into the user's Recycle Bin: the trash_items row is
// written first, then the item is renamed into DATA_DIR/trash/<uid>/<id>
// (SPEC §4.3 ordering: DB soft, then FS; the row is removed if the move
// fails). kind must be one of Kinds; batchID groups one request. The quota
// moves size from used to trash.
func (s *Store) MoveToTrash(ctx context.Context, j *fsjail.Jail, userID string, p fsjail.VPath, kind, batchID string) (sqlc.TrashItem, error) {
	if !Kinds[kind] {
		kind = "other"
	}
	info, err := j.Lstat(p)
	if err != nil {
		return sqlc.TrashItem{}, err
	}
	sys, err := s.Sys(userID)
	if err != nil {
		return sqlc.TrashItem{}, err
	}
	bytes, count, complete := s.Measure(ctx, j, info)
	now := s.now()
	row := sqlc.TrashItem{
		ID: db.NewID(), UserID: userID, OriginalPath: string(p), Name: p.Base(), SizeBytes: bytes, ItemCount: count,
		Kind: kind, DeletedAt: db.FormatTime(now),
	}
	if info.IsDir {
		row.IsDir = 1
		if kind != "folder" {
			row.Kind = "folder"
		}
	}
	if s.retention > 0 {
		row.ExpiresAt = db.Ptr(db.FormatTime(now.Add(s.retention)))
	}
	if batchID != "" {
		row.BatchID = &batchID
	}
	if err := s.repo.InsertTrashItem(ctx, sqlc.InsertTrashItemParams{
		ID: row.ID, UserID: row.UserID, OriginalPath: row.OriginalPath, Name: row.Name, IsDir: row.IsDir,
		SizeBytes: row.SizeBytes, ItemCount: row.ItemCount, Kind: row.Kind, DeletedAt: row.DeletedAt,
		ExpiresAt: row.ExpiresAt, BatchID: row.BatchID,
	}); err != nil {
		return sqlc.TrashItem{}, fmt.Errorf("trash: insert row: %w", err)
	}
	if s.crossFS {
		s.logger.Warn("trash is on another filesystem; copying instead of renaming", slog.String("path", string(p)))
	}
	if err := j.MoveOut(ctx, p, sys, row.ID); err != nil {
		if derr := s.repo.DeleteTrashItemRow(ctx, userID, row.ID); derr != nil {
			s.logger.Error("trash row cleanup failed", slog.String("id", row.ID), slog.String("error", derr.Error()))
		}
		return sqlc.TrashItem{}, err
	}
	s.applyQuota(ctx, nil, userID, -bytes, bytes)
	if !complete {
		s.markDirty(ctx, userID)
	}
	return row, nil
}

// RestoreRequest asks for one item back.
type RestoreRequest struct {
	ID     string
	Dest   fsjail.VPath // full destination path; "" = original path
	Policy Policy
}

// RestoreResult reports one restore.
type RestoreResult struct {
	ID           string `json:"id"`
	OriginalPath string `json:"path"`
	To           string `json:"to"`
	Status       string `json:"status"` // ok | renamed | overwritten | skipped
	IsDir        bool   `json:"isDir"`
	Size         int64  `json:"size"`
	// OverwrittenID is the trash row of the item that was displaced by an
	// overwrite (it can be restored in turn).
	OverwrittenID string `json:"overwrittenId,omitempty"`
}

// Get loads one active row (ErrNotFound for other users' ids).
func (s *Store) Get(ctx context.Context, userID, id string) (sqlc.TrashItem, error) {
	row, err := s.repo.GetTrashItem(ctx, userID, id)
	if err != nil {
		if db.IsNotFound(err) {
			return sqlc.TrashItem{}, ErrNotFound
		}
		return sqlc.TrashItem{}, err
	}
	if row.RestoredAt != nil || row.PurgedAt != nil {
		return row, ErrGone
	}
	return row, nil
}

// Restore moves a trash item back into the jail (SPEC §8.6 "Restore").
// Missing parent folders are recreated; an occupied destination follows the
// policy: fail → ConflictError, skip → status skipped, rename → "name (n)",
// overwrite → the existing item goes to the Recycle Bin first (nothing is
// destroyed without an explicit purge), then the item moves in. The quota
// moves size from trash back to used.
func (s *Store) Restore(ctx context.Context, j *fsjail.Jail, userID string, req RestoreRequest) (RestoreResult, error) {
	row, err := s.Get(ctx, userID, req.ID)
	if err != nil {
		return RestoreResult{}, err
	}
	res := RestoreResult{ID: row.ID, OriginalPath: row.OriginalPath, IsDir: row.IsDir != 0, Size: row.SizeBytes}
	dst := req.Dest
	if dst == "" {
		dst, err = fsjail.Clean(row.OriginalPath)
		if err != nil {
			return res, err
		}
	}
	if dst.IsRoot() {
		return res, &fsjail.Error{Code: fsjail.CodePathInvalid, Op: "restore", Path: dst, Reason: "root", Err: fsjail.ErrInvalidPath}
	}
	sys, err := s.Sys(userID)
	if err != nil {
		return res, err
	}
	srcInfo, err := sys.Lstat(row.ID)
	if err != nil {
		return res, fmt.Errorf("trash: payload missing: %w", err)
	}
	if dir := dst.Dir(); !dir.IsRoot() {
		if err := j.MkdirAll(dir); err != nil {
			return res, err
		}
	}
	res.Status = "ok"
	if existing, lerr := j.Lstat(dst); lerr == nil {
		switch req.Policy {
		case PolicySkip:
			res.Status, res.To = "skipped", string(dst)
			return res, nil
		case PolicyRename:
			dst, err = s.Unique(j, dst.Dir(), dst.Base(), res.IsDir)
			if err != nil {
				return res, err
			}
			res.Status = "renamed"
		case PolicyOverwrite:
			displaced, terr := s.MoveToTrash(ctx, j, userID, dst, kindOf(existing), "restore-overwrite")
			if terr != nil {
				return res, fmt.Errorf("trash: displace existing item: %w", terr)
			}
			res.Status, res.OverwrittenID = "overwritten", displaced.ID
		default:
			return res, &ConflictError{ID: row.ID, Path: string(dst), Source: row.ID,
				Existing: Stat{Size: existing.Size, Mtime: iso(existing.ModTime), IsDir: existing.IsDir},
				Incoming: Stat{Size: row.SizeBytes, Mtime: iso(srcInfo.ModTime()), IsDir: res.IsDir}}
		}
	} else if !errors.Is(lerr, fsjail.ErrNotFound) {
		return res, lerr
	}
	if err := j.MoveIn(ctx, sys, row.ID, dst); err != nil {
		return res, err
	}
	res.To = string(dst)
	if _, err := s.repo.SetTrashRestored(ctx, userID, row.ID, db.FormatTime(s.now())); err != nil {
		s.logger.Error("trash restored flag failed", slog.String("id", row.ID), slog.String("error", err.Error()))
	}
	s.applyQuota(ctx, nil, userID, row.SizeBytes, -row.SizeBytes)
	return res, nil
}

// PurgeResult reports one permanent deletion.
type PurgeResult struct {
	ID           string `json:"id"`
	OriginalPath string `json:"path"`
	Size         int64  `json:"size"`
	IsDir        bool   `json:"isDir"`
	Status       string `json:"status"` // ok | gone
}

// purgeRow removes the payload of row and marks it purged inside one
// transaction together with the reference cleanup and the quota delta.
func (s *Store) purgeRow(ctx context.Context, j *fsjail.Jail, row sqlc.TrashItem) error {
	sys, err := s.Sys(row.UserID)
	if err != nil {
		return err
	}
	if err := sys.RemoveAll(row.ID); err != nil && !errors.Is(err, fsjail.ErrNotFound) {
		return err
	}
	at := db.FormatTime(s.now())
	return s.repo.WithTx(ctx, func(tx db.Repository) error {
		if _, err := tx.SetTrashPurged(ctx, row.UserID, row.ID, at); err != nil {
			return err
		}
		if s.OnPurge != nil {
			if err := s.OnPurge(ctx, tx, j, row.UserID, row.OriginalPath); err != nil {
				return err
			}
		}
		if s.Quota != nil {
			if err := s.Quota.Apply(ctx, tx, row.UserID, 0, -row.SizeBytes, 0); err != nil {
				return err
			}
		}
		return nil
	})
}

// Purge deletes the given items for good (SPEC §8.6). Unknown ids (other
// users' included) are reported as gone, never as an error about someone
// else's data. Returns the per-item results and the bytes freed.
func (s *Store) Purge(ctx context.Context, j *fsjail.Jail, userID string, ids []string) ([]PurgeResult, int64, error) {
	rows, err := s.repo.ListTrashItemsByIDs(ctx, userID, ids)
	if err != nil {
		return nil, 0, err
	}
	byID := map[string]sqlc.TrashItem{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	out := make([]PurgeResult, 0, len(ids))
	var freed int64
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		row, ok := byID[id]
		if !ok || row.RestoredAt != nil || row.PurgedAt != nil {
			out = append(out, PurgeResult{ID: id, Status: "gone"})
			continue
		}
		if err := ctx.Err(); err != nil {
			return out, freed, err
		}
		if err := s.purgeRow(ctx, j, row); err != nil {
			return out, freed, fmt.Errorf("trash: purge %s: %w", id, err)
		}
		freed += row.SizeBytes
		out = append(out, PurgeResult{ID: id, OriginalPath: row.OriginalPath, Size: row.SizeBytes, IsDir: row.IsDir != 0, Status: "ok"})
	}
	return out, freed, nil
}

// Empty purges everything in the user's Recycle Bin.
func (s *Store) Empty(ctx context.Context, j *fsjail.Jail, userID string) (count int, freed int64, err error) {
	rows, err := s.repo.ListTrashItems(ctx, userID)
	if err != nil {
		return 0, 0, err
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return count, freed, err
		}
		if err := s.purgeRow(ctx, j, row); err != nil {
			return count, freed, fmt.Errorf("trash: empty %s: %w", row.ID, err)
		}
		count++
		freed += row.SizeBytes
	}
	return count, freed, nil
}

// PurgeExpired removes items whose expires_at has passed, in batches, for
// every user (the 03:00 job). It also prunes rows purged longer ago than
// PurgedRowRetention. Returns items purged and bytes freed.
func (s *Store) PurgeExpired(ctx context.Context, batch int) (count int, freed int64, err error) {
	if batch <= 0 {
		batch = 100
	}
	now := db.FormatTime(s.now())
	jails := map[string]*fsjail.Jail{}
	var closers []func()
	defer func() {
		for _, c := range closers {
			c()
		}
	}()
	for {
		rows, lerr := s.repo.ListExpiredTrash(ctx, now, int64(batch))
		if lerr != nil {
			return count, freed, lerr
		}
		if len(rows) == 0 {
			break
		}
		for _, row := range rows {
			if err := ctx.Err(); err != nil {
				return count, freed, err
			}
			j := jails[row.UserID]
			if j == nil && s.OpenJail != nil {
				if oj, closeFn, oerr := s.OpenJail(ctx, row.UserID); oerr == nil {
					j = oj
					jails[row.UserID] = oj
					closers = append(closers, closeFn)
				}
			}
			if perr := s.purgeRow(ctx, j, row); perr != nil {
				// mark the row so the batch loop cannot spin on it
				s.logger.ErrorContext(ctx, "auto purge failed", slog.String("id", row.ID), slog.String("error", perr.Error()))
				if _, merr := s.repo.SetTrashPurged(ctx, row.UserID, row.ID, now); merr != nil {
					return count, freed, perr
				}
				continue
			}
			count++
			freed += row.SizeBytes
		}
		if len(rows) < batch {
			break
		}
	}
	if n, perr := s.repo.DeletePurgedTrashRowsBefore(ctx, db.FormatTime(s.now().Add(-PurgedRowRetention))); perr != nil {
		s.logger.WarnContext(ctx, "trash row prune failed", slog.String("error", perr.Error()))
	} else if n > 0 {
		s.logger.InfoContext(ctx, "trash rows pruned", slog.Int64("rows", n))
	}
	return count, freed, nil
}

// --- listing ---

// Filter narrows and orders List.
type Filter struct {
	Kind   string // "" | file | folder (alias dir) | image | document | video | audio | archive | other
	Query  string // case-insensitive substring of the name
	Sort   string // deleted (default) | name | size | expires | path
	Desc   *bool  // default: deleted desc, everything else asc
	Cursor string
	Limit  int
}

// Item is one Recycle Bin row for the API.
type Item struct {
	ID           string  `json:"id"`
	Name         string  `json:"name"`
	OriginalPath string  `json:"originalPath"`
	IsDir        bool    `json:"isDir"`
	Size         int64   `json:"size"`
	ItemCount    int64   `json:"itemCount"`
	Kind         string  `json:"kind"`
	DeletedAt    string  `json:"deletedAt"`
	ExpiresAt    *string `json:"expiresAt"`
	BatchID      *string `json:"batchId,omitempty"`
}

// ListResult is one page.
type ListResult struct {
	Items      []Item `json:"items"`
	Total      int    `json:"total"`
	TotalBytes int64  `json:"totalBytes"`
	NextCursor string `json:"nextCursor,omitempty"`
}

// List returns the user's active trash items filtered, sorted and paged.
// Rows are loaded once (bounded by MaxList) and handled in memory: the
// Recycle Bin is small compared with folders, and SQL cannot natural-sort.
func (s *Store) List(ctx context.Context, userID string, f Filter) (ListResult, error) {
	rows, err := s.repo.ListTrashItems(ctx, userID)
	if err != nil {
		return ListResult{}, err
	}
	if len(rows) > MaxList {
		rows = rows[:MaxList]
	}
	kind := strings.ToLower(f.Kind)
	if kind == "dir" {
		kind = "folder"
	}
	q := strings.ToLower(strings.TrimSpace(f.Query))
	items := make([]Item, 0, len(rows))
	var total int64
	for _, r := range rows {
		if kind != "" && r.Kind != kind {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(r.Name), q) {
			continue
		}
		items = append(items, Item{ID: r.ID, Name: r.Name, OriginalPath: r.OriginalPath, IsDir: r.IsDir != 0, Size: r.SizeBytes,
			ItemCount: r.ItemCount, Kind: r.Kind, DeletedAt: r.DeletedAt, ExpiresAt: r.ExpiresAt, BatchID: r.BatchID})
		total += r.SizeBytes
	}
	desc := f.Desc != nil && *f.Desc
	if f.Desc == nil && (f.Sort == "" || f.Sort == "deleted") {
		desc = true
	}
	less := s.lessFor(f.Sort)
	sort.SliceStable(items, func(i, k int) bool {
		if desc {
			return less(items[k], items[i])
		}
		return less(items[i], items[k])
	})
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultPageSize
	}
	offset := decodeCursor(f.Cursor)
	if offset > len(items) {
		offset = len(items)
	}
	end := min(offset+limit, len(items))
	out := ListResult{Items: items[offset:end], Total: len(items), TotalBytes: total}
	if end < len(items) {
		out.NextCursor = encodeCursor(end)
	}
	return out, nil
}

func (s *Store) lessFor(sortBy string) func(a, b Item) bool {
	byName := func(a, b Item) bool {
		if s.Less(a.Name, b.Name) {
			return true
		}
		if s.Less(b.Name, a.Name) {
			return false
		}
		return a.ID < b.ID
	}
	switch sortBy {
	case "name":
		return byName
	case "size":
		return func(a, b Item) bool {
			if a.Size != b.Size {
				return a.Size < b.Size
			}
			return byName(a, b)
		}
	case "expires":
		return func(a, b Item) bool {
			ea, eb := "", ""
			if a.ExpiresAt != nil {
				ea = *a.ExpiresAt
			}
			if b.ExpiresAt != nil {
				eb = *b.ExpiresAt
			}
			if ea != eb {
				if ea == "" {
					return false
				}
				if eb == "" {
					return true
				}
				return ea < eb
			}
			return byName(a, b)
		}
	case "path":
		return func(a, b Item) bool {
			if a.OriginalPath != b.OriginalPath {
				return s.Less(a.OriginalPath, b.OriginalPath)
			}
			return a.ID < b.ID
		}
	default: // deleted
		return func(a, b Item) bool {
			if a.DeletedAt != b.DeletedAt {
				return a.DeletedAt < b.DeletedAt
			}
			return a.ID < b.ID
		}
	}
}

func encodeCursor(offset int) string {
	return base64.RawURLEncoding.EncodeToString([]byte("o:" + strconv.Itoa(offset)))
}

func decodeCursor(c string) int {
	if c == "" {
		return 0
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil || !strings.HasPrefix(string(raw), "o:") {
		return 0
	}
	n, err := strconv.Atoi(string(raw[2:]))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// --- helpers ---

func iso(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

// kindOf is the kind of an item displaced by an overwrite; the extension
// table lives in files (which imports this package), so only the
// folder/file distinction is known here.
func kindOf(info fsjail.Info) string {
	if info.IsDir {
		return "folder"
	}
	return "file"
}

// defaultUnique finds "name (n).ext" with a last-dot extension split; the
// compound-extension aware version from files is wired by main.
func defaultUnique(j *fsjail.Jail, dir fsjail.VPath, name string, isDir bool) (fsjail.VPath, error) {
	stem, ext := name, ""
	if !isDir {
		if i := strings.LastIndex(name, "."); i > 0 {
			stem, ext = name[:i], name[i:]
		}
	}
	for n := 1; n <= 10000; n++ {
		p, err := fsjail.Join(dir, fmt.Sprintf("%s (%d)%s", stem, n, ext))
		if err != nil {
			return "", err
		}
		if _, err := j.Lstat(p); err != nil {
			if errors.Is(err, fsjail.ErrNotFound) {
				return p, nil
			}
			return "", err
		}
	}
	return "", fmt.Errorf("trash: no free name for %s in %s", name, dir)
}
