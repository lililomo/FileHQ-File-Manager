-- +goose Up
-- Step 16a: open editor tabs per user (SPEC 4.2 editor_sessions, 8.13 "Tab &
-- sesi"). One row per user; tabs_json is the ordered tab array (max 20).

CREATE TABLE editor_sessions (
  user_id     TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  tabs_json   TEXT NOT NULL,
  active_path TEXT,
  updated_at  TEXT NOT NULL
) STRICT;

-- +goose Down
DROP TABLE editor_sessions;
