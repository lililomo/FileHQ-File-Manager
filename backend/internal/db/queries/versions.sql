-- name: GetFileVersion :one
SELECT * FROM file_versions WHERE id = ? AND user_id = ?;

-- name: ListFileVersionsByPath :many
SELECT * FROM file_versions WHERE user_id = ? AND path = ? ORDER BY version_no DESC;

-- name: LatestFileVersion :one
SELECT * FROM file_versions WHERE user_id = ? AND path = ? ORDER BY version_no DESC LIMIT 1;

-- name: DeleteFileVersion :execrows
DELETE FROM file_versions WHERE id = ? AND user_id = ?;

-- name: SumFileVersionBytes :one
SELECT COALESCE(SUM(size), 0) AS total FROM file_versions WHERE user_id = ?;
