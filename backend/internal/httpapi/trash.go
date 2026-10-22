package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/audit"
	"kppbwi.app/filemanager/internal/files"
	"kppbwi.app/filemanager/internal/fsjail"
	"kppbwi.app/filemanager/internal/trash"
)

// mountTrash registers /trash (SPEC §6.3 "Trash", §8.6): S+P, CSRF on POST.
func (s *Server) mountTrash(api chi.Router) {
	if s.files == nil || s.files.Trash() == nil {
		return
	}
	api.Group(func(g chi.Router) {
		g.Use(OriginCheck(s.cfg.AppBaseURL))
		g.Use(s.RequireAuth)
		g.Use(s.RequireCSRF)
		g.Use(RequirePasswordChanged)
		g.Get("/trash", s.handleTrashList)
		g.Post("/trash/restore", s.handleTrashRestore)
		g.Post("/trash/purge", s.handleTrashPurge)
		g.Post("/trash/empty", s.handleTrashEmpty)
	})
}

func (s *Server) handleTrashList(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	u, _ := UserFrom(r.Context())
	q := r.URL.Query()
	f := trash.Filter{Kind: q.Get("kind"), Query: q.Get("q"), Sort: q.Get("sort"), Cursor: q.Get("cursor")}
	if k := strings.ToLower(f.Kind); k != "" && k != "dir" && !trash.Kinds[k] {
		WriteError(w, r, CodeValidationFailed, 0, "unknown kind", map[string]any{"fields": map[string]any{"kind": "file, folder, image, document, video, audio, archive or other"}})
		return
	}
	switch f.Sort {
	case "", "deleted", "name", "size", "expires", "path":
	default:
		WriteError(w, r, CodeValidationFailed, 0, "unknown sort", map[string]any{"fields": map[string]any{"sort": "deleted, name, size, expires or path"}})
		return
	}
	if d := q.Get("dir"); d != "" {
		desc := d == "desc"
		if !desc && d != "asc" {
			WriteError(w, r, CodeValidationFailed, 0, "dir must be asc or desc", map[string]any{"fields": map[string]any{"dir": "asc|desc"}})
			return
		}
		f.Desc = &desc
	}
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > 1000 {
			WriteError(w, r, CodeValidationFailed, 0, "limit must be 1..1000", map[string]any{"fields": map[string]any{"limit": "1..1000"}})
			return
		}
		f.Limit = n
	}
	res, err := s.files.Trash().List(r.Context(), u.ID, f)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{
		"items": res.Items, "total": res.Total, "totalBytes": res.TotalBytes, "nextCursor": nullable(res.NextCursor),
		"retentionDays": s.files.Trash().RetentionDays(),
	})
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

type trashRestoreRequest struct {
	IDs         []string          `json:"ids"`
	To          string            `json:"to,omitempty"`
	Dest        string            `json:"dest,omitempty"`
	OnConflict  string            `json:"onConflict,omitempty"`
	OnConflict2 string            `json:"on_conflict,omitempty"`
	Policy      string            `json:"policy,omitempty"`
	PerItem     map[string]string `json:"perItem,omitempty"`
	PerItem2    map[string]string `json:"per_item,omitempty"`
}

func (s *Server) handleTrashRestore(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req trashRestoreRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	if len(req.IDs) == 0 {
		WriteError(w, r, CodeValidationFailed, 0, "ids are required", map[string]any{"fields": map[string]any{"ids": "required"}})
		return
	}
	def, ok := trash.ParsePolicy(firstNonEmpty(req.OnConflict, req.OnConflict2, req.Policy))
	if !ok {
		WriteError(w, r, CodeValidationFailed, 0, "invalid conflict policy", map[string]any{"fields": map[string]any{"onConflict": "must be fail, overwrite, skip or rename"}})
		return
	}
	fr := files.RestoreRequest{IDs: req.IDs, Default: def}
	if dest := firstNonEmpty(req.To, req.Dest); dest != "" {
		p, ok := cleanPath(w, r, dest)
		if !ok {
			return
		}
		fr.Dest = p
	}
	per := req.PerItem
	if len(per) == 0 {
		per = req.PerItem2
	}
	if len(per) > 0 {
		fr.PerItem = map[string]trash.Policy{}
		for id, v := range per {
			pp, ok := trash.ParsePolicy(v)
			if !ok {
				WriteError(w, r, CodeValidationFailed, 0, "invalid conflict policy", map[string]any{"fields": map[string]any{"perItem." + id: "must be fail, overwrite, skip or rename"}})
				return
			}
			fr.PerItem[id] = pp
		}
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	out, err := s.files.RestoreFromTrash(r.Context(), j, u.ID, fr)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	for _, res := range out.Results {
		if res.To != "" {
			s.OnChange(r, u.ID, fsjail.VPath(res.To), res.IsDir)
		}
	}
	WriteJSON(w, http.StatusOK, map[string]any{"results": out.Results, "reactivateShares": out.ReactivateShares, "undo": nil})
}

type idsRequest struct {
	IDs []string `json:"ids"`
}

func (s *Server) handleTrashPurge(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req idsRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	if len(req.IDs) == 0 {
		WriteError(w, r, CodeValidationFailed, 0, "ids are required", map[string]any{"fields": map[string]any{"ids": "required"}})
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	out, err := s.files.PurgeTrash(r.Context(), j, u.ID, req.IDs)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	if out.Deleted > 0 {
		first := ""
		for _, res := range out.Results {
			if res.Status == "ok" {
				first = res.OriginalPath
				break
			}
		}
		s.audit.Record(r.Context(), audit.Entry{ActorID: u.ID, ActorUsername: u.Username, Action: audit.ActionDeletePermanent, TargetType: "trash",
			IP: ClientIP(r), UserAgent: r.UserAgent(), Details: map[string]any{"count": out.Deleted, "first": first, "bytesFreed": out.BytesFreed, "source": "trash"}})
	}
	WriteJSON(w, http.StatusOK, map[string]any{"results": out.Results, "deleted": out.Deleted, "bytesFreed": out.BytesFreed, "undo": nil})
}

type confirmRequest struct {
	Confirm string `json:"confirm,omitempty"`
}

func (s *Server) handleTrashEmpty(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req confirmRequest
	if r.ContentLength != 0 && !ReadJSON(w, r, &req) {
		return
	}
	if req.Confirm != files.PermanentConfirmWord {
		WriteError(w, r, CodeValidationFailed, 0, "confirmation word required", map[string]any{"fields": map[string]any{"confirm": "required"}, "word": files.PermanentConfirmWord})
		return
	}
	j, u, release, ok := s.userJail(w, r)
	if !ok {
		return
	}
	defer release()
	out, err := s.files.EmptyTrash(r.Context(), j, u.ID)
	if err != nil {
		s.writeOpsError(w, r, err)
		return
	}
	s.audit.Record(r.Context(), audit.Entry{ActorID: u.ID, ActorUsername: u.Username, Action: audit.ActionTrashEmpty, TargetType: "trash",
		IP: ClientIP(r), UserAgent: r.UserAgent(), Details: map[string]any{"count": out.Deleted, "bytesFreed": out.BytesFreed}})
	WriteJSON(w, http.StatusOK, map[string]any{"results": out.Results, "deleted": out.Deleted, "bytesFreed": out.BytesFreed, "undo": nil})
}
