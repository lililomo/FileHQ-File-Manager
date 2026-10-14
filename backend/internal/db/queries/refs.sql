-- Path-keyed metadata tables (SPEC 5.7). Prefix matching uses substr because
-- sqlc's SQLite parser has no LIKE ... ESCAPE: prefix = old || '/',
-- prefix_len = len(prefix), from_pos = len(old) + 1.

-- name: UpsertFavorite :exec
INSERT INTO favorites (user_id, path, created_at) VALUES (?, ?, ?)
ON CONFLICT (user_id, path) DO NOTHING;

-- name: ListFavorites :many
SELECT * FROM favorites WHERE user_id = ? ORDER BY path;

-- name: RewriteFavorites :execrows
UPDATE favorites
SET path = sqlc.arg('new_path') || substr(path, sqlc.arg('from_pos'))
WHERE user_id = sqlc.arg('user_id')
  AND (path = sqlc.arg('old_path') OR substr(path, 1, sqlc.arg('prefix_len')) = sqlc.arg('prefix'));

-- name: DeleteFavoritesPrefix :execrows
DELETE FROM favorites
WHERE user_id = sqlc.arg('user_id')
  AND (path = sqlc.arg('path') OR substr(path, 1, sqlc.arg('prefix_len')) = sqlc.arg('prefix'));

-- name: UpsertPin :exec
INSERT INTO pins (user_id, path, created_at) VALUES (?, ?, ?)
ON CONFLICT (user_id, path) DO NOTHING;

-- name: ListPins :many
SELECT * FROM pins WHERE user_id = ? ORDER BY path;

-- name: RewritePins :execrows
UPDATE pins
SET path = sqlc.arg('new_path') || substr(path, sqlc.arg('from_pos'))
WHERE user_id = sqlc.arg('user_id')
  AND (path = sqlc.arg('old_path') OR substr(path, 1, sqlc.arg('prefix_len')) = sqlc.arg('prefix'));

-- name: DeletePinsPrefix :execrows
DELETE FROM pins
WHERE user_id = sqlc.arg('user_id')
  AND (path = sqlc.arg('path') OR substr(path, 1, sqlc.arg('prefix_len')) = sqlc.arg('prefix'));

-- name: UpsertFileNote :exec
INSERT INTO file_notes (user_id, path, note, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT (user_id, path) DO UPDATE SET note = excluded.note, updated_at = excluded.updated_at;

-- name: ListFileNotes :many
SELECT * FROM file_notes WHERE user_id = ? ORDER BY path;

-- name: RewriteFileNotes :execrows
UPDATE file_notes
SET path = sqlc.arg('new_path') || substr(path, sqlc.arg('from_pos'))
WHERE user_id = sqlc.arg('user_id')
  AND (path = sqlc.arg('old_path') OR substr(path, 1, sqlc.arg('prefix_len')) = sqlc.arg('prefix'));

-- name: DeleteFileNotesPrefix :execrows
DELETE FROM file_notes
WHERE user_id = sqlc.arg('user_id')
  AND (path = sqlc.arg('path') OR substr(path, 1, sqlc.arg('prefix_len')) = sqlc.arg('prefix'));

-- name: UpsertFileTag :exec
INSERT INTO file_tags (user_id, path, color, updated_at) VALUES (?, ?, ?, ?)
ON CONFLICT (user_id, path) DO UPDATE SET color = excluded.color, updated_at = excluded.updated_at;

-- name: ListFileTags :many
SELECT * FROM file_tags WHERE user_id = ? ORDER BY path;

-- name: RewriteFileTags :execrows
UPDATE file_tags
SET path = sqlc.arg('new_path') || substr(path, sqlc.arg('from_pos'))
WHERE user_id = sqlc.arg('user_id')
  AND (path = sqlc.arg('old_path') OR substr(path, 1, sqlc.arg('prefix_len')) = sqlc.arg('prefix'));

-- name: DeleteFileTagsPrefix :execrows
DELETE FROM file_tags
WHERE user_id = sqlc.arg('user_id')
  AND (path = sqlc.arg('path') OR substr(path, 1, sqlc.arg('prefix_len')) = sqlc.arg('prefix'));

-- name: InsertFileVersion :exec
INSERT INTO file_versions (id, user_id, path, version_no, size, sha256, blob_rel, reason, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListFileVersions :many
SELECT * FROM file_versions WHERE user_id = ? ORDER BY path, version_no DESC;

-- name: RewriteFileVersions :execrows
UPDATE file_versions
SET path = sqlc.arg('new_path') || substr(path, sqlc.arg('from_pos'))
WHERE user_id = sqlc.arg('user_id')
  AND (path = sqlc.arg('old_path') OR substr(path, 1, sqlc.arg('prefix_len')) = sqlc.arg('prefix'));

-- name: DeleteFileVersionsPrefix :execrows
DELETE FROM file_versions
WHERE user_id = sqlc.arg('user_id')
  AND (path = sqlc.arg('path') OR substr(path, 1, sqlc.arg('prefix_len')) = sqlc.arg('prefix'));
