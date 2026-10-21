-- Sharing (SPEC 4.2 shares / share_access_logs, 8.15, 6.6). Comments are
-- ASCII only (sqlc 1.31 offset bug). Prefix matching follows refs.sql:
-- prefix = path || '/', prefix_len = len(prefix), from_pos = len(path) + 1.

-- name: InsertShare :one
INSERT INTO shares (
  id, owner_user_id, path, is_dir, mode, token_hash, slug, password_hash, expires_at,
  max_downloads, allow_download, allow_upload, allow_reshare, hide_owner, hide_listing,
  note, token_enc, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetShare :one
SELECT * FROM shares WHERE id = sqlc.arg('id') AND owner_user_id = sqlc.arg('owner_user_id');

-- name: GetShareByID :one
SELECT * FROM shares WHERE id = ?;

-- name: GetShareByTokenHash :one
SELECT * FROM shares WHERE token_hash = ?;

-- name: GetShareBySlug :one
SELECT * FROM shares WHERE slug = ?;

-- name: ListSharesByOwner :many
SELECT * FROM shares
WHERE owner_user_id = sqlc.arg('owner_user_id')
  AND (created_at < sqlc.arg('before_created') OR (created_at = sqlc.arg('before_created') AND id < sqlc.arg('before_id')))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('lim');

-- name: ListSharesByOwnerPath :many
SELECT * FROM shares
WHERE owner_user_id = sqlc.arg('owner_user_id') AND path = sqlc.arg('path')
ORDER BY created_at DESC, id DESC;

-- name: ListSharesInDir :many
SELECT id, path, mode, disabled_at, broken_at, expires_at FROM shares
WHERE owner_user_id = sqlc.arg('owner_user_id')
  AND substr(path, 1, sqlc.arg('prefix_len')) = sqlc.arg('prefix')
  AND instr(substr(path, sqlc.arg('from_pos')), '/') = 0;

-- name: ListSharesUnderPath :many
SELECT * FROM shares
WHERE owner_user_id = sqlc.arg('owner_user_id')
  AND (path = sqlc.arg('path') OR substr(path, 1, sqlc.arg('prefix_len')) = sqlc.arg('prefix'))
ORDER BY path;

-- name: UpdateShare :one
UPDATE shares
SET slug = sqlc.arg('slug'),
    password_hash = sqlc.arg('password_hash'),
    expires_at = sqlc.arg('expires_at'),
    max_downloads = sqlc.arg('max_downloads'),
    allow_download = sqlc.arg('allow_download'),
    allow_upload = sqlc.arg('allow_upload'),
    hide_owner = sqlc.arg('hide_owner'),
    hide_listing = sqlc.arg('hide_listing'),
    note = sqlc.arg('note'),
    updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id') AND owner_user_id = sqlc.arg('owner_user_id')
RETURNING *;

-- name: SetShareToken :execrows
UPDATE shares SET token_hash = ?, token_enc = ?, updated_at = ?
WHERE id = ? AND owner_user_id = ?;

-- name: SetShareDisabled :execrows
UPDATE shares SET disabled_at = sqlc.arg('disabled_at'), disabled_reason = sqlc.arg('disabled_reason'), updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id') AND owner_user_id = sqlc.arg('owner_user_id');

-- name: SetShareEnabled :execrows
UPDATE shares SET disabled_at = NULL, disabled_reason = NULL, updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id') AND owner_user_id = sqlc.arg('owner_user_id');

-- name: SetShareBroken :exec
UPDATE shares SET broken_at = ? WHERE id = ?;

-- name: DeleteShare :execrows
DELETE FROM shares WHERE id = ? AND owner_user_id = ?;

-- name: IncrementShareView :exec
UPDATE shares SET view_count = view_count + 1, last_access_at = ? WHERE id = ?;

-- name: TouchShareAccess :exec
UPDATE shares SET last_access_at = ? WHERE id = ?;

-- name: IncrementShareDownload :execrows
UPDATE shares SET download_count = download_count + 1, last_access_at = sqlc.arg('at')
WHERE id = sqlc.arg('id') AND (max_downloads IS NULL OR download_count < max_downloads);

-- name: RewriteSharesPath :execrows
UPDATE shares
SET path = sqlc.arg('new_path') || substr(path, sqlc.arg('from_pos')), broken_at = NULL, updated_at = sqlc.arg('updated_at')
WHERE owner_user_id = sqlc.arg('owner_user_id')
  AND (path = sqlc.arg('old_path') OR substr(path, 1, sqlc.arg('prefix_len')) = sqlc.arg('prefix'));

-- name: DeleteSharesPrefix :execrows
DELETE FROM shares
WHERE owner_user_id = sqlc.arg('owner_user_id')
  AND (path = sqlc.arg('path') OR substr(path, 1, sqlc.arg('prefix_len')) = sqlc.arg('prefix'));

-- name: ListSharesForCheck :many
SELECT id, owner_user_id, path, broken_at FROM shares
WHERE id > sqlc.arg('after_id')
ORDER BY id
LIMIT sqlc.arg('lim');

-- name: InsertShareAccessLog :exec
INSERT INTO share_access_logs (share_id, at, ip_trunc, action, user_id, user_agent, path)
VALUES (?, ?, ?, ?, ?, ?, ?);

-- name: ListShareAccessLogs :many
SELECT * FROM share_access_logs
WHERE share_id = sqlc.arg('share_id') AND id < sqlc.arg('before_id')
ORDER BY id DESC
LIMIT sqlc.arg('lim');

-- name: CountShareAccessLogs :one
SELECT COUNT(*) FROM share_access_logs WHERE share_id = ? AND action = ? AND at >= ?;

-- name: DeleteShareAccessLogsBefore :execrows
DELETE FROM share_access_logs WHERE at < ?;
