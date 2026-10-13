-- +goose Up
-- audit_logs: security-relevant actions for admins (SPEC §4.2, actions in §8.16). Append-only.
CREATE TABLE audit_logs (
  id             INTEGER PRIMARY KEY AUTOINCREMENT,
  at             TEXT    NOT NULL,
  actor_user_id  TEXT    REFERENCES users (id) ON DELETE SET NULL,
  actor_username TEXT,
  action         TEXT    NOT NULL,
  target_type    TEXT,
  target_id      TEXT,
  ip             TEXT    NOT NULL,
  user_agent     TEXT    NOT NULL,
  details_json   TEXT
) STRICT;
CREATE INDEX audit_logs_at_idx ON audit_logs (at);
CREATE INDEX audit_logs_actor_idx ON audit_logs (actor_user_id, at);
CREATE INDEX audit_logs_action_idx ON audit_logs (action, at);

-- login_attempts: by username (no FK: attempts for unknown usernames are recorded too). Retention 30 days.
CREATE TABLE login_attempts (
  id       INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT    NOT NULL,
  ip       TEXT    NOT NULL,
  success  INTEGER NOT NULL,
  at       TEXT    NOT NULL
) STRICT;
CREATE INDEX login_attempts_username_idx ON login_attempts (username, at);
CREATE INDEX login_attempts_ip_idx ON login_attempts (ip, at);

-- +goose Down
DROP TABLE login_attempts;
DROP TABLE audit_logs;
