-- name: GetMediaMetadata :one
SELECT * FROM media_metadata WHERE user_id = ? AND path = ?;

-- name: ListMediaMetadataByPaths :many
SELECT * FROM media_metadata
WHERE user_id = sqlc.arg('user_id') AND path IN (sqlc.slice('paths'));

-- name: UpsertMediaMetadata :exec
INSERT INTO media_metadata (
  user_id, path, size, mtime_unix, mime, width, height, duration_ms,
  codec_video, codec_audio, bitrate, exif_json, tags_json, has_cover,
  thumb_state, thumb_error, computed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (user_id, path) DO UPDATE SET
  size = excluded.size,
  mtime_unix = excluded.mtime_unix,
  mime = excluded.mime,
  width = excluded.width,
  height = excluded.height,
  duration_ms = excluded.duration_ms,
  codec_video = excluded.codec_video,
  codec_audio = excluded.codec_audio,
  bitrate = excluded.bitrate,
  exif_json = excluded.exif_json,
  tags_json = excluded.tags_json,
  has_cover = excluded.has_cover,
  thumb_state = excluded.thumb_state,
  thumb_error = excluded.thumb_error,
  computed_at = excluded.computed_at;

-- name: SetMediaThumbState :exec
UPDATE media_metadata
SET thumb_state = ?, thumb_error = ?, computed_at = ?
WHERE user_id = ? AND path = ?;

-- name: DeleteMediaMetadata :exec
DELETE FROM media_metadata WHERE user_id = ? AND path = ?;

-- prefix = "<dir>/" and prefix_len = len(prefix): sqlc's SQLite parser has no
-- LIKE ... ESCAPE, substr avoids wildcard escaping.
-- name: ListMediaMetadataPrefix :many
SELECT * FROM media_metadata
WHERE user_id = sqlc.arg('user_id')
  AND (path = sqlc.arg('path') OR substr(path, 1, sqlc.arg('prefix_len')) = sqlc.arg('prefix'));

-- name: DeleteMediaMetadataPrefix :execrows
DELETE FROM media_metadata
WHERE user_id = sqlc.arg('user_id')
  AND (path = sqlc.arg('path') OR substr(path, 1, sqlc.arg('prefix_len')) = sqlc.arg('prefix'));

-- name: CountMediaMetadata :one
SELECT COUNT(*) FROM media_metadata WHERE user_id = ?;
