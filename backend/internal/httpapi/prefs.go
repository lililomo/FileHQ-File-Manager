package httpapi

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/listing"
)

// Preference registry (SPEC §8.18). PUT /me/preferences rejects keys that are
// not listed here (VALIDATION_FAILED details.fields.<key> = unknown_key) and
// values that do not match the declared shape (invalid_value).

// ColumnIDs are the Details-view columns (SPEC §8.4 / brief §5).
var ColumnIDs = []string{
	"name", "size", "type", "mtime", "note", "btime", "atime", "ext", "mode",
	"itemCount", "dimensions", "duration", "shared", "location",
}

// ViewModes mirrors folder_view_prefs.view_mode CHECK (SPEC §4.2).
var ViewModes = []string{"details", "list", "tiles", "large", "xlarge"}

// maxPrefValueBytes bounds a single free-form value (editor.*, shortcuts).
const maxPrefValueBytes = 4 << 10

type prefValidator func(raw json.RawMessage) bool

func enumPref(values ...string) prefValidator {
	return func(raw json.RawMessage) bool {
		var s string
		return json.Unmarshal(raw, &s) == nil && slices.Contains(values, s)
	}
}

func boolPref(raw json.RawMessage) bool {
	var b bool
	return json.Unmarshal(raw, &b) == nil
}

func numberPref(min, max float64) prefValidator {
	return func(raw json.RawMessage) bool {
		var f float64
		return json.Unmarshal(raw, &f) == nil && f >= min && f <= max
	}
}

func timezonePref(raw json.RawMessage) bool {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return false
	}
	if s == "auto" {
		return true
	}
	_, err := time.LoadLocation(s)
	return err == nil && s != "" && len(s) <= 64
}

// ColumnsPref is the shape of view.columns and folder_view_prefs.columns_json.
type ColumnsPref struct {
	Visible []string       `json:"visible"`
	Order   []string       `json:"order"`
	Widths  map[string]int `json:"widths"`
}

func columnsPref(raw json.RawMessage) bool {
	var c ColumnsPref
	if json.Unmarshal(raw, &c) != nil {
		return false
	}
	return c.valid()
}

func (c ColumnsPref) valid() bool {
	for _, id := range c.Visible {
		if !slices.Contains(ColumnIDs, id) {
			return false
		}
	}
	if !slices.Contains(c.Visible, "name") && len(c.Visible) > 0 {
		return false // name is mandatory
	}
	for _, id := range c.Order {
		if !slices.Contains(ColumnIDs, id) {
			return false
		}
	}
	for id, w := range c.Widths {
		if !slices.Contains(ColumnIDs, id) || w < 40 || w > 2000 {
			return false
		}
	}
	return true
}

// SortPref is the shape of view.sort.
type SortPref struct {
	Field  string `json:"field"`
	Dir    string `json:"dir"`
	Field2 string `json:"field2,omitempty"`
	Dir2   string `json:"dir2,omitempty"`
}

func sortPref(raw json.RawMessage) bool {
	var s SortPref
	if json.Unmarshal(raw, &s) != nil {
		return false
	}
	_, _, err := listing.ParseSortSpec(s.Field, s.Dir, s.Field2, s.Dir2, true)
	return err == nil
}

func tabsOpenPref(raw json.RawMessage) bool {
	var tabs []struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(raw, &tabs) != nil || len(tabs) > 10 {
		return false
	}
	for _, t := range tabs {
		if !strings.HasPrefix(t.Path, "/") || len(t.Path) > 4096 {
			return false
		}
	}
	return true
}

func videoPositionsPref(raw json.RawMessage) bool {
	var m map[string]float64
	if json.Unmarshal(raw, &m) != nil || len(m) > 100 {
		return false
	}
	for _, v := range m {
		if v < 0 {
			return false
		}
	}
	return true
}

func objectPref(raw json.RawMessage) bool {
	var m map[string]json.RawMessage
	return len(raw) <= maxPrefValueBytes && json.Unmarshal(raw, &m) == nil
}

func anySmallPref(raw json.RawMessage) bool {
	return len(raw) <= maxPrefValueBytes && json.Valid(raw)
}

var prefRegistry = map[string]prefValidator{
	"ui.theme":                 enumPref("light", "dark", "system"),
	"ui.accent":                enumPref("blue", "emerald", "violet", "amber", "rose", "slate"),
	"ui.language":              enumPref("id", "en"),
	"ui.density":               enumPref("compact", "comfortable"),
	"ui.sidebar_collapsed":     boolPref,
	"ui.preview_panel":         boolPref,
	"ui.timezone":              timezonePref,
	"view.mode":                enumPref(ViewModes...),
	"view.remember_per_folder": boolPref,
	"view.folders_first":       boolPref,
	"view.show_hidden":         boolPref,
	"view.columns":             columnsPref,
	"view.sort":                sortPref,
	"fileops.confirm_delete":   boolPref,
	"fileops.copy_metadata":    boolPref,
	"upload.preserve_mtime":    boolPref,
	"viewer.slideshow_interval": func(raw json.RawMessage) bool {
		var f float64
		return json.Unmarshal(raw, &f) == nil && (f == 2 || f == 5 || f == 10)
	},
	"exif.show_gps":            boolPref,
	"audio.volume":             numberPref(0, 1),
	"audio.repeat":             enumPref("off", "all", "one"),
	"audio.shuffle":            boolPref,
	"video.positions":          videoPositionsPref,
	"notifications.task_toast": boolPref,
	"shortcuts.overrides":      objectPref,
	"tabs.restore":             boolPref,
	"tabs.open":                tabsOpenPref,
}

// validatePref returns "" when key/value are acceptable, otherwise the field
// reason (unknown_key | invalid_value).
func validatePref(key string, raw json.RawMessage) string {
	v, ok := prefRegistry[key]
	if !ok {
		if strings.HasPrefix(key, "editor.") && len(key) > len("editor.") {
			v = anySmallPref
		} else {
			return "unknown_key"
		}
	}
	if !v(raw) {
		return "invalid_value"
	}
	return ""
}

// --- folder view prefs (SPEC §6.3 "me", §8.18, §4.2 folder_view_prefs) ---

func (s *Server) mountFolderPrefs(p chi.Router) {
	p.Get("/me/folder-prefs", s.handleGetFolderPref)
	p.Put("/me/folder-prefs", s.handlePutFolderPref)
	p.Delete("/me/folder-prefs", s.handleDeleteFolderPref)
}

type folderPrefView struct {
	Path      string       `json:"path"`
	View      string       `json:"view"`
	Sort      SortPref     `json:"sort"`
	Columns   *ColumnsPref `json:"columns"`
	UpdatedAt string       `json:"updatedAt"`
	raw       sqlc.FolderViewPref
}

func folderPrefFromRow(row sqlc.FolderViewPref) folderPrefView {
	v := folderPrefView{Path: row.Path, View: row.ViewMode, UpdatedAt: row.UpdatedAt, raw: row}
	v.Sort = SortPref{Field: row.SortField, Dir: row.SortDir}
	if row.Sort2Field != nil {
		v.Sort.Field2 = *row.Sort2Field
	}
	if row.Sort2Dir != nil {
		v.Sort.Dir2 = *row.Sort2Dir
	}
	if row.ColumnsJson != nil {
		var c ColumnsPref
		if json.Unmarshal([]byte(*row.ColumnsJson), &c) == nil {
			v.Columns = &c
		}
	}
	return v
}

func (s *Server) handleGetFolderPref(w http.ResponseWriter, r *http.Request) {
	p, ok := cleanPath(w, r, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	u, _ := UserFrom(r.Context())
	row, err := s.store.GetFolderViewPref(r.Context(), u.ID, string(p))
	if err != nil {
		if db.IsNotFound(err) {
			WriteJSON(w, http.StatusOK, map[string]any{"pref": nil})
			return
		}
		WriteError(w, r, CodeInternal, 0, "internal server error", nil)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"pref": folderPrefFromRow(row)})
}

type folderPrefRequest struct {
	Path    string       `json:"path"`
	View    string       `json:"view"`
	Sort    string       `json:"sort"`
	Dir     string       `json:"dir"`
	Sort2   string       `json:"sort2,omitempty"`
	Dir2    string       `json:"dir2,omitempty"`
	Columns *ColumnsPref `json:"columns,omitempty"`
}

func (s *Server) handlePutFolderPref(w http.ResponseWriter, r *http.Request) {
	var req folderPrefRequest
	if !ReadJSON(w, r, &req) {
		return
	}
	p, ok := cleanPath(w, r, req.Path)
	if !ok {
		return
	}
	fields := map[string]any{}
	if req.View == "" {
		req.View = "details"
	}
	if !slices.Contains(ViewModes, req.View) {
		fields["view"] = "invalid"
	}
	spec, reason, err := listing.ParseSortSpec(req.Sort, req.Dir, req.Sort2, req.Dir2, true)
	if err != nil {
		fields[reason] = "invalid"
	}
	if req.Columns != nil && !req.Columns.valid() {
		fields["columns"] = "invalid"
	}
	if len(fields) > 0 {
		WriteError(w, r, CodeValidationFailed, 0, "invalid folder preference", map[string]any{"fields": fields})
		return
	}
	u, _ := UserFrom(r.Context())
	params := sqlc.UpsertFolderViewPrefParams{
		UserID: u.ID, Path: string(p), ViewMode: req.View,
		SortField: string(spec.Field), SortDir: dirString(spec.Desc), UpdatedAt: db.Now(),
	}
	if spec.Field2 != "" {
		f2, d2 := string(spec.Field2), dirString(spec.Desc2)
		params.Sort2Field, params.Sort2Dir = &f2, &d2
	}
	if req.Columns != nil {
		raw, _ := json.Marshal(req.Columns)
		cs := string(raw)
		params.ColumnsJson = &cs
	}
	if err := s.store.UpsertFolderViewPref(r.Context(), params); err != nil {
		WriteError(w, r, CodeInternal, 0, "internal server error", nil)
		return
	}
	row, err := s.store.GetFolderViewPref(r.Context(), u.ID, string(p))
	if err != nil {
		WriteError(w, r, CodeInternal, 0, "internal server error", nil)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"pref": folderPrefFromRow(row)})
}

func (s *Server) handleDeleteFolderPref(w http.ResponseWriter, r *http.Request) {
	p, ok := cleanPath(w, r, r.URL.Query().Get("path"))
	if !ok {
		return
	}
	u, _ := UserFrom(r.Context())
	if err := s.store.DeleteFolderViewPref(r.Context(), u.ID, string(p)); err != nil {
		WriteError(w, r, CodeInternal, 0, "internal server error", nil)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func dirString(desc bool) string {
	if desc {
		return "desc"
	}
	return "asc"
}
