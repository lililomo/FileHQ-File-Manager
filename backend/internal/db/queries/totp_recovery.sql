-- name: InsertTOTPRecoveryCode :exec
-- Step 23a: single-use TOTP recovery codes (SPEC 4.2 totp_recovery_codes; keep this file ASCII: sqlc star expansion miscounts multi-byte runes).
INSERT INTO totp_recovery_codes (user_id, code_hash, created_at) VALUES (?, ?, ?);

-- name: ListUnusedTOTPRecoveryCodes :many
SELECT * FROM totp_recovery_codes WHERE user_id = ? AND used_at IS NULL ORDER BY id;

-- name: CountUnusedTOTPRecoveryCodes :one
SELECT COUNT(*) FROM totp_recovery_codes WHERE user_id = ? AND used_at IS NULL;

-- name: UseTOTPRecoveryCode :execrows
UPDATE totp_recovery_codes SET used_at = ? WHERE id = ? AND used_at IS NULL;

-- name: DeleteTOTPRecoveryCodes :execrows
DELETE FROM totp_recovery_codes WHERE user_id = ?;
