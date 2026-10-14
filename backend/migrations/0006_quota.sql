-- +goose Up
-- Step 10a: incremental quota accounting (SPEC 4.2 quota_usage, 8.2, ADR-23).
-- used_bytes = files inside the user root (no trash/versions); the effective
-- usage compared with users.quota_bytes is used + trash + versions.

CREATE TABLE quota_usage (
  user_id        TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  used_bytes     INTEGER NOT NULL DEFAULT 0,
  trash_bytes    INTEGER NOT NULL DEFAULT 0,
  versions_bytes INTEGER NOT NULL DEFAULT 0,
  file_count     INTEGER NOT NULL DEFAULT 0,
  dir_count      INTEGER NOT NULL DEFAULT 0,
  calculated_at  TEXT,
  dirty          INTEGER NOT NULL DEFAULT 0,
  updated_at     TEXT NOT NULL
) STRICT, WITHOUT ROWID;

-- +goose Down
DROP TABLE quota_usage;
