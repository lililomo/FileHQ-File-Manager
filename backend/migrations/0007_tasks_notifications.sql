-- +goose Up
-- Step 11: persistent task engine and in-app notifications
-- (SPEC 4.2 tasks / task_logs / notifications, 7, 8.16, ADR-18).
-- user_id NULL = system task (admin only). payload_json never holds secrets.

CREATE TABLE tasks (
  id               TEXT PRIMARY KEY,
  user_id          TEXT REFERENCES users (id) ON DELETE CASCADE,
  type             TEXT NOT NULL,
  state            TEXT NOT NULL CHECK (state IN ('queued','running','done','failed','cancelled','interrupted')),
  title            TEXT NOT NULL,
  payload_json     TEXT NOT NULL,
  result_json      TEXT,
  error_code       TEXT,
  error_message    TEXT,
  progress_done    INTEGER NOT NULL DEFAULT 0,
  progress_total   INTEGER NOT NULL DEFAULT 0,
  progress_unit    TEXT NOT NULL DEFAULT 'items' CHECK (progress_unit IN ('bytes','items')),
  eta_seconds      INTEGER,
  speed_bps        INTEGER,
  cancel_requested INTEGER NOT NULL DEFAULT 0,
  locks_json       TEXT NOT NULL DEFAULT '[]',
  hidden           INTEGER NOT NULL DEFAULT 0,
  retry_of         TEXT,
  created_at       TEXT NOT NULL,
  started_at       TEXT,
  finished_at      TEXT,
  updated_at       TEXT NOT NULL
) STRICT;

CREATE INDEX tasks_user_created ON tasks (user_id, created_at DESC);
CREATE INDEX tasks_state ON tasks (state);

CREATE TABLE task_logs (
  id      INTEGER PRIMARY KEY AUTOINCREMENT,
  task_id TEXT NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
  at      TEXT NOT NULL,
  level   TEXT NOT NULL CHECK (level IN ('info','warn','error')),
  message TEXT NOT NULL
) STRICT;

CREATE INDEX task_logs_task ON task_logs (task_id, id);

CREATE TABLE notifications (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  type       TEXT NOT NULL,
  title      TEXT NOT NULL,
  body       TEXT,
  link       TEXT,
  read_at    TEXT,
  created_at TEXT NOT NULL
) STRICT;

CREATE INDEX notifications_user ON notifications (user_id, read_at, created_at DESC);

-- +goose Down
DROP TABLE notifications;
DROP TABLE task_logs;
DROP TABLE tasks;
