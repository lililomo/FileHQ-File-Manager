-- +goose Up
-- Step 12a: resumable (tus) upload sessions (SPEC 4.2 upload_sessions, 6.5, 8.8, ADR-03).
-- id is the tus upload id (UUIDv7 chosen in PreUploadCreate). The quota
-- reservation of a user is the sum of size over rows in state 'active';
-- share_id (file requests, Step 18a) has no FK until the shares table exists.

CREATE TABLE upload_sessions (
  id              TEXT PRIMARY KEY,
  user_id         TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  share_id        TEXT,
  dest_dir        TEXT NOT NULL,
  filename        TEXT NOT NULL,
  relative_path   TEXT NOT NULL DEFAULT '',
  size            INTEGER NOT NULL,
  conflict_policy TEXT NOT NULL CHECK (conflict_policy IN ('overwrite','skip','rename')),
  state           TEXT NOT NULL CHECK (state IN ('active','finalized','aborted','expired')),
  finalized_path  TEXT,
  last_modified   INTEGER,
  created_at      TEXT NOT NULL,
  updated_at      TEXT NOT NULL,
  expires_at      TEXT NOT NULL
) STRICT;

CREATE INDEX idx_upload_sessions_user_state ON upload_sessions (user_id, state);
CREATE INDEX idx_upload_sessions_expires ON upload_sessions (expires_at);

-- +goose Down
DROP TABLE upload_sessions;
