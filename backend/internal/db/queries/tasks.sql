-- Task engine persistence (SPEC 4.2 tasks / task_logs, 7.3). Comments are
-- ASCII only (sqlc 1.31 offset bug). Every per-user query filters on user_id
-- so another user's id answers "not found".

-- name: InsertTask :exec
INSERT INTO tasks (
  id, user_id, type, state, title, payload_json, progress_unit, locks_json, hidden, retry_of, created_at, updated_at
) VALUES (
  sqlc.arg('id'), sqlc.arg('user_id'), sqlc.arg('type'), 'queued', sqlc.arg('title'), sqlc.arg('payload_json'),
  sqlc.arg('progress_unit'), sqlc.arg('locks_json'), sqlc.arg('hidden'), sqlc.arg('retry_of'), sqlc.arg('created_at'), sqlc.arg('updated_at')
);

-- name: GetTask :one
SELECT * FROM tasks WHERE id = sqlc.arg('id');

-- name: GetUserTask :one
SELECT * FROM tasks WHERE id = sqlc.arg('id') AND user_id = sqlc.arg('user_id');

-- name: ListUserTasks :many
SELECT * FROM tasks
WHERE user_id = sqlc.arg('user_id')
  AND hidden = 0
  AND (sqlc.arg('any_state') = 1 OR state = sqlc.arg('state'))
  AND (sqlc.arg('before') = '' OR created_at < sqlc.arg('before') OR (created_at = sqlc.arg('before') AND id < sqlc.arg('before_id')))
ORDER BY created_at DESC, id DESC
LIMIT sqlc.arg('max_rows');

-- name: ListTasksByState :many
SELECT * FROM tasks WHERE state = sqlc.arg('state') ORDER BY created_at, id;

-- name: CountUserTasksByState :one
SELECT COUNT(*) FROM tasks WHERE user_id = sqlc.arg('user_id') AND state = sqlc.arg('state');

-- name: SetTaskRunning :exec
UPDATE tasks SET state = 'running', started_at = sqlc.arg('started_at'), updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id');

-- name: SetTaskFinal :exec
UPDATE tasks SET
  state = sqlc.arg('state'),
  result_json = sqlc.arg('result_json'),
  error_code = sqlc.arg('error_code'),
  error_message = sqlc.arg('error_message'),
  progress_done = sqlc.arg('progress_done'),
  progress_total = sqlc.arg('progress_total'),
  eta_seconds = NULL,
  speed_bps = NULL,
  finished_at = sqlc.arg('finished_at'),
  updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id');

-- name: UpdateTaskProgress :exec
UPDATE tasks SET
  progress_done = sqlc.arg('progress_done'),
  progress_total = sqlc.arg('progress_total'),
  eta_seconds = sqlc.arg('eta_seconds'),
  speed_bps = sqlc.arg('speed_bps'),
  updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id');

-- name: SetTaskCancelRequested :execrows
UPDATE tasks SET cancel_requested = 1, updated_at = sqlc.arg('updated_at')
WHERE id = sqlc.arg('id') AND state IN ('queued','running');

-- name: MarkRunningTasksInterrupted :execrows
UPDATE tasks SET state = 'interrupted', error_code = 'INTERRUPTED', finished_at = sqlc.arg('finished_at'), updated_at = sqlc.arg('updated_at')
WHERE state = 'running';

-- name: DeleteUserTask :execrows
DELETE FROM tasks
WHERE id = sqlc.arg('id') AND user_id = sqlc.arg('user_id') AND state IN ('done','failed','cancelled','interrupted');

-- name: DeleteFinishedTasksBefore :execrows
DELETE FROM tasks
WHERE state IN ('done','failed','cancelled','interrupted') AND finished_at IS NOT NULL AND finished_at < sqlc.arg('before');

-- name: CountTasksByState :many
SELECT state, COUNT(*) AS n FROM tasks GROUP BY state;

-- name: InsertTaskLog :exec
INSERT INTO task_logs (task_id, at, level, message)
VALUES (sqlc.arg('task_id'), sqlc.arg('at'), sqlc.arg('level'), sqlc.arg('message'));

-- name: ListTaskLogs :many
SELECT * FROM task_logs
WHERE task_id = sqlc.arg('task_id') AND id > sqlc.arg('after_id')
ORDER BY id
LIMIT sqlc.arg('max_rows');

-- name: CountTaskLogs :one
SELECT COUNT(*) FROM task_logs WHERE task_id = sqlc.arg('task_id');
