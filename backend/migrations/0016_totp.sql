-- +goose Up
-- Step 23a: optional TOTP two-factor authentication (SPEC §4.2, §8.1, §10.4).
-- totp_secret_enc / totp_enabled exist since 0001; add the verification
-- timestamp, the anti-replay step and the single-use recovery codes.
ALTER TABLE users ADD COLUMN totp_enabled_at TEXT;
ALTER TABLE users ADD COLUMN totp_last_step INTEGER;

CREATE TABLE IF NOT EXISTS totp_recovery_codes (
  id INTEGER PRIMARY KEY,
  user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  code_hash BLOB NOT NULL,
  used_at TEXT,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_totp_recovery_codes_user ON totp_recovery_codes(user_id);

-- +goose Down
DROP INDEX IF EXISTS idx_totp_recovery_codes_user;
DROP TABLE IF EXISTS totp_recovery_codes;
ALTER TABLE users DROP COLUMN totp_last_step;
ALTER TABLE users DROP COLUMN totp_enabled_at;
