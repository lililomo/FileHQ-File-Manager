package files

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strconv"
	"strings"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/trash"
)

// Rename changes the name of p inside its folder (SPEC §8.5 "Rename").
func (s *Service) Rename(ctx context.Context, j *fsjail.Jail, userID string, p fsjail.VPath, newName string) (*Result, error) {
	return s.rename(ctx, j, userID, p, newName, true)
}

func (s *Service) rename(ctx context.Context, j *fsjail.Jail, userID string, p fsjail.VPath, newName string, recordUndo bool) (*Result, error) {
	if p.IsRoot() {
		return nil, &ValidationError{Reason: "root", Path: string(p), Fields: map[string]string{"path": "cannot rename the root"}}
	}
	warnings, err := fsjail.ValidateName(newName)
	if err != nil {
		return nil, err
	}
	newPath, err := fsjail.Join(p.Dir(), newName)
	if err != nil {
		return nil, err
	}
	if newPath == p {
		return nil, &ValidationError{Reason: "same_name", Path: string(p), Fields: map[string]string{"newName": "unchanged"}}
	}
	release, err := s.lock(userID, "rename", fsjail.Write, p, newPath)
	if err != nil {
		return nil, err
	}
	defer release()
	s.hook("rename")
	info, err := j.Lstat(p)
	if err != nil {
		return nil, err
	}
	if existing, err := j.Lstat(newPath); err == nil {
		return nil, &ConflictError{Conflicts: []Conflict{{Path: string(newPath), Source: string(p), Existing: statOf(existing), Incoming: statOf(info)}}}
	} else if !isNotFound(err) {
		return nil, err
	}
	if err := j.Rename(p, newPath); err != nil {
		return nil, err
	}
	action := ActRename
	if !recordUndo {
		action = ActUndo
	}
	if err := s.repo.WithTx(ctx, func(tx db.Repository) error {
		if err := UpdateReferences(ctx, tx, userID, string(p), string(newPath)); err != nil {
			return err
		}
		s.logActivity(ctx, tx, userID, action, string(p), string(newPath), map[string]any{"op": ActRename, "isDir": info.IsDir}, "")
		return nil
	}); err != nil {
		s.logger.ErrorContext(ctx, "reference update failed; rolling back rename", slog.String("path", string(p)), slog.String("error", err.Error()))
		if rerr := j.Rename(newPath, p); rerr != nil {
			s.logger.ErrorContext(ctx, "rename rollback failed", slog.String("path", string(newPath)), slog.String("error", rerr.Error()))
		}
		return nil, fmt.Errorf("files: rename references: %w", err)
	}
	s.changed(ctx, userID, p, info.IsDir)
	res := &Result{Results: []ItemResult{{Path: string(p), To: string(newPath), Status: "ok", IsDir: info.IsDir, Warnings: warnings}}, Warnings: warnings}
	if res.Warnings == nil {
		res.Warnings = []string{}
	}
	if recordUndo {
		s.withUndo(res, userID, UndoInfo{Kind: "rename", Path: string(newPath), NewName: p.Base()})
	}
	return res, nil
}

// plannedItem is one source/destination pair after validation.
type plannedItem struct {
	src     fsjail.VPath
	dst     fsjail.VPath
	info    fsjail.Info
	exists  *fsjail.Info
	policy  Policy
	renamed bool // dst already carries a fresh "(n)" name (copy onto itself)
}

// decide maps a planned item to its outcome: folders meeting folders merge
// with the policy applied per file (overwrite/skip); rename gives the whole
// item a fresh "(n)" name.
func decide(it plannedItem) string {
	if it.renamed {
		return "renamed"
	}
	if it.exists == nil {
		return "ok"
	}
	bothDirs := it.info.IsDir && it.exists.IsDir
	switch it.policy {
	case PolicyRename:
		return "renamed"
	case PolicySkip:
		if bothDirs {
			return "merged"
		}
		return "skipped"
	default:
		if bothDirs {
			return "merged"
		}
		if it.info.IsDir != it.exists.IsDir {
			return "type_mismatch"
		}
		return "overwritten"
	}
}

// plan validates sources against the destination folder and resolves
// conflicts (SPEC §8.5 steps 1-2). It mutates nothing.
func (s *Service) plan(j *fsjail.Jail, paths []fsjail.VPath, to fsjail.VPath, pol ConflictPolicy, op string) ([]plannedItem, error) {
	if len(paths) == 0 {
		return nil, &ValidationError{Reason: "empty", Fields: map[string]string{"paths": "required"}}
	}
	if len(paths) > MaxBatchItems {
		return nil, &ValidationError{Reason: "too_many", Fields: map[string]string{"paths": "max " + strconv.Itoa(MaxBatchItems)}}
	}
	toInfo, err := j.Stat(to)
	if err != nil {
		return nil, err
	}
	if !toInfo.IsDir {
		return nil, &ValidationError{Reason: "not_a_directory", Path: string(to), Fields: map[string]string{"to": "not_a_directory"}}
	}
	paths = dedupe(paths)
	items := make([]plannedItem, 0, len(paths))
	var conflicts []Conflict
	for _, src := range paths {
		if src.IsRoot() {
			return nil, &ValidationError{Reason: "root", Path: string(src), Fields: map[string]string{"paths": "root"}}
		}
		if to == src || to.HasPrefix(src) {
			return nil, &ValidationError{Reason: "cannot_move_into_self", Path: string(src), Fields: map[string]string{"to": "cannot_move_into_self"}}
		}
		info, err := j.Lstat(src)
		if err != nil {
			return nil, err
		}
		dst, err := fsjail.Join(to, src.Base())
		if err != nil {
			return nil, err
		}
		it := plannedItem{src: src, dst: dst, info: info, policy: pol.For(src)}
		if dst == src {
			if op == ActMove {
				it.policy = PolicySkip // already there
				it.exists = &info
				items = append(items, it)
				continue
			}
			// copy onto itself (duplicate): pick the fresh name now so the
			// read lock on src and the write lock on dst do not collide
			it.dst, err = UniqueName(j, to, src.Base(), info.IsDir)
			if err != nil {
				return nil, err
			}
			it.renamed = true
			items = append(items, it)
			continue
		}
		if existing, err := j.Lstat(dst); err == nil {
			it.exists = &existing
			if it.policy == PolicyFail {
				conflicts = append(conflicts, Conflict{Path: string(dst), Source: string(src), Existing: statOf(existing), Incoming: statOf(info)})
			}
		} else if !isNotFound(err) {
			return nil, err
		}
		items = append(items, it)
	}
	if len(conflicts) > 0 {
		return nil, &ConflictError{Conflicts: conflicts}
	}
	return items, nil
}

// Move moves paths into folder to (SPEC §8.5 "Copy / Move").
func (s *Service) Move(ctx context.Context, j *fsjail.Jail, userID string, paths []fsjail.VPath, to fsjail.VPath, pol ConflictPolicy) (*Result, error) {
	return s.move(ctx, j, userID, paths, to, pol, true)
}

func (s *Service) move(ctx context.Context, j *fsjail.Jail, userID string, paths []fsjail.VPath, to fsjail.VPath, pol ConflictPolicy, recordUndo bool) (*Result, error) {
	items, err := s.plan(j, paths, to, pol, ActMove)
	if err != nil {
		return nil, err
	}
	lockPaths := make([]fsjail.VPath, 0, 2*len(items))
	for _, it := range items {
		lockPaths = append(lockPaths, it.src, it.dst)
	}
	release, err := s.lock(userID, "move", fsjail.Write, lockPaths...)
	if err != nil {
		return nil, err
	}
	defer release()
	s.hook("move")
	// Merges copy nothing (rename per entry) but still walk the tree:
	// bound them like copies.
	for _, it := range items {
		if it.exists != nil && it.policy == PolicyOverwrite && it.info.IsDir && it.exists.IsDir {
			if _, _, err := s.estimate(ctx, j, []fsjail.VPath{it.src}); err != nil {
				return nil, err
			}
		}
	}
	res := &Result{Results: []ItemResult{}}
	var undoItems []UndoItem
	action := ActMove
	if !recordUndo {
		action = ActUndo
	}
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
		// An overwrite destroys the destination's bytes (file over file, or
		// files inside a merged folder): account the real delta (review B, D-2).
		var before int64
		account := r.Status == "overwritten" || r.Status == "merged"
		if account {
			b1, ok1 := s.sizeOf(ctx, j, dst)
			b2, ok2 := s.sizeOf(ctx, j, it.src)
			before = b1 + b2
			if !ok1 || !ok2 {
				s.markQuotaDirty(ctx, userID)
				account = false
			}
		}
		if r.Status == "merged" {
			if err := s.mergeMove(ctx, j, userID, it.src, dst, it.policy); err != nil {
				return res, err
			}
		} else {
			if err := j.MoveTree(ctx, it.src, dst, nil); err != nil {
				return res, err
			}
			if err := s.repo.WithTx(ctx, func(tx db.Repository) error {
				return UpdateReferences(ctx, tx, userID, string(it.src), string(dst))
			}); err != nil {
				s.logger.ErrorContext(ctx, "reference update failed; rolling back move", slog.String("path", string(it.src)), slog.String("error", err.Error()))
				if rerr := j.MoveTree(ctx, dst, it.src, nil); rerr != nil {
					s.logger.ErrorContext(ctx, "move rollback failed", slog.String("path", string(dst)), slog.String("error", rerr.Error()))
				}
				return res, fmt.Errorf("files: move references: %w", err)
			}
		}
		if account && s.quota != nil {
			a1, ok1 := s.sizeOf(ctx, j, dst)
			a2, ok2 := s.sizeOf(ctx, j, it.src)
			if !ok1 || !ok2 {
				s.markQuotaDirty(ctx, userID)
			} else if delta := a1 + a2 - before; delta != 0 {
				if qerr := s.quota.Apply(ctx, nil, userID, delta, 0, 0); qerr != nil {
					s.logger.ErrorContext(ctx, "quota update after move failed", slog.String("user", userID), slog.String("error", qerr.Error()))
					s.markQuotaDirty(ctx, userID)
				}
			}
		}
		s.logActivity(ctx, s.repo, userID, action, string(it.src), string(dst), map[string]any{"op": ActMove, "status": r.Status, "isDir": it.info.IsDir}, "")
		s.changed(ctx, userID, it.src, it.info.IsDir)
		if r.Status == "overwritten" || r.Status == "merged" {
			s.changed(ctx, userID, dst, it.info.IsDir)
		}
		s.invalidateDir(userID, to)
		res.Results = append(res.Results, r)
		if r.Status == "ok" || r.Status == "renamed" {
			undoItems = append(undoItems, UndoItem{From: string(dst), To: string(it.src)})
		}
	}
	if recordUndo && len(undoItems) > 0 {
		s.withUndo(res, userID, UndoInfo{Kind: "move", Items: undoItems})
	}
	return res, nil
}

// moveExact moves from to the exact path to (undo of a move, which may have
// given the item a "(n)" name). The destination must be free.
func (s *Service) moveExact(ctx context.Context, j *fsjail.Jail, userID string, from, to fsjail.VPath) (*ItemResult, error) {
	release, err := s.lock(userID, "undo", fsjail.Write, from, to)
	if err != nil {
		return nil, err
	}
	defer release()
	info, err := j.Lstat(from)
	if err != nil {
		return nil, err
	}
	if existing, err := j.Lstat(to); err == nil {
		return nil, &ConflictError{Conflicts: []Conflict{{Path: string(to), Source: string(from), Existing: statOf(existing), Incoming: statOf(info)}}}
	} else if !isNotFound(err) {
		return nil, err
	}
	if dir := to.Dir(); !dir.IsRoot() {
		if err := j.MkdirAll(dir); err != nil {
			return nil, err
		}
	}
	if err := j.MoveTree(ctx, from, to, nil); err != nil {
		return nil, err
	}
	if err := s.repo.WithTx(ctx, func(tx db.Repository) error {
		if err := UpdateReferences(ctx, tx, userID, string(from), string(to)); err != nil {
			return err
		}
		s.logActivity(ctx, tx, userID, ActUndo, string(from), string(to), map[string]any{"op": ActMove}, "")
		return nil
	}); err != nil {
		if rerr := j.MoveTree(ctx, to, from, nil); rerr != nil {
			s.logger.ErrorContext(ctx, "undo move rollback failed", slog.String("path", string(to)), slog.String("error", rerr.Error()))
		}
		return nil, fmt.Errorf("files: undo move references: %w", err)
	}
	s.changed(ctx, userID, from, info.IsDir)
	s.invalidateDir(userID, to.Dir())
	return &ItemResult{Path: string(from), To: string(to), Status: "ok", IsDir: info.IsDir}, nil
}

// mergeMove moves the contents of src into the existing folder dst entry by
// entry; files at dst are overwritten (policy overwrite), subfolders merge
// recursively. src is removed when it ends up empty.
func (s *Service) mergeMove(ctx context.Context, j *fsjail.Jail, userID string, src, dst fsjail.VPath, pol Policy) error {
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
		switch {
		case lerr != nil && !isNotFound(lerr):
			return lerr
		case lerr == nil && e.IsDir && existing.IsDir:
			if err := s.mergeMove(ctx, j, userID, from, target, pol); err != nil {
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
		}
		if err := j.MoveTree(ctx, from, target, nil); err != nil {
			return err
		}
		if err := s.repo.WithTx(ctx, func(tx db.Repository) error {
			return UpdateReferences(ctx, tx, userID, string(from), string(target))
		}); err != nil {
			s.logger.ErrorContext(ctx, "reference update failed; rolling back merged entry", slog.String("path", string(from)), slog.String("error", err.Error()))
			if rerr := j.MoveTree(ctx, target, from, nil); rerr != nil {
				s.logger.ErrorContext(ctx, "merge rollback failed", slog.String("path", string(target)), slog.String("error", rerr.Error()))
			}
			return fmt.Errorf("files: merge references: %w", err)
		}
		s.changed(ctx, userID, from, e.IsDir)
	}
	// The merged folder itself disappears; its own metadata goes with it.
	if err := j.Remove(src); err != nil {
		if errors.Is(err, fsjail.ErrNotEmpty) {
			return nil // skipped entries remain; keep the folder
		}
		return err
	}
	return s.repo.WithTx(ctx, func(tx db.Repository) error {
		return DeleteReferences(ctx, tx, userID, string(src))
	})
}

// Duplicate copies p next to itself as "name (n).ext" (SPEC §8.5).
func (s *Service) Duplicate(ctx context.Context, j *fsjail.Jail, userID string, p fsjail.VPath) (*Result, error) {
	if p.IsRoot() {
		return nil, &ValidationError{Reason: "root", Path: string(p), Fields: map[string]string{"path": "root"}}
	}
	res, err := s.copy(ctx, j, userID, []fsjail.VPath{p}, p.Dir(), ConflictPolicy{Default: PolicyRename}, ActDuplicate)
	if err != nil {
		return nil, err
	}
	return res, nil
}

// Symlink creates link → target (target relative, resolved inside the root,
// SPEC §5.4). Undo removes the link.
func (s *Service) Symlink(ctx context.Context, j *fsjail.Jail, userID string, link fsjail.VPath, target string) (*Result, error) {
	if link.IsRoot() {
		return nil, &ValidationError{Reason: "root", Path: string(link), Fields: map[string]string{"path": "root"}}
	}
	warnings, err := fsjail.ValidateName(link.Base())
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(target, "/") {
		return nil, &ValidationError{Reason: "target_absolute", Path: string(link), Fields: map[string]string{"target": "must be relative"}}
	}
	release, err := s.lock(userID, "symlink", fsjail.Write, link)
	if err != nil {
		return nil, err
	}
	defer release()
	s.hook("symlink")
	if existing, err := j.Lstat(link); err == nil {
		return nil, &ConflictError{Conflicts: []Conflict{{Path: string(link), Existing: statOf(existing)}}}
	} else if !isNotFound(err) {
		return nil, err
	}
	if err := j.Symlink(target, link); err != nil {
		return nil, err
	}
	s.logActivity(ctx, s.repo, userID, ActSymlink, string(link), "", map[string]any{"target": target}, "")
	s.invalidateDir(userID, link.Dir())
	if warnings == nil {
		warnings = []string{}
	}
	res := &Result{Results: []ItemResult{{Path: string(link), Status: "ok", Warnings: warnings}}, Warnings: warnings}
	s.withUndo(res, userID, UndoInfo{Kind: "delete_created", Created: []string{string(link)}})
	return res, nil
}

// ParseMode parses "0644", "644" or "u=rwx,g=rx,o=" style is not supported;
// only octal. Bits beyond rwx (setuid/setgid/sticky) are dropped.
func ParseMode(s string) (fs.FileMode, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 4 {
		return 0, false
	}
	n, err := strconv.ParseUint(s, 8, 32)
	if err != nil {
		return 0, false
	}
	return fs.FileMode(n) & 0o777, true
}

// ChmodRequest is the input of Chmod.
type ChmodRequest struct {
	Paths     []fsjail.VPath
	Mode      fs.FileMode
	Recursive bool
	DirMode   *fs.FileMode // recursive: mode for folders (default: Mode plus x where r)
}

// Chmod sets permission bits (SPEC §8.5 "Chmod"). Recursive requests are
// bounded by the synchronous entry limit until the task engine exists.
func (s *Service) Chmod(ctx context.Context, j *fsjail.Jail, userID string, req ChmodRequest) (*Result, error) {
	if len(req.Paths) == 0 {
		return nil, &ValidationError{Reason: "empty", Fields: map[string]string{"paths": "required"}}
	}
	paths := dedupe(req.Paths)
	for _, p := range paths {
		if p.IsRoot() {
			return nil, &ValidationError{Reason: "root", Path: string(p), Fields: map[string]string{"paths": "root"}}
		}
	}
	release, err := s.lock(userID, "chmod", fsjail.Write, paths...)
	if err != nil {
		return nil, err
	}
	defer release()
	s.hook("chmod")
	dirMode := req.Mode
	if req.DirMode != nil {
		dirMode = *req.DirMode & 0o777
	} else {
		// folders need x where r is set to stay traversable
		for _, bit := range []fs.FileMode{0o400, 0o040, 0o004} {
			if req.Mode&bit != 0 {
				dirMode |= bit >> 2
			}
		}
	}
	if req.Recursive {
		if _, _, err := s.estimate(ctx, j, paths); err != nil {
			return nil, err
		}
	}
	res := &Result{Results: []ItemResult{}, Undo: nil}
	for _, p := range paths {
		info, err := j.Lstat(p)
		if err != nil {
			return res, err
		}
		if info.IsSymlink {
			res.Results = append(res.Results, ItemResult{Path: string(p), Status: "skipped", Warnings: []string{"symlink"}})
			continue
		}
		switch {
		case req.Recursive && info.IsDir:
			if err := j.ChmodRecursive(ctx, p, req.Mode, dirMode, nil); err != nil {
				return res, err
			}
		case info.IsDir:
			if err := j.Chmod(p, dirMode); err != nil {
				return res, err
			}
		default:
			if err := j.Chmod(p, req.Mode); err != nil {
				return res, err
			}
		}
		s.logActivity(ctx, s.repo, userID, ActChmod, string(p), "", map[string]any{"mode": fmt.Sprintf("%04o", req.Mode), "recursive": req.Recursive && info.IsDir}, "")
		s.invalidateDir(userID, p.Dir())
		if req.Recursive && info.IsDir {
			s.invalidateDir(userID, p)
		}
		res.Results = append(res.Results, ItemResult{Path: string(p), Status: "ok", IsDir: info.IsDir})
	}
	return res, nil
}

// Delete moves paths to the Recycle Bin (SPEC §8.5 "Delete ke Recycle Bin").
func (s *Service) Delete(ctx context.Context, j *fsjail.Jail, userID string, paths []fsjail.VPath) (*Result, error) {
	return s.delete(ctx, j, userID, paths, true)
}

func (s *Service) delete(ctx context.Context, j *fsjail.Jail, userID string, paths []fsjail.VPath, recordUndo bool) (*Result, error) {
	if s.trash == nil {
		return nil, errors.New("files: trash store not configured")
	}
	if len(paths) == 0 {
		return nil, &ValidationError{Reason: "empty", Fields: map[string]string{"paths": "required"}}
	}
	if len(paths) > MaxBatchItems {
		return nil, &ValidationError{Reason: "too_many", Fields: map[string]string{"paths": "max " + strconv.Itoa(MaxBatchItems)}}
	}
	paths = dedupe(paths)
	for _, p := range paths {
		if p.IsRoot() {
			return nil, &ValidationError{Reason: "root", Path: string(p), Fields: map[string]string{"paths": "root"}}
		}
	}
	// A nested selection (/a and /a/b) only needs the ancestor.
	paths = dropDescendants(paths)
	release, err := s.lock(userID, "delete", fsjail.Write, paths...)
	if err != nil {
		return nil, err
	}
	defer release()
	s.hook("delete")
	batchID := db.NewID()
	action := ActDelete
	if !recordUndo {
		action = ActUndo
	}
	res := &Result{Results: []ItemResult{}, BatchID: batchID}
	var undoItems []UndoTrashItem
	for _, p := range paths {
		info, err := j.Lstat(p)
		if err != nil {
			return res, err
		}
		row, err := s.trash.MoveToTrash(ctx, j, userID, p, TrashKind(p.Base(), info.IsDir), batchID)
		if err != nil {
			return res, err
		}
		// shares → disabled item_trashed arrives with the shares table (18a)
		s.logActivity(ctx, s.repo, userID, action, string(p), "", map[string]any{"op": ActDelete, "trashId": row.ID, "size": row.SizeBytes, "isDir": info.IsDir}, batchID)
		s.changed(ctx, userID, p, info.IsDir)
		res.Results = append(res.Results, ItemResult{Path: string(p), Status: "ok", IsDir: info.IsDir, TrashID: row.ID})
		undoItems = append(undoItems, UndoTrashItem{ID: row.ID, Path: string(p)})
	}
	if recordUndo && len(undoItems) > 0 {
		s.withUndo(res, userID, UndoInfo{Kind: "restore", BatchID: batchID, Trash: undoItems})
	}
	return res, nil
}

// dropDescendants removes paths that are inside another selected path.
func dropDescendants(paths []fsjail.VPath) []fsjail.VPath {
	out := make([]fsjail.VPath, 0, len(paths))
	for _, p := range paths {
		nested := false
		for _, q := range paths {
			if q != p && p.HasPrefix(q) {
				nested = true
				break
			}
		}
		if !nested {
			out = append(out, p)
		}
	}
	return out
}

// Undo executes the inverse of a previous operation (SPEC §8.5 "Undo").
// Tokens are single use; a changed state answers ErrUndoConflict.
func (s *Service) Undo(ctx context.Context, j *fsjail.Jail, userID, token string) (*Result, error) {
	info, ok := s.undo.Take(userID, token)
	if !ok {
		return nil, ErrUndoNotFound
	}
	var (
		res *Result
		err error
	)
	switch info.Kind {
	case "rename":
		p, perr := fsjail.Clean(info.Path)
		if perr != nil {
			return nil, perr
		}
		res, err = s.rename(ctx, j, userID, p, info.NewName, false)
	case "move":
		res = &Result{Results: []ItemResult{}}
		for _, it := range info.Items {
			from, ferr := fsjail.Clean(it.From)
			to, terr := fsjail.Clean(it.To)
			if ferr != nil || terr != nil {
				return nil, errors.Join(ferr, terr)
			}
			r, merr := s.moveExact(ctx, j, userID, from, to)
			if merr != nil {
				err = merr
				break
			}
			res.Results = append(res.Results, *r)
		}
	case "restore":
		res = &Result{Results: []ItemResult{}, BatchID: info.BatchID}
		for _, t := range info.Trash {
			p, perr := fsjail.Clean(t.Path)
			if perr != nil {
				return nil, perr
			}
			release, lerr := s.lock(userID, "restore", fsjail.Write, p)
			if lerr != nil {
				return res, lerr
			}
			_, rerr := s.trash.Restore(ctx, j, userID, trash.RestoreRequest{ID: t.ID, Dest: p})
			release()
			if rerr != nil {
				err = rerr
				break
			}
			s.logActivity(ctx, s.repo, userID, ActRestore, string(p), "", map[string]any{"trashId": t.ID, "undo": true}, info.BatchID)
			s.changed(ctx, userID, p, false)
			res.Results = append(res.Results, ItemResult{Path: string(p), Status: "ok", TrashID: t.ID})
		}
	case "delete_created":
		paths := make([]fsjail.VPath, 0, len(info.Created))
		for _, c := range info.Created {
			p, perr := fsjail.Clean(c)
			if perr != nil {
				return nil, perr
			}
			paths = append(paths, p)
		}
		res, err = s.delete(ctx, j, userID, paths, false)
	case "batch_rename":
		pairs := make([]renamePair, 0, len(info.Items))
		for _, it := range info.Items {
			from, ferr := fsjail.Clean(it.From)
			to, terr := fsjail.Clean(it.To)
			if ferr != nil || terr != nil {
				return nil, errors.Join(ferr, terr)
			}
			pairs = append(pairs, renamePair{from: from, to: to})
		}
		res, err = s.applyPairs(ctx, j, userID, pairs, false)
	default:
		return nil, ErrUndoNotFound
	}
	if err != nil {
		var ce *ConflictError
		var tce *trash.ConflictError
		if errors.As(err, &ce) || errors.As(err, &tce) || errors.Is(err, fsjail.ErrExists) || isNotFound(err) || errors.Is(err, trash.ErrGone) {
			return res, fmt.Errorf("%w: %w", ErrUndoConflict, err)
		}
		return res, err
	}
	res.Undo = nil
	res.UndoToken = ""
	return res, nil
}
