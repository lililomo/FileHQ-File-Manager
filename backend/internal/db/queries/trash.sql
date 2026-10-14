-- name: InsertTrashItem :exec
INSERT INTO trash_items (
  id, user_id, original_path, name, is_dir, size_bytes, item_count, kind,
  deleted_at, expires_at, batch_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: GetTrashItem :one
SELECT * FROM trash_items WHERE user_id = ? AND id = ?;

-- name: ListTrashByBatch :many
SELECT * FROM trash_items
WHERE user_id = ? AND batch_id = ? AND restored_at IS NULL AND purged_at IS NULL
ORDER BY deleted_at, id;

-- name: ListTrashItems :many
SELECT * FROM trash_items
WHERE user_id = ? AND restored_at IS NULL AND purged_at IS NULL
ORDER BY deleted_at DESC, id;

-- name: SetTrashRestored :execrows
UPDATE trash_items SET restored_at = ?
WHERE user_id = ? AND id = ? AND restored_at IS NULL AND purged_at IS NULL;

-- name: SetTrashPurged :execrows
UPDATE trash_items SET purged_at = ?
WHERE user_id = ? AND id = ? AND purged_at IS NULL;

-- name: DeleteTrashItemRow :exec
DELETE FROM trash_items WHERE user_id = ? AND id = ?;

-- name: SumTrashBytes :one
SELECT COALESCE(SUM(size_bytes), 0) FROM trash_items
WHERE user_id = ? AND restored_at IS NULL AND purged_at IS NULL;
