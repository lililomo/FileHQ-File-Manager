-- name: CreateSession :one
INSERT INTO sessions (
  id, user_id, csrf_secret, created_at, last_seen_at, idle_expires_at, absolute_expires_at,
  ip, user_agent, device_label
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetSession :one
SELECT * FROM sessions WHERE id = ?;

-- name: TouchSession :exec
UPDATE sessions SET last_seen_at = ?, idle_expires_at = ? WHERE id = ?;

-- name: RevokeSession :execrows
UPDATE sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL;

-- name: RevokeUserSessions :execrows
UPDATE sessions
SET revoked_at = sqlc.arg('revoked_at')
WHERE user_id = sqlc.arg('user_id') AND revoked_at IS NULL AND id <> sqlc.arg('except_id');

-- name: ListUserSessions :many
SELECT * FROM sessions
WHERE user_id = sqlc.arg('user_id') AND revoked_at IS NULL
  AND idle_expires_at > sqlc.arg('now') AND absolute_expires_at > sqlc.arg('now')
ORDER BY last_seen_at DESC;

-- name: CountActiveSessions :one
SELECT COUNT(*) FROM sessions
WHERE revoked_at IS NULL AND idle_expires_at > sqlc.arg('now') AND absolute_expires_at > sqlc.arg('now');

-- name: DeleteExpiredSessions :execrows
DELETE FROM sessions
WHERE absolute_expires_at < sqlc.arg('before')
   OR idle_expires_at < sqlc.arg('before')
   OR (revoked_at IS NOT NULL AND revoked_at < sqlc.arg('before'));
