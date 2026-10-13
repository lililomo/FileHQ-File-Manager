-- name: InsertLoginAttempt :exec
INSERT INTO login_attempts (username, ip, success, at) VALUES (?, ?, ?, ?);

-- name: CountFailedLoginsByUsernameSince :one
SELECT COUNT(*) FROM login_attempts WHERE username = ? AND success = 0 AND at >= ?;

-- name: CountLoginAttemptsByIPSince :one
SELECT COUNT(*) FROM login_attempts WHERE ip = ? AND at >= ?;

-- name: CountFailedLoginsSince :one
SELECT COUNT(*) FROM login_attempts WHERE success = 0 AND at >= ?;

-- name: DeleteLoginAttemptsOlderThan :execrows
DELETE FROM login_attempts WHERE at < ?;
