-- Resumable upload sessions (SPEC 4.2 upload_sessions, 6.5). Comments are
-- ASCII only (sqlc 1.31 offset bug). Per-user queries filter on user_id so
-- another user's upload id answers "not found".

-- name: InsertUploadSession :exec
INSERT INTO upload_sessions (
  id, user_id, share_id, dest_dir, filename, relative_path, size, conflict_policy, state,
  last_modified, created_at, updated_at, expires_at
) VALUES (
  sqlc.arg('id'), sqlc.arg('user_id'), sqlc.arg('share_id'), sqlc.arg('dest_dir'), sqlc.arg('filename'),
  sqlc.arg('relative_path'), sqlc.arg('size'), sqlc.arg('conflict_policy'), 'active',
  sqlc.arg('last_modified'), sqlc.arg('created_at'), sqlc.arg('updated_at'), sqlc.arg('expires_at')
);

-- name: GetUploadSession :one
SELECT * FROM upload_sessions WHERE id = sqlc.arg('id');

-- name: GetUserUploadSession :one
SELECT * FROM upload_sessions WHERE id = sqlc.arg('id') AND user_id = sqlc.arg('user_id');

-- name: SetUploadSessionState :execrows
UPDATE upload_sessions
SET state = sqlc.arg('state'), finalized_path = sqlc.arg('finalized_path'), updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id') AND state = 'active';

-- name: TouchUploadSession :exec
UPDATE upload_sessions SET updated_at = sqlc.arg('updated_at'), expires_at = sqlc.arg('expires_at')
WHERE id = sqlc.arg('id') AND state = 'active';

-- name: SumActiveUploadSizes :one
SELECT COALESCE(SUM(size), 0) FROM upload_sessions WHERE user_id = sqlc.arg('user_id') AND state = 'active';

-- name: CountActiveUploadSessions :one
SELECT COUNT(*) FROM upload_sessions WHERE user_id = sqlc.arg('user_id') AND state = 'active';

-- name: ListExpiredUploadSessions :many
SELECT * FROM upload_sessions WHERE state = 'active' AND expires_at < sqlc.arg('now')
ORDER BY expires_at LIMIT sqlc.arg('max_rows');

-- name: ListActiveUploadIDs :many
SELECT id FROM upload_sessions WHERE state = 'active';

-- name: DeleteUploadSessionsBefore :execrows
DELETE FROM upload_sessions WHERE state <> 'active' AND updated_at < sqlc.arg('before');
