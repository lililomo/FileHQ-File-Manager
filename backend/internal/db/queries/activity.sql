-- name: InsertActivity :one
INSERT INTO activity_logs (user_id, at, action, path, path2, details_json, batch_id)
VALUES (?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: ListActivity :many
SELECT * FROM activity_logs
WHERE user_id = ? AND id < ?
ORDER BY id DESC
LIMIT ?;

-- name: DeleteActivityOlderThan :execrows
DELETE FROM activity_logs WHERE at < ?;
