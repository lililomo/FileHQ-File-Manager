package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/tasks"
)

// Duplicate finder endpoints (SPEC §6.3 "me", §7.1 duplicate_finder, §8.7
// "Duplikat", Step 23c). Owner scope only; mounted in mountMeActivity.

// dupEnqueueMu makes the "already running" check and the enqueue atomic
// within the process (two quick clicks never start two scans).
var dupEnqueueMu sync.Mutex

type duplicatesStartRequest struct {
	Path string `json:"path"`
}

type linkGroupRequest struct {
	Keep  string   `json:"keep"`
	Paths []string `json:"paths"`
}

// duplicatesSymlinkRequest: one group as {keep, paths} or several as
// {groups:[{keep, paths}]} (one Recycle Bin batch, one undo token).
type duplicatesSymlinkRequest struct {
	Keep   string             `json:"keep"`
	Paths  []string           `json:"paths"`
	Groups []linkGroupRequest `json:"groups"`
}

// duplicatesView is the body of GET /me/duplicates.
type duplicatesView struct {
	TaskID string `json:"taskId"`
	*files.DuplicateReport
}

func (s *Server) handleDuplicatesStart(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req duplicatesStartRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	if req.Path == "" {
		req.Path = "/"
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
	info, err := j.Stat(p)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	if !info.IsDir {
		WriteError(w, r, CodeValidationFailed, 0, "path is not a folder", map[string]any{"reason": "not_a_directory", "fields": map[string]any{"path": "not_a_directory"}})
		return
	}
	dupEnqueueMu.Lock()
	defer dupEnqueueMu.Unlock()
	active, err := s.store.ListUserActiveTasksOfType(r.Context(), u.ID, files.TaskDuplicates)
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	if len(active) > 0 {
		WriteError(w, r, CodeConflict, 0, "a duplicate scan is already queued or running", map[string]any{"reason": "already_running", "taskId": active[0].ID})
		return
	}
	name := p.Base()
	if p.IsRoot() {
		name = "/"
	}
	t, err := s.tasks.Enqueue(r.Context(), tasks.EnqueueRequest{UserID: u.ID, Type: files.TaskDuplicates, Title: "Cari duplikat di " + name,
		Payload: files.DuplicatesPayload{Path: string(p)}})
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusAccepted, map[string]any{"task": t})
}

// handleDuplicatesLatest serves the newest finished scan of the last 24 h,
// pruned of copies that are gone or changed since.
func (s *Server) handleDuplicatesLatest(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	row, err := s.store.LatestUserTaskOfTypeState(r.Context(), u.ID, files.TaskDuplicates, string(tasks.Done))
	if errors.Is(err, sql.ErrNoRows) {
		WriteError(w, r, CodeNotFound, 0, "no duplicate scan in the last 24 hours", map[string]any{"reason": "no_result"})
		return
	}
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	var finished time.Time
	if row.FinishedAt != nil {
		finished, _ = db.ParseTime(*row.FinishedAt)
	}
	if row.ResultJson == nil || finished.IsZero() || time.Since(finished) > files.DuplicateResultTTL {
		WriteError(w, r, CodeNotFound, 0, "no duplicate scan in the last 24 hours", map[string]any{"reason": "no_result"})
		return
	}
	var rep files.DuplicateReport
	if err := json.Unmarshal([]byte(*row.ResultJson), &rep); err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	if rep.Groups == nil {
		rep.Groups = []files.DuplicateGroup{}
	}
	files.PruneDuplicateReport(j, &rep)
	WriteJSON(w, http.StatusOK, duplicatesView{TaskID: row.ID, DuplicateReport: &rep})
}

func (s *Server) handleDuplicatesSymlink(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req duplicatesSymlinkRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	in := req.Groups
	if req.Keep != "" || len(req.Paths) > 0 {
		in = append([]linkGroupRequest{{Keep: req.Keep, Paths: req.Paths}}, in...)
	}
	if len(in) == 0 {
		WriteError(w, r, CodeValidationFailed, 0, "keep and paths are required", map[string]any{"fields": map[string]any{"keep": "required", "paths": "required"}})
		return
	}
	groups := make([]files.LinkGroup, 0, len(in))
	for _, g := range in {
		if g.Keep == "" {
			WriteError(w, r, CodeValidationFailed, 0, "keep is required", map[string]any{"fields": map[string]any{"keep": "required"}})
			return
		}
		keep, ok := cleanPath(w, r, g.Keep)
		if !ok {
			return
		}
		paths, ok := cleanPaths(w, r, g.Paths)
		if !ok {
			return
		}
		groups = append(groups, files.LinkGroup{Keep: keep, Paths: paths})
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	res, err := s.files.ReplaceWithSymlinks(r.Context(), j, u.ID, groups)
	if err != nil {
		var fe *files.FileTooLargeError
		if errors.As(err, &fe) {
			WriteError(w, r, CodeFileTooLarge, 0, "too many bytes to verify in one request", map[string]any{"reason": "too_large", "size": fe.Size, "max": fe.Max})
			return
		}
		s.writeOpsError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, s.opView(r, j, ownScope(u), res, fsjail.VPath("")))
}
