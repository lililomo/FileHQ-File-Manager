-- name: ListSettings :many
SELECT * FROM settings ORDER BY key;

-- name: GetSetting :one
SELECT * FROM settings WHERE key = ?;

-- name: UpsertSetting :exec
INSERT INTO settings (key, value_json, updated_at, updated_by)
VALUES (?, ?, ?, ?)
ON CONFLICT (key) DO UPDATE SET
  value_json = excluded.value_json,
  updated_at = excluded.updated_at,
  updated_by = excluded.updated_by;

-- name: DeleteSetting :exec
DELETE FROM settings WHERE key = ?;
