-- name: GetEditorSession :one
SELECT * FROM editor_sessions WHERE user_id = ?;

-- name: UpsertEditorSession :one
INSERT INTO editor_sessions (user_id, tabs_json, active_path, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (user_id) DO UPDATE SET
  tabs_json = excluded.tabs_json,
  active_path = excluded.active_path,
  updated_at = excluded.updated_at
RETURNING *;

-- name: DeleteEditorSession :execrows
DELETE FROM editor_sessions WHERE user_id = ?;
