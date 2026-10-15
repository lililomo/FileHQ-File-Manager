package httpapi

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"kppbwi.app/filemanager/internal/db"
	"kppbwi.app/filemanager/internal/db/sqlc"
	"kppbwi.app/filemanager/internal/fsjail"
)

// Editor workspace session (SPEC §4.2 editor_sessions, §8.13 "Tab & sesi"):
// the ordered open tabs with cursor/scroll per user, restored by /editor.

const (
	// MaxEditorTabs is the tab cap of the workspace (SPEC §8.13).
	MaxEditorTabs = 20
	// maxEditorSessionBytes bounds the stored JSON.
	maxEditorSessionBytes = 32 << 10
)

type editorCursor struct {
	Line int `json:"line"`
	Col  int `json:"col"`
}

type editorTab struct {
	Path      string        `json:"path"`
	ScrollTop int           `json:"scrollTop"`
	Cursor    *editorCursor `json:"cursor,omitempty"`
	Language  string        `json:"language,omitempty"`
	Encoding  string        `json:"encoding,omitempty"`
	Pinned    bool          `json:"pinned,omitempty"`
}

type editorSession struct {
	Tabs       []editorTab `json:"tabs"`
	ActivePath *string     `json:"activePath"`
	UpdatedAt  string      `json:"updatedAt,omitempty"`
}

func (s *Server) handleEditorSessionGet(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	u, _ := UserFrom(r.Context())
	row, err := s.store.GetEditorSession(r.Context(), u.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			WriteJSON(w, http.StatusOK, editorSession{Tabs: []editorTab{}})
			return
		}
		WriteError(w, r, CodeInternal, 0, "could not load the editor session", nil)
		return
	}
	var tabs []editorTab
	if err := json.Unmarshal([]byte(row.TabsJson), &tabs); err != nil || tabs == nil {
		tabs = []editorTab{}
	}
	WriteJSON(w, http.StatusOK, editorSession{Tabs: tabs, ActivePath: row.ActivePath, UpdatedAt: row.UpdatedAt})
}

func (s *Server) handleEditorSessionPut(w http.ResponseWriter, r *http.Request) {
	if rejectScope(w, r) {
		return
	}
	var req editorSession
	if !ReadJSON(w, r, &req) {
		return
	}
	if len(req.Tabs) > MaxEditorTabs {
		WriteError(w, r, CodeValidationFailed, 0, "too many tabs", map[string]any{"fields": map[string]any{"tabs": "max_20"}, "max": MaxEditorTabs})
		return
	}
	clean := make([]editorTab, 0, len(req.Tabs))
	for i, t := range req.Tabs {
		vp, err := fsjail.Clean(t.Path)
		if err != nil || vp.IsRoot() {
			WriteError(w, r, CodeValidationFailed, 0, "tab path is not valid", map[string]any{"fields": map[string]any{"tabs": "invalid_path"}, "index": i})
			return
		}
		t.Path = string(vp)
		if t.ScrollTop < 0 {
			t.ScrollTop = 0
		}
		if t.Cursor != nil && (t.Cursor.Line < 1 || t.Cursor.Col < 1) {
			t.Cursor = &editorCursor{Line: max(1, t.Cursor.Line), Col: max(1, t.Cursor.Col)}
		}
		if len(t.Language) > 64 || len(t.Encoding) > 32 {
			WriteError(w, r, CodeValidationFailed, 0, "tab metadata too long", map[string]any{"fields": map[string]any{"tabs": "invalid_value"}, "index": i})
			return
		}
		clean = append(clean, t)
	}
	var active *string
	if req.ActivePath != nil && *req.ActivePath != "" {
		vp, err := fsjail.Clean(*req.ActivePath)
		if err != nil {
			WriteError(w, r, CodeValidationFailed, 0, "activePath is not valid", map[string]any{"fields": map[string]any{"activePath": "invalid"}})
			return
		}
		a := string(vp)
		active = &a
	}
	raw, _ := json.Marshal(clean)
	if len(raw) > maxEditorSessionBytes {
		WriteError(w, r, CodePayloadTooLarge, 0, "editor session too large", map[string]any{"limit": maxEditorSessionBytes})
		return
	}
	u, _ := UserFrom(r.Context())
	row, err := s.store.UpsertEditorSession(r.Context(), sqlc.UpsertEditorSessionParams{UserID: u.ID, TabsJson: string(raw), ActivePath: active, UpdatedAt: db.FormatTime(time.Now())})
	if err != nil {
		WriteError(w, r, CodeInternal, 0, "could not save the editor session", nil)
		return
	}
	WriteJSON(w, http.StatusOK, editorSession{Tabs: clean, ActivePath: row.ActivePath, UpdatedAt: row.UpdatedAt})
}
