-- +goose Up
-- users: application accounts (SPEC §4.2). Times are ISO-8601 UTC with millis (TEXT).
CREATE TABLE users (
  id                   TEXT    PRIMARY KEY,
  username             TEXT    NOT NULL,
  display_name         TEXT    NOT NULL,
  role                 TEXT    NOT NULL CHECK (role IN ('admin', 'user')),
  status               TEXT    NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'disabled')),
  password_hash        TEXT    NOT NULL,
  must_change_password INTEGER NOT NULL DEFAULT 0,
  password_changed_at  TEXT,
  quota_bytes          INTEGER NOT NULL,
  home_provisioned_at  TEXT,
  home_warning         TEXT,
  last_login_at        TEXT,
  failed_login_count   INTEGER NOT NULL DEFAULT 0,
  locked_until         TEXT,
  totp_secret_enc      BLOB,
  totp_enabled         INTEGER NOT NULL DEFAULT 0,
  created_by           TEXT,
  created_at           TEXT    NOT NULL,
  updated_at           TEXT    NOT NULL,
  deleted_at           TEXT
) STRICT;
CREATE UNIQUE INDEX users_username_uq ON users (username);
CREATE INDEX users_status_idx ON users (status);

-- sessions: server-side sessions; id = SHA-256 hex of the cookie token.
CREATE TABLE sessions (
  id                  TEXT PRIMARY KEY,
  user_id             TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  csrf_secret         BLOB NOT NULL,
  created_at          TEXT NOT NULL,
  last_seen_at        TEXT NOT NULL,
  idle_expires_at     TEXT NOT NULL,
  absolute_expires_at TEXT NOT NULL,
  ip                  TEXT NOT NULL,
  user_agent          TEXT NOT NULL,
  device_label        TEXT,
  revoked_at          TEXT
) STRICT;
CREATE INDEX sessions_user_idx ON sessions (user_id);
CREATE INDEX sessions_idle_idx ON sessions (idle_expires_at);

-- +goose Down
DROP TABLE sessions;
DROP TABLE users;
