-- name: InsertAudit :one
INSERT INTO audit_logs (
  at, actor_user_id, actor_username, action, target_type, target_id, ip, user_agent, details_json
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING id;

-- name: ListAudit :many
SELECT * FROM audit_logs
WHERE (sqlc.arg('before_id') = 0 OR id < sqlc.arg('before_id'))
  AND (sqlc.narg('actor_user_id') IS NULL OR actor_user_id = sqlc.narg('actor_user_id'))
  AND (sqlc.narg('action') IS NULL OR action = sqlc.narg('action'))
  AND (sqlc.narg('from_at') IS NULL OR at >= sqlc.narg('from_at'))
  AND (sqlc.narg('to_at') IS NULL OR at < sqlc.narg('to_at'))
ORDER BY id DESC
LIMIT sqlc.arg('limit');

-- name: CountAuditSince :one
SELECT COUNT(*) FROM audit_logs WHERE action = ? AND at >= ?;

-- name: DeleteAuditOlderThan :execrows
DELETE FROM audit_logs WHERE at < ?;
