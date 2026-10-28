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

-- name: SetUserTOTPPending :exec
-- Step 23a: POST /me/totp/setup stores the encrypted secret unverified.
UPDATE users
SET totp_secret_enc = ?, totp_enabled = 0, totp_enabled_at = NULL, totp_last_step = NULL, updated_at = ?
WHERE id = ?;

-- name: EnableUserTOTP :exec
-- Step 23a: POST /me/totp/enable after the first code verified (step = anti-replay floor).
UPDATE users
SET totp_enabled = 1, totp_enabled_at = ?, totp_last_step = ?, updated_at = ?
WHERE id = ?;

-- name: ClearUserTOTP :exec
-- Step 23a: disable (user) or reset (admin).
UPDATE users
SET totp_secret_enc = NULL, totp_enabled = 0, totp_enabled_at = NULL, totp_last_step = NULL, updated_at = ?
WHERE id = ?;

-- name: AdvanceUserTOTPStep :execrows
-- Step 23a anti-replay: accept a code only when its time step is newer than
-- the last accepted one (0 rows = replay or concurrent use of the same code).
UPDATE users
SET totp_last_step = sqlc.arg('step')
WHERE id = sqlc.arg('id') AND (totp_last_step IS NULL OR totp_last_step < sqlc.arg('step'));

-- name: SoftDeleteUser :exec
UPDATE users SET deleted_at = ?, status = 'disabled', updated_at = ? WHERE id = ?;

-- name: HardDeleteUser :exec
DELETE FROM users WHERE id = ?;

-- name: SuggestUsers :many
-- Step 18b: username autocomplete for sharing (active users only, never the
-- caller). Username prefix or display-name substring; the parser has no
-- LIKE ... ESCAPE, so prefix_len/prefix/needle are computed in Go.
SELECT id, username, display_name FROM users
WHERE deleted_at IS NULL AND status = 'active' AND id <> sqlc.arg('self_id')
  AND (substr(username, 1, sqlc.arg('prefix_len')) = sqlc.arg('prefix')
       OR instr(lower(display_name), sqlc.arg('needle')) > 0)
ORDER BY username
LIMIT sqlc.arg('lim');

-- name: ListUsersPage :many
-- Step 19a: admin user list, keyset cursor on username (after = "" for the
-- first page). Filters as ListUsers.
SELECT * FROM users
WHERE deleted_at IS NULL
  AND username > sqlc.arg('after')
  AND (sqlc.narg('status') IS NULL OR status = sqlc.narg('status'))
  AND (sqlc.narg('role') IS NULL OR role = sqlc.narg('role'))
  AND (sqlc.narg('q') IS NULL OR username LIKE sqlc.narg('q') OR display_name LIKE sqlc.narg('q'))
ORDER BY username
LIMIT sqlc.arg('limit');

-- name: SumUserQuota :one
-- Step 19a dashboard: sum of quota_bytes of every live account.
SELECT COALESCE(SUM(quota_bytes), 0) FROM users WHERE deleted_at IS NULL;

-- name: TopQuotaUsers :many
-- Step 19a dashboard: accounts ordered by effective usage (used + trash + versions).
SELECT users.id, users.username, users.display_name, users.quota_bytes,
       (quota_usage.used_bytes + quota_usage.trash_bytes + quota_usage.versions_bytes) AS effective_bytes
FROM quota_usage JOIN users ON users.id = quota_usage.user_id
WHERE users.deleted_at IS NULL
ORDER BY effective_bytes DESC, users.username
LIMIT ?;

-- name: ListDeletedUsersBefore :many
-- Step 19a: purged accounts kept 30 days for the audit trail (SPEC 8.17).
SELECT * FROM users WHERE deleted_at IS NOT NULL AND deleted_at < sqlc.arg('before');
