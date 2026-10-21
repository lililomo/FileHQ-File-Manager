-- +goose Up
-- Step 18a: sharing (SPEC 4.2 shares, share_recipients, share_access_logs;
-- 8.15; ADR-16). PROMPT numbered this 0011; media_positions (17b) took that
-- slot. Public links keep only the SHA-256 of the token plus an AES-GCM copy
-- (token_enc) so the owner can copy the link again; share_recipients is used
-- from Step 18b.

CREATE TABLE shares (
  id               TEXT PRIMARY KEY,
  owner_user_id    TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  path             TEXT NOT NULL,
  is_dir           INTEGER NOT NULL,
  mode             TEXT NOT NULL CHECK (mode IN ('public', 'users')),
  token_hash       TEXT UNIQUE,
  slug             TEXT UNIQUE,
  password_hash    TEXT,
  expires_at       TEXT,
  max_downloads    INTEGER,
  download_count   INTEGER NOT NULL DEFAULT 0,
  view_count       INTEGER NOT NULL DEFAULT 0,
  allow_download   INTEGER NOT NULL DEFAULT 1,
  allow_upload     INTEGER NOT NULL DEFAULT 0,
  allow_reshare    INTEGER NOT NULL DEFAULT 0,
  hide_owner       INTEGER NOT NULL DEFAULT 0,
  hide_listing     INTEGER NOT NULL DEFAULT 0,
  note             TEXT,
  token_enc        BLOB,
  disabled_at      TEXT,
  disabled_reason  TEXT CHECK (disabled_reason IN ('manual', 'owner_disabled', 'item_trashed', 'admin_revoked')),
  broken_at        TEXT,
  last_access_at   TEXT,
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL
) STRICT;

CREATE INDEX shares_owner_idx ON shares (owner_user_id, path);
CREATE INDEX shares_expires_idx ON shares (expires_at);

CREATE TABLE share_recipients (
  share_id           TEXT NOT NULL REFERENCES shares (id) ON DELETE CASCADE,
  recipient_user_id  TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  permission         TEXT NOT NULL CHECK (permission IN ('viewer', 'editor')),
  note               TEXT,
  created_at         TEXT NOT NULL,
  PRIMARY KEY (share_id, recipient_user_id)
) STRICT;

CREATE INDEX share_recipients_user_idx ON share_recipients (recipient_user_id);

CREATE TABLE share_access_logs (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  share_id    TEXT NOT NULL REFERENCES shares (id) ON DELETE CASCADE,
  at          TEXT NOT NULL,
  ip_trunc    TEXT NOT NULL,
  action      TEXT NOT NULL CHECK (action IN ('view', 'list', 'download', 'zip', 'unlock_ok', 'unlock_fail', 'upload', 'denied')),
  user_id     TEXT,
  user_agent  TEXT NOT NULL DEFAULT '',
  path        TEXT
) STRICT;

CREATE INDEX share_access_logs_share_idx ON share_access_logs (share_id, at);

-- +goose Down
DROP TABLE share_access_logs;
DROP TABLE share_recipients;
DROP TABLE shares;
