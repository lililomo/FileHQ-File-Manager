-- +goose Up
-- Step 17b: last playback position per video (SPEC 4.2 media_positions,
-- 8.14 VideoPlayer "lanjut dari posisi terakhir"). At most 500 rows per user;
-- the least recently updated rows are pruned on write (LRU).

CREATE TABLE media_positions (
  user_id     TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  path        TEXT NOT NULL,
  seconds     REAL NOT NULL,
  duration    REAL,
  updated_at  TEXT NOT NULL,
  PRIMARY KEY (user_id, path)
) STRICT;

CREATE INDEX media_positions_lru ON media_positions (user_id, updated_at);

-- +goose Down
DROP TABLE media_positions;
