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

-- name: ListActivityFiltered :many
SELECT * FROM activity_logs
WHERE user_id = sqlc.arg('user_id')
  AND id < sqlc.arg('before_id')
  AND (sqlc.arg('action') = '' OR action = sqlc.arg('action'))
  AND (sqlc.arg('from_at') = '' OR at >= sqlc.arg('from_at'))
  AND (sqlc.arg('to_at') = '' OR at < sqlc.arg('to_at'))
  AND (sqlc.arg('q') = '' OR instr(lower(coalesce(path, '')), sqlc.arg('q')) > 0 OR instr(lower(coalesce(path2, '')), sqlc.arg('q')) > 0)
ORDER BY id DESC
LIMIT sqlc.arg('lim');

-- name: ListRecentActivity :many
SELECT * FROM activity_logs
WHERE user_id = ? AND at >= ? AND path IS NOT NULL
  AND action IN ('open', 'edit', 'upload', 'create', 'restore')
ORDER BY id DESC
LIMIT ?;

-- name: DeleteActivityForUser :execrows
DELETE FROM activity_logs WHERE user_id = ?;

-- name: CountActivity :one
SELECT count(*) FROM activity_logs WHERE user_id = ?;
