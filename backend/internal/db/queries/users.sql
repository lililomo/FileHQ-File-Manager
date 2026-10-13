-- name: CreateUser :one
INSERT INTO users (
  id, username, display_name, role, status, password_hash, must_change_password,
  quota_bytes, created_by, created_at, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = ?;

-- name: GetUserByUsername :one
SELECT * FROM users WHERE username = ?;

-- name: ListUsers :many
SELECT * FROM users
WHERE deleted_at IS NULL
  AND (sqlc.narg('status') IS NULL OR status = sqlc.narg('status'))
  AND (sqlc.narg('role') IS NULL OR role = sqlc.narg('role'))
  AND (sqlc.narg('q') IS NULL OR username LIKE sqlc.narg('q') OR display_name LIKE sqlc.narg('q'))
ORDER BY username
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');

-- name: CountUsers :one
SELECT COUNT(*) FROM users
WHERE deleted_at IS NULL
  AND (sqlc.narg('status') IS NULL OR status = sqlc.narg('status'))
  AND (sqlc.narg('role') IS NULL OR role = sqlc.narg('role'))
  AND (sqlc.narg('q') IS NULL OR username LIKE sqlc.narg('q') OR display_name LIKE sqlc.narg('q'));

-- name: CountAllUsers :one
SELECT COUNT(*) FROM users;

-- name: CountActiveAdmins :one
SELECT COUNT(*) FROM users WHERE role = 'admin' AND status = 'active' AND deleted_at IS NULL;

-- name: UpdateUserPassword :exec
UPDATE users
SET password_hash = ?, must_change_password = ?, password_changed_at = ?, updated_at = ?
WHERE id = ?;

-- name: UpdateUserProfile :exec
UPDATE users SET display_name = ?, role = ?, quota_bytes = ?, updated_at = ? WHERE id = ?;

-- name: UpdateUserStatus :exec
UPDATE users SET status = ?, updated_at = ? WHERE id = ?;

-- name: UpdateUserQuota :exec
UPDATE users SET quota_bytes = ?, updated_at = ? WHERE id = ?;

-- name: SetMustChangePassword :exec
UPDATE users SET must_change_password = ?, updated_at = ? WHERE id = ?;

-- name: RecordLoginSuccess :exec
UPDATE users
SET last_login_at = ?, failed_login_count = 0, locked_until = NULL, updated_at = ?
WHERE id = ?;

-- name: RecordLoginFailure :exec
UPDATE users
SET failed_login_count = failed_login_count + 1, locked_until = ?, updated_at = ?
WHERE id = ?;

-- name: SetHomeProvisioned :exec
UPDATE users SET home_provisioned_at = ?, home_warning = ?, updated_at = ? WHERE id = ?;

-- name: SetUserTOTP :exec
UPDATE users SET totp_secret_enc = ?, totp_enabled = ?, updated_at = ? WHERE id = ?;

-- name: SoftDeleteUser :exec
UPDATE users SET deleted_at = ?, status = 'disabled', updated_at = ? WHERE id = ?;

-- name: HardDeleteUser :exec
DELETE FROM users WHERE id = ?;
