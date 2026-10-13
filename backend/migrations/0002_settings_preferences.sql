-- +goose Up
-- settings: admin overrides of env defaults (SPEC §3.3, §4.2). key = env name lowercase or dotted key.
CREATE TABLE settings (
  key        TEXT PRIMARY KEY,
  value_json TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  updated_by TEXT
) STRICT;

-- user_preferences: one JSON document per user (keys in SPEC §8.18), max 64 KB.
CREATE TABLE user_preferences (
  user_id    TEXT PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
  prefs_json TEXT NOT NULL DEFAULT '{}',
  updated_at TEXT NOT NULL
) STRICT;

-- folder_view_prefs: per-folder view/sort memory (max 500 rows per user).
CREATE TABLE folder_view_prefs (
  user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  path         TEXT NOT NULL,
  view_mode    TEXT NOT NULL CHECK (view_mode IN ('details', 'list', 'tiles', 'large', 'xlarge')),
  sort_field   TEXT NOT NULL,
  sort_dir     TEXT NOT NULL CHECK (sort_dir IN ('asc', 'desc')),
  sort2_field  TEXT,
  sort2_dir    TEXT CHECK (sort2_dir IS NULL OR sort2_dir IN ('asc', 'desc')),
  columns_json TEXT,
  updated_at   TEXT NOT NULL,
  PRIMARY KEY (user_id, path)
) STRICT, WITHOUT ROWID;
CREATE INDEX folder_view_prefs_updated_idx ON folder_view_prefs (user_id, updated_at);

-- +goose Down
DROP TABLE folder_view_prefs;
DROP TABLE user_preferences;
DROP TABLE settings;
