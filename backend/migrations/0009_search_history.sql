-- +goose Up
-- Step 15: search history (SPEC 4.2 search_history, 8.12). 50 newest rows
-- per user are kept; the service prunes after every insert.

CREATE TABLE search_history (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id      TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  kind         TEXT NOT NULL CHECK (kind IN ('name', 'content')),
  query        TEXT NOT NULL,
  scope_path   TEXT NOT NULL,
  filters_json TEXT,
  at           TEXT NOT NULL
) STRICT;
CREATE INDEX search_history_user_at_idx ON search_history (user_id, at DESC);

-- +goose Down
DROP TABLE search_history;
