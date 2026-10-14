-- +goose Up
-- Step 9a: Recycle Bin rows, activity journal and the path-keyed metadata
-- tables whose references move with rename/move (SPEC 4.2, 5.7, 8.5, 8.6).
-- favorites/pins/file_notes/file_tags/file_versions are populated by later
-- steps (10b, 16b); they exist now so UpdateReferences is testable.

CREATE TABLE trash_items (
  id            TEXT PRIMARY KEY,
  user_id       TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  original_path TEXT NOT NULL,
  name          TEXT NOT NULL,
  is_dir        INTEGER NOT NULL,
  size_bytes    INTEGER NOT NULL,
  item_count    INTEGER NOT NULL,
  kind          TEXT NOT NULL CHECK (kind IN ('file', 'folder', 'image', 'document', 'video', 'audio', 'archive', 'other')),
  deleted_at    TEXT NOT NULL,
  expires_at    TEXT,
  batch_id      TEXT,
  restored_at   TEXT,
  purged_at     TEXT
) STRICT;
CREATE INDEX trash_items_user_deleted_idx ON trash_items (user_id, deleted_at DESC);
CREATE INDEX trash_items_expires_idx ON trash_items (expires_at);
CREATE INDEX trash_items_batch_idx ON trash_items (user_id, batch_id);

CREATE TABLE activity_logs (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  at           TEXT NOT NULL,
  action       TEXT NOT NULL,
  path         TEXT,
  path2        TEXT,
  details_json TEXT,
  batch_id     TEXT
) STRICT;
CREATE INDEX activity_logs_user_at_idx ON activity_logs (user_id, at DESC);
CREATE INDEX activity_logs_user_path_idx ON activity_logs (user_id, path);

CREATE TABLE favorites (
  user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  path       TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (user_id, path)
) STRICT, WITHOUT ROWID;

CREATE TABLE pins (
  user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  path       TEXT NOT NULL,
  created_at TEXT NOT NULL,
  PRIMARY KEY (user_id, path)
) STRICT, WITHOUT ROWID;

CREATE TABLE file_notes (
  user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  path       TEXT NOT NULL,
  note       TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (user_id, path)
) STRICT, WITHOUT ROWID;

CREATE TABLE file_tags (
  user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  path       TEXT NOT NULL,
  color      TEXT NOT NULL CHECK (color IN ('red', 'orange', 'yellow', 'green', 'blue', 'purple', 'gray')),
  updated_at TEXT NOT NULL,
  PRIMARY KEY (user_id, path)
) STRICT, WITHOUT ROWID;

CREATE TABLE file_versions (
  id         TEXT PRIMARY KEY,
  user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  path       TEXT NOT NULL,
  version_no INTEGER NOT NULL,
  size       INTEGER NOT NULL,
  sha256     TEXT NOT NULL,
  blob_rel   TEXT NOT NULL,
  reason     TEXT NOT NULL CHECK (reason IN ('save', 'force_save', 'restore', 'replace_in_files', 'external_overwrite')),
  created_at TEXT NOT NULL
) STRICT;
CREATE UNIQUE INDEX file_versions_path_no_uq ON file_versions (user_id, path, version_no DESC);

-- +goose Down
DROP TABLE file_versions;
DROP TABLE file_tags;
DROP TABLE file_notes;
DROP TABLE pins;
DROP TABLE favorites;
DROP TABLE activity_logs;
DROP TABLE trash_items;
