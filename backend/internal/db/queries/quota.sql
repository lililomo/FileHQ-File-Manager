-- Quota accounting (SPEC 4.2 quota_usage, 8.2, ADR-23). Deltas are applied
-- in the same transaction as the metadata change; counters never go below 0.

-- name: GetQuotaUsage :one
SELECT * FROM quota_usage WHERE user_id = ?;

-- name: ApplyQuotaDelta :exec
INSERT INTO quota_usage (user_id, used_bytes, trash_bytes, versions_bytes, file_count, dir_count, calculated_at, dirty, updated_at)
VALUES (?1, max(0, ?2), max(0, ?3), max(0, ?4), 0, 0, NULL, 1, ?5)
ON CONFLICT (user_id) DO UPDATE SET
  used_bytes = max(0, used_bytes + ?2),
  trash_bytes = max(0, trash_bytes + ?3),
  versions_bytes = max(0, versions_bytes + ?4),
  updated_at = ?5;

-- name: SetQuotaUsage :exec
INSERT INTO quota_usage (user_id, used_bytes, trash_bytes, versions_bytes, file_count, dir_count, calculated_at, dirty, updated_at)
VALUES (sqlc.arg('user_id'), sqlc.arg('used_bytes'), sqlc.arg('trash_bytes'), sqlc.arg('versions_bytes'), sqlc.arg('file_count'), sqlc.arg('dir_count'), sqlc.arg('calculated_at'), 0, sqlc.arg('updated_at'))
ON CONFLICT (user_id) DO UPDATE SET
  used_bytes = excluded.used_bytes,
  trash_bytes = excluded.trash_bytes,
  versions_bytes = excluded.versions_bytes,
  file_count = excluded.file_count,
  dir_count = excluded.dir_count,
  calculated_at = excluded.calculated_at,
  dirty = 0,
  updated_at = excluded.updated_at;

-- name: SetQuotaDirty :exec
INSERT INTO quota_usage (user_id, used_bytes, trash_bytes, versions_bytes, file_count, dir_count, calculated_at, dirty, updated_at)
VALUES (sqlc.arg('user_id'), 0, 0, 0, 0, 0, NULL, 1, sqlc.arg('updated_at'))
ON CONFLICT (user_id) DO UPDATE SET dirty = 1, updated_at = excluded.updated_at;

-- name: ListDirtyQuotaUsers :many
SELECT user_id FROM quota_usage WHERE dirty = 1 ORDER BY updated_at;

-- name: SumQuotaUsed :one
SELECT COALESCE(SUM(used_bytes + trash_bytes + versions_bytes), 0) FROM quota_usage;
