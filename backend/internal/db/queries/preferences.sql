-- name: GetPreferences :one
SELECT * FROM user_preferences WHERE user_id = ?;

-- name: UpsertPreferences :exec
INSERT INTO user_preferences (user_id, prefs_json, updated_at)
VALUES (?, ?, ?)
ON CONFLICT (user_id) DO UPDATE SET
  prefs_json = excluded.prefs_json,
  updated_at = excluded.updated_at;

-- name: DeletePreferences :exec
DELETE FROM user_preferences WHERE user_id = ?;

-- name: GetFolderViewPref :one
SELECT * FROM folder_view_prefs WHERE user_id = ? AND path = ?;

-- name: UpsertFolderViewPref :exec
INSERT INTO folder_view_prefs (
  user_id, path, view_mode, sort_field, sort_dir, sort2_field, sort2_dir, columns_json, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (user_id, path) DO UPDATE SET
  view_mode = excluded.view_mode,
  sort_field = excluded.sort_field,
  sort_dir = excluded.sort_dir,
  sort2_field = excluded.sort2_field,
  sort2_dir = excluded.sort2_dir,
  columns_json = excluded.columns_json,
  updated_at = excluded.updated_at;

-- name: DeleteFolderViewPref :exec
DELETE FROM folder_view_prefs WHERE user_id = ? AND path = ?;

-- name: CountFolderViewPrefs :one
SELECT COUNT(*) FROM folder_view_prefs WHERE user_id = ?;

-- name: ListOldestFolderViewPrefPaths :many
SELECT path FROM folder_view_prefs WHERE user_id = ? ORDER BY updated_at ASC LIMIT ?;
