package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/listing"
	"kppbwi.app/filemanager/internal/tasks"
)

// mountMeActivity registers Recent, the activity journal and the storage
// analysis (SPEC §6.3 "me", §8.7, §8.16). S+P, CSRF on writes.
func (s *Server) mountMeActivity(api chi.Router) {
	if s.files == nil {
		return
	}
	api.Group(func(g chi.Router) {
		g.Use(OriginCheck(s.cfg.AppBaseURL))
		g.Use(s.RequireAuth)
		g.Use(s.RequireCSRF)
		g.Use(RequirePasswordChanged)
		g.Get("/me/recent", s.handleMeRecent)
		g.Post("/me/recent", s.handleMeRecentRecord)
		g.Get("/me/activity", s.handleMeActivity)
		g.Delete("/me/activity", s.handleMeActivityClear)
		g.Get("/me/storage-analysis", s.handleStorageAnalysis)
		g.Post("/me/storage-analysis", s.handleStorageAnalysis)
		if s.tasks != nil {
			g.Get("/me/duplicates", s.handleDuplicatesLatest)
			g.Post("/me/duplicates", s.handleDuplicatesStart)
			g.Post("/me/duplicates/symlink", s.handleDuplicatesSymlink)
		}
	})
}

// annotateItems fills favorite/pin/note/tag/media on items that did not come
// from a listing page (stat, recent).
func (s *Server) annotateItems(r *http.Request, userID string, items []*listing.Item) {
	if s.listing == nil || s.listing.Meta() == nil || len(items) == 0 {
		return
	}
	byDir := map[fsjail.VPath][]*listing.Item{}
	for _, it := range items {
		d := fsjail.VPath(it.Path).Dir()
		byDir[d] = append(byDir[d], it)
	}
	for d, group := range byDir {
		if err := s.listing.Meta().Annotate(r.Context(), userID, d, group); err != nil {
			s.logger.WarnContext(r.Context(), "annotate failed", "dir", string(d), "error", err.Error())
		}
	}
}

func (s *Server) handleMeRecent(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > files.MaxRecent {
			WriteError(w, r, CodeValidationFailed, 0, "limit must be 1..200", map[string]any{"fields": map[string]any{"limit": "1..200"}})
			return
		}
		limit = n
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	entries, err := s.files.Recent(r.Context(), u.ID, limit)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	type recentItem struct {
		*listing.Item
		Location string `json:"location"`
		Action   string `json:"action"`
		At       string `json:"at"`
	}
	out := make([]recentItem, 0, len(entries))
	items := make([]*listing.Item, 0, len(entries))
	for _, e := range entries {
		p, err := fsjail.Clean(e.Path)
		if err != nil {
			continue
		}
		info, err := j.Lstat(p)
		if err != nil {
			continue // only items that still exist (SPEC §8.7)
		}
		it := listing.FromInfo(info)
		items = append(items, it)
		out = append(out, recentItem{Item: it, Location: string(p.Dir()), Action: e.Action, At: e.At})
	}
	s.annotateItems(r, u.ID, items)
	WriteJSON(w, http.StatusOK, map[string]any{"items": out, "total": len(out), "windowDays": int(files.RecentWindow.Hours() / 24)})
}

// handleMeRecentRecord logs an "open" (viewer, Quick Look, editor open) so
// Recent reflects reads, not just writes.
func (s *Server) handleMeRecentRecord(w http.ResponseWriter, r *http.Request) {
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
	if p.IsRoot() {
		WriteError(w, r, CodeValidationFailed, 0, "path must be an item", map[string]any{"fields": map[string]any{"path": "root_not_allowed"}})
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	info, err := j.Lstat(p)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	s.files.RecordActivity(r.Context(), u.ID, files.ActOpen, string(p), "", map[string]any{"isDir": info.IsDir, "size": info.Size})
	w.WriteHeader(http.StatusNoContent)
}

func parseDay(raw string, endOfDay bool) (string, bool) {
	if raw == "" {
		return "", true
	}
	t, err := time.Parse("2006-01-02", raw)
	if err != nil {
		t, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return "", false
		}
		return db.FormatTime(t), true
	}
	if endOfDay {
		t = t.Add(24 * time.Hour)
	}
	return db.FormatTime(t), true
}

func (s *Server) handleMeActivity(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fields := map[string]any{}
	f := db.ActivityFilter{Action: strings.TrimSpace(q.Get("action")), Query: strings.TrimSpace(q.Get("q"))}
	if len(f.Action) > 64 {
		fields["action"] = "too_long"
	}
	if len(f.Query) > 255 {
		fields["q"] = "too_long"
	}
	var ok bool
	if f.From, ok = parseDay(q.Get("from"), false); !ok {
		fields["from"] = "invalid"
	}
	if f.To, ok = parseDay(q.Get("to"), true); !ok {
		fields["to"] = "invalid"
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > files.MaxActivityPage {
			fields["limit"] = "1..200"
		} else {
			f.Limit = int64(n)
		}
	}
	if raw := q.Get("cursor"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 {
			fields["cursor"] = "invalid"
		} else {
			f.BeforeID = n
		}
	}
	if len(fields) > 0 {
		WriteError(w, r, CodeValidationFailed, 0, "invalid query parameters", map[string]any{"fields": fields})
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	page, err := s.files.ListActivity(r.Context(), u.ID, f)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	type row struct {
		files.ActivityEntry
		Exists bool `json:"exists"`
	}
	rows := make([]row, 0, len(page.Items))
	for _, e := range page.Items {
		x := row{ActivityEntry: e}
		if e.Path != nil {
			if p, err := fsjail.Clean(*e.Path); err == nil {
				if _, err := j.Lstat(p); err == nil {
					x.Exists = true
				}
			}
		}
		rows = append(rows, x)
	}
	WriteJSON(w, http.StatusOK, map[string]any{"items": rows, "nextCursor": page.NextCursor, "retentionDays": s.cfg.Runtime().ActivityRetentionDays})
}

func (s *Server) handleMeActivityClear(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	n, err := s.files.ClearActivity(r.Context(), u.ID)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"deleted": n})
}

// handleStorageAnalysis serves the cached report (GET) or recomputes it
// (POST "Hitung ulang"); trash/versions/quota come from the accounting.
func (s *Server) handleStorageAnalysis(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	res, err := s.files.Analyze(r.Context(), j, u.ID, r.Method == http.MethodPost)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	out := itemView2(res)
	out["computedAt"] = res.GeneratedAt
	status := http.StatusOK
	if res.Partial && s.tasks != nil {
		// walk cut by the 60 s budget: finish it as a dir_size task (Step 11)
		t, terr := s.tasks.Enqueue(r.Context(), tasks.EnqueueRequest{UserID: u.ID, Type: files.TaskDirSize, Title: "Analisis penyimpanan",
			Payload: files.DirSizePayload{Path: "/", Analysis: true}})
		if terr != nil {
			s.writeTaskError(w, r, terr)
			return
		}
		out["task"] = t
		status = http.StatusAccepted
	}
	if s.quota != nil {
		if usage, err := s.quota.Usage(r.Context(), u.ID); err == nil {
			out["trash"], out["versions"] = usage.Trash, usage.Versions
			out["quota"] = map[string]any{"limit": usage.Limit, "used": usage.Used, "free": usage.Free, "percent": usage.Percent, "dirty": usage.Dirty}
		}
	}
	WriteJSON(w, status, out)
}
