-- Step 10b: favorites, pins, notes and tags read per folder (listing
-- enrichment) and per item. Folder scans use the same substr prefix trick as
-- refs.sql: prefix = dir || '/' ('/' for the root), prefix_len = len(prefix),
-- from_pos = prefix_len + 1; a direct child has no '/' after the prefix.

-- name: ListFavoritesInDir :many
SELECT * FROM favorites
WHERE user_id = sqlc.arg('user_id')
  AND substr(path, 1, sqlc.arg('prefix_len')) = sqlc.arg('prefix')
  AND instr(substr(path, sqlc.arg('from_pos')), '/') = 0;

-- name: ListPinsInDir :many
SELECT * FROM pins
WHERE user_id = sqlc.arg('user_id')
  AND substr(path, 1, sqlc.arg('prefix_len')) = sqlc.arg('prefix')
  AND instr(substr(path, sqlc.arg('from_pos')), '/') = 0;

-- name: CountPinsInDir :one
SELECT count(*) FROM pins
WHERE user_id = sqlc.arg('user_id')
  AND substr(path, 1, sqlc.arg('prefix_len')) = sqlc.arg('prefix')
  AND instr(substr(path, sqlc.arg('from_pos')), '/') = 0;

-- name: ListFileNotesInDir :many
SELECT * FROM file_notes
WHERE user_id = sqlc.arg('user_id')
  AND substr(path, 1, sqlc.arg('prefix_len')) = sqlc.arg('prefix')
  AND instr(substr(path, sqlc.arg('from_pos')), '/') = 0;

-- name: ListFileTagsInDir :many
SELECT * FROM file_tags
WHERE user_id = sqlc.arg('user_id')
  AND substr(path, 1, sqlc.arg('prefix_len')) = sqlc.arg('prefix')
  AND instr(substr(path, sqlc.arg('from_pos')), '/') = 0;

-- name: GetFavorite :one
SELECT * FROM favorites WHERE user_id = ? AND path = ?;

-- name: GetPin :one
SELECT * FROM pins WHERE user_id = ? AND path = ?;

-- name: GetFileNote :one
SELECT * FROM file_notes WHERE user_id = ? AND path = ?;

-- name: GetFileTag :one
SELECT * FROM file_tags WHERE user_id = ? AND path = ?;

-- name: DeleteFavorite :execrows
DELETE FROM favorites WHERE user_id = ? AND path = ?;

-- name: DeletePin :execrows
DELETE FROM pins WHERE user_id = ? AND path = ?;

-- name: DeleteFileNote :execrows
DELETE FROM file_notes WHERE user_id = ? AND path = ?;

-- name: DeleteFileTag :execrows
DELETE FROM file_tags WHERE user_id = ? AND path = ?;

-- name: ListFileTagsByColor :many
SELECT * FROM file_tags WHERE user_id = ? AND color = ? ORDER BY path;

-- name: CountFavorites :one
SELECT count(*) FROM favorites WHERE user_id = ?;
