-- name: GetMediaPosition :one
SELECT * FROM media_positions WHERE user_id = ? AND path = ?;

-- name: UpsertMediaPosition :one
INSERT INTO media_positions (user_id, path, seconds, duration, updated_at)
VALUES (?, ?, ?, ?, ?)
ON CONFLICT (user_id, path) DO UPDATE SET
  seconds = excluded.seconds,
  duration = excluded.duration,
  updated_at = excluded.updated_at
RETURNING *;

-- name: DeleteMediaPosition :execrows
DELETE FROM media_positions WHERE user_id = ? AND path = ?;

-- name: PruneMediaPositions :execrows
DELETE FROM media_positions
WHERE media_positions.user_id = sqlc.arg(user_id)
  AND media_positions.path NOT IN (
    SELECT keep.path FROM media_positions AS keep
    WHERE keep.user_id = sqlc.arg(user_id)
    ORDER BY keep.updated_at DESC, keep.path
    LIMIT sqlc.arg(keep)
  );
