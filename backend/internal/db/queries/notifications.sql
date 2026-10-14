-- In-app notifications (SPEC 4.2 notifications, 8.16). ASCII comments only.

-- name: InsertNotification :exec
INSERT INTO notifications (id, user_id, type, title, body, link, created_at)
VALUES (sqlc.arg('id'), sqlc.arg('user_id'), sqlc.arg('type'), sqlc.arg('title'), sqlc.arg('body'), sqlc.arg('link'), sqlc.arg('created_at'));

-- name: ListNotifications :many
SELECT * FROM notifications
WHERE user_id = sqlc.arg('user_id')
  AND (sqlc.arg('unread_only') = 0 OR read_at IS NULL)
  AND (sqlc.arg('before') = '' OR created_at < sqlc.arg('before') OR (created_at = sqlc.arg('before') AND id < sqlc.arg('before_id')))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('max_rows');

-- name: CountUnreadNotifications :one
SELECT COUNT(*) FROM notifications WHERE user_id = sqlc.arg('user_id') AND read_at IS NULL;

-- name: MarkNotificationRead :execrows
UPDATE notifications SET read_at = sqlc.arg('read_at')
WHERE user_id = sqlc.arg('user_id') AND id = sqlc.arg('id') AND read_at IS NULL;

-- name: MarkAllNotificationsRead :execrows
UPDATE notifications SET read_at = sqlc.arg('read_at')
WHERE user_id = sqlc.arg('user_id') AND read_at IS NULL;

-- name: DeleteNotification :execrows
DELETE FROM notifications WHERE user_id = sqlc.arg('user_id') AND id = sqlc.arg('id');

-- name: PruneNotifications :execrows
DELETE FROM notifications
WHERE notifications.user_id = sqlc.arg('user_id')
  AND notifications.id NOT IN (
    SELECT n2.id FROM notifications AS n2 WHERE n2.user_id = sqlc.arg('user_id') ORDER BY n2.created_at DESC, n2.id DESC LIMIT sqlc.arg('keep')
  );
