-- name: InsertSearchHistory :one
INSERT INTO search_history (user_id, kind, query, scope_path, filters_json, at)
VALUES (?, ?, ?, ?, ?, ?)
RETURNING *;

-- name: DeleteSearchHistoryDuplicate :execrows
DELETE FROM search_history
WHERE user_id = ? AND kind = ? AND query = ? AND scope_path = ?;

-- name: ListSearchHistory :many
SELECT * FROM search_history
WHERE user_id = ?1 AND (?2 = '' OR kind = ?2)
ORDER BY at DESC, id DESC
LIMIT ?3;

-- name: PruneSearchHistory :execrows
DELETE FROM search_history
WHERE id IN (
  SELECT h.id FROM search_history AS h
  WHERE h.user_id = ?
  ORDER BY h.at DESC, h.id DESC
  LIMIT -1 OFFSET ?
);

-- name: DeleteSearchHistory :execrows
DELETE FROM search_history WHERE user_id = ?;
