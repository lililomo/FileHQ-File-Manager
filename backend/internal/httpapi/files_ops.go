package httpapi

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/quota"
	"kppbwi.app/filemanager/internal/trash"
)

// mountFileOps registers the mutating file operations of Step 9a
// (SPEC §6.3 "Files", §8.5). Same gates as mountFiles.
func (s *Server) mountFileOps(api chi.Router) {
	if s.files == nil {
		return
	}
	api.Group(func(g chi.Router) {
		g.Use(OriginCheck(s.cfg.AppBaseURL))
		g.Use(s.RequireAuth)
		g.Use(s.RequireCSRF)
		g.Use(RequirePasswordChanged)
		g.Post("/files/rename", s.handleFilesRename)
		g.Post("/files/copy", s.handleFilesCopy)
		g.Post("/files/move", s.handleFilesMove)
		g.Post("/files/duplicate", s.handleFilesDuplicate)
		g.Post("/files/symlink", s.handleFilesSymlink)
		g.Post("/files/chmod", s.handleFilesChmod)
		g.Post("/files/delete", s.handleFilesDelete)
		g.Post("/files/batch-rename/preview", s.handleBatchRenamePreview)
		g.Post("/files/batch-rename", s.handleBatchRename)
		g.Post("/files/undo", s.handleFilesUndo)
		g.Post("/files/delete-permanent", s.handleFilesDeletePermanent)
		g.Post("/files/dir-size", s.handleFilesDirSize)
		g.Post("/files/checksum", s.handleFilesChecksum)
		g.Post("/files/checksum/verify", s.handleFilesChecksumVerify)
	})
}

// writeOpsError maps files/trash errors to the envelope (SPEC §6.2).
func (s *Server) writeOpsError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		ce  *files.ConflictError
		te  *files.TooLargeError
		ve  *files.ValidationError
		be  *files.BatchRenameError
		qe  *quota.ExceededError
		fe  *files.FileTooLargeError
		rce *files.RestoreConflictError
		tce *trash.ConflictError
	)
	switch {
	case errors.As(err, &qe):
		WriteError(w, r, CodeQuotaExceeded, 0, quotaMessage(qe), map[string]any{"reason": qe.Reason, "used": qe.Used, "quota": qe.Limit, "needed": qe.Needed, "free": qe.Free})
	case errors.As(err, &fe):
		WriteError(w, r, CodeFileTooLarge, 0, "file too large to hash synchronously; it needs the task engine", map[string]any{"reason": "task_required", "size": fe.Size, "max": fe.Max})
	case errors.As(err, &rce):
		details := map[string]any{"conflicts": rce.Conflicts}
		if len(rce.Conflicts) > 0 {
			details["path"] = rce.Conflicts[0].Path
			details["existing"] = rce.Conflicts[0].Existing
			details["incoming"] = rce.Conflicts[0].Incoming
		}
		WriteError(w, r, CodeAlreadyExists, 0, fsMessages[CodeAlreadyExists], details)
	case errors.As(err, &tce):
		WriteError(w, r, CodeAlreadyExists, 0, fsMessages[CodeAlreadyExists], map[string]any{"conflicts": []trash.ConflictError{*tce}, "path": tce.Path, "existing": tce.Existing, "incoming": tce.Incoming})
	case errors.As(err, &ce):
		details := map[string]any{"conflicts": ce.Conflicts}
		if len(ce.Conflicts) > 0 {
			details["path"] = ce.Conflicts[0].Path
			details["existing"] = ce.Conflicts[0].Existing
			details["incoming"] = ce.Conflicts[0].Incoming
		}
		WriteError(w, r, CodeAlreadyExists, 0, fsMessages[CodeAlreadyExists], details)
	case errors.As(err, &te):
		WriteError(w, r, CodePayloadTooLarge, 0, "operation too large to run synchronously; it needs the task engine",
			map[string]any{"reason": "task_required", "cause": te.Reason, "entries": te.Entries, "bytes": te.Bytes, "maxEntries": te.MaxEntries, "maxBytes": te.MaxBytes})
	case errors.As(err, &ve):
		details := map[string]any{"reason": ve.Reason}
		if ve.Path != "" {
			details["path"] = ve.Path
		}
		if len(ve.Fields) > 0 {
			details["fields"] = ve.Fields
		}
		WriteError(w, r, CodeValidationFailed, 0, fsMessages[CodeValidationFailed], details)
	case errors.As(err, &be):
		WriteError(w, r, CodeConflict, 0, "batch rename has collisions or invalid names", map[string]any{"reason": "preview_not_clean", "items": be.Items})
	case errors.Is(err, files.ErrUndoNotFound):
		WriteError(w, r, CodeNotFound, 0, "undo token unknown or expired", map[string]any{"reason": "undo_expired"})
	case errors.Is(err, files.ErrUndoConflict):
		WriteError(w, r, CodeConflict, 0, "state changed; cannot undo", map[string]any{"reason": "undo_conflict"})
	case errors.Is(err, trash.ErrNotFound):
		WriteError(w, r, CodeNotFound, 0, "trash item not found", map[string]any{"reason": "trash_item"})
	case errors.Is(err, trash.ErrGone):
		WriteError(w, r, CodeConflict, 0, "trash item already restored or purged", map[string]any{"reason": "trash_gone"})
	case errors.Is(err, r.Context().Err()) && r.Context().Err() != nil:
		WriteError(w, r, CodeInternal, http.StatusServiceUnavailable, "request cancelled", nil)
	case WriteFSError(w, r, err):
	default:
		s.logger.ErrorContext(r.Context(), "file operation failed", slog.String("error", err.Error()))
		WriteError(w, r, CodeInternal, 0, "internal server error", nil)
	}
}

// cleanPaths validates a batch of client paths.
func cleanPaths(w http.ResponseWriter, r *http.Request, raw []string) ([]fsjail.VPath, bool) {
	if len(raw) == 0 {
		WriteError(w, r, CodeValidationFailed, 0, "paths are required", map[string]any{"fields": map[string]any{"paths": "required"}})
		return nil, false
	}
	if len(raw) > files.MaxBatchItems {
		WriteError(w, r, CodeValidationFailed, 0, "too many paths", map[string]any{"fields": map[string]any{"paths": "too_many"}, "max": files.MaxBatchItems})
		return nil, false
	}
	out := make([]fsjail.VPath, 0, len(raw))
	for _, p := range raw {
		v, ok := cleanPath(w, r, p)
		if !ok {
			return nil, false
		}
		out = append(out, v)
	}
	return out, true
}

// opView adds the Item of path (when it still exists) to a Result.
func (s *Server) opView(r *http.Request, j *fsjail.Jail, userID string, res *files.Result, itemPath fsjail.VPath) map[string]any {
	out := map[string]any{"results": res.Results, "undo": res.Undo}
	if res.UndoToken != "" {
		out["undoToken"] = res.UndoToken
	}
	if res.BatchID != "" {
		out["batchId"] = res.BatchID
	}
	if res.Warnings != nil {
		out["warnings"] = res.Warnings
	}
	if itemPath != "" {
		if it, err := s.listing.Stat(j, userID, scopeOwn, itemPath); err == nil {
			out["item"] = it
		} else {
			s.logger.DebugContext(r.Context(), "stat after operation failed", slog.String("path", string(itemPath)), slog.String("error", err.Error()))
		}
	}
	return out
}

func quotaMessage(e *quota.ExceededError) string {
	if e.Reason == "disk" {
		return "not enough free disk space on the server"
	}
	return "storage quota exceeded"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

type renameRequest struct {
	Path     string `json:"path"`
	NewName  string `json:"newName,omitempty"`
	NewName2 string `json:"new_name,omitempty"`
}

func (s *Server) handleFilesRename(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req renameRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	p, ok := cleanPath(w, r, req.Path)
	if !ok {
		return
	}
	newName := firstNonEmpty(req.NewName, req.NewName2)
	if newName == "" {
		WriteError(w, r, CodeValidationFailed, 0, "newName is required", map[string]any{"fields": map[string]any{"newName": "required"}})
		return
	}
	if ext, blocked := s.blockedExtension(newName); blocked {
		s.writeBlockedExtension(w, r, p, ext)
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	res, err := s.files.Rename(r.Context(), j, u.ID, p, newName)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, s.opView(r, j, u.ID, res, fsjail.VPath(res.Results[0].To)))
}

type transferRequest struct {
	Paths       []string          `json:"paths,omitempty"`
	Items       []string          `json:"items,omitempty"`
	To          string            `json:"to,omitempty"`
	Dest        string            `json:"dest,omitempty"`
	OnConflict  string            `json:"onConflict,omitempty"`
	OnConflict2 string            `json:"on_conflict,omitempty"`
	PerItem     map[string]string `json:"perItem,omitempty"`
	PerItem2    map[string]string `json:"per_item,omitempty"`
}

func (s *Server) parseTransfer(w http.ResponseWriter, r *http.Request) ([]fsjail.VPath, fsjail.VPath, files.ConflictPolicy, bool) {
	var req transferRequest
	if !ReadJSON(w, r, &req) {
		return nil, "", files.ConflictPolicy{}, false
	}
	raw := req.Paths
	if len(raw) == 0 {
		raw = req.Items
	}
	paths, ok := cleanPaths(w, r, raw)
	if !ok {
		return nil, "", files.ConflictPolicy{}, false
	}
	to, ok := cleanPath(w, r, firstNonEmpty(req.To, req.Dest))
	if !ok {
		return nil, "", files.ConflictPolicy{}, false
	}
	pol := files.ConflictPolicy{}
	def, ok := files.ParsePolicy(firstNonEmpty(req.OnConflict, req.OnConflict2))
	if !ok {
		WriteError(w, r, CodeValidationFailed, 0, "invalid conflict policy", map[string]any{"fields": map[string]any{"onConflict": "must be fail, overwrite, skip or rename"}})
		return nil, "", files.ConflictPolicy{}, false
	}
	pol.Default = def
	per := req.PerItem
	if len(per) == 0 {
		per = req.PerItem2
	}
	if len(per) > 0 {
		pol.PerItem = map[string]files.Policy{}
		for k, v := range per {
			kp, err := fsjail.Clean(k)
			if err != nil {
				continue
			}
			pp, ok := files.ParsePolicy(v)
			if !ok {
				WriteError(w, r, CodeValidationFailed, 0, "invalid conflict policy", map[string]any{"fields": map[string]any{"perItem." + k: "must be fail, overwrite, skip or rename"}})
				return nil, "", files.ConflictPolicy{}, false
			}
			pol.PerItem[string(kp)] = pp
		}
	}
	return paths, to, pol, true
}

func (s *Server) handleFilesCopy(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	paths, to, pol, ok := s.parseTransfer(w, r)
	if !ok {
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	res, err := s.files.Copy(r.Context(), j, u.ID, paths, to, pol)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, s.opView(r, j, u.ID, res, ""))
}

func (s *Server) handleFilesMove(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	paths, to, pol, ok := s.parseTransfer(w, r)
	if !ok {
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	res, err := s.files.Move(r.Context(), j, u.ID, paths, to, pol)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, s.opView(r, j, u.ID, res, ""))
}

type pathRequest struct {
	Path string `json:"path"`
}

func (s *Server) handleFilesDuplicate(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req pathRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	p, ok := cleanPath(w, r, req.Path)
	if !ok {
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	res, err := s.files.Duplicate(r.Context(), j, u.ID, p)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, s.opView(r, j, u.ID, res, fsjail.VPath(res.Results[0].To)))
}

type symlinkRequest struct {
	Path   string `json:"path"`
	Target string `json:"target"`
}

func (s *Server) handleFilesSymlink(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req symlinkRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	link, ok := cleanPath(w, r, req.Path)
	if !ok {
		return
	}
	if req.Target == "" {
		WriteError(w, r, CodeValidationFailed, 0, "target is required", map[string]any{"fields": map[string]any{"target": "required"}})
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	res, err := s.files.Symlink(r.Context(), j, u.ID, link, req.Target)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusCreated, s.opView(r, j, u.ID, res, link))
}

type chmodRequest struct {
	Paths     []string `json:"paths"`
	Mode      string   `json:"mode"`
	Recursive bool     `json:"recursive,omitempty"`
	DirMode   string   `json:"dirMode,omitempty"`
	DirMode2  string   `json:"dir_mode,omitempty"`
}

func (s *Server) handleFilesChmod(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req chmodRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	paths, ok := cleanPaths(w, r, req.Paths)
	if !ok {
		return
	}
	mode, ok := files.ParseMode(req.Mode)
	if !ok {
		WriteError(w, r, CodeValidationFailed, 0, "invalid mode", map[string]any{"fields": map[string]any{"mode": "octal like 0644"}})
		return
	}
	cr := files.ChmodRequest{Paths: paths, Mode: mode, Recursive: req.Recursive}
	if dm := firstNonEmpty(req.DirMode, req.DirMode2); dm != "" {
		d, ok := files.ParseMode(dm)
		if !ok {
			WriteError(w, r, CodeValidationFailed, 0, "invalid dirMode", map[string]any{"fields": map[string]any{"dirMode": "octal like 0755"}})
			return
		}
		cr.DirMode = &d
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	res, err := s.files.Chmod(r.Context(), j, u.ID, cr)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, s.opView(r, j, u.ID, res, ""))
}

type pathsRequest struct {
	Paths []string `json:"paths"`
}

func (s *Server) handleFilesDelete(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req pathsRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	paths, ok := cleanPaths(w, r, req.Paths)
	if !ok {
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	res, err := s.files.Delete(r.Context(), j, u.ID, paths)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, s.opView(r, j, u.ID, res, ""))
}

type batchRenameRequest struct {
	Paths []string         `json:"paths"`
	Rule  files.RenameRule `json:"rule"`
}

func (s *Server) handleBatchRenamePreview(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req batchRenameRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	paths, ok := cleanPaths(w, r, req.Paths)
	if !ok {
		return
	}
	j, _, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	items, err := s.files.BatchRenamePreview(r.Context(), j, paths, req.Rule)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	clean := true
	for _, it := range items {
		if it.Status != "ok" {
			clean = false
			break
		}
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": items, "clean": clean})
}

func (s *Server) handleBatchRename(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req batchRenameRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	paths, ok := cleanPaths(w, r, req.Paths)
	if !ok {
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	res, preview, err := s.files.BatchRename(r.Context(), j, u.ID, paths, req.Rule)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	out := s.opView(r, j, u.ID, res, "")
	out["items"] = preview
	WriteJSON(w, http.StatusOK, out)
}

type undoRequest struct {
	Token  string `json:"token,omitempty"`
	Token2 string `json:"undoToken,omitempty"`
	Token3 string `json:"undo_token,omitempty"`
}

func (s *Server) handleFilesUndo(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req undoRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	token := firstNonEmpty(req.Token, req.Token2, req.Token3)
	if token == "" {
		WriteError(w, r, CodeValidationFailed, 0, "token is required", map[string]any{"fields": map[string]any{"token": "required"}})
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	res, err := s.files.Undo(r.Context(), j, u.ID, token)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, s.opView(r, j, u.ID, res, ""))
}
