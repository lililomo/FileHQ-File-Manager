-- +goose Up
-- media_metadata: cached dimensions/duration/codec/tags per file plus the
-- thumbnail state (SPEC §4.2, §8.4). (size, mtime_unix) is the validity key:
-- a row whose key differs from the file on disk is recomputed. Rows are
-- removed when the file is deleted or moved (prefix rewrite on move).
CREATE TABLE media_metadata (
  user_id     TEXT NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  path        TEXT NOT NULL,
  size        INTEGER NOT NULL,
  mtime_unix  INTEGER NOT NULL, -- unix time in nanoseconds
  mime        TEXT NOT NULL,     -- from magic bytes
  width       INTEGER,
  height      INTEGER,
  duration_ms INTEGER,
  codec_video TEXT,
  codec_audio TEXT,
  bitrate     INTEGER,
  exif_json   TEXT,
  tags_json   TEXT,
  has_cover   INTEGER NOT NULL DEFAULT 0,
  thumb_state TEXT NOT NULL CHECK (thumb_state IN ('pending', 'ready', 'failed', 'unsupported')),
  thumb_error TEXT,
  computed_at TEXT NOT NULL,
  PRIMARY KEY (user_id, path)
) STRICT, WITHOUT ROWID;
CREATE INDEX media_metadata_mime_idx ON media_metadata (user_id, mime);

-- +goose Down
DROP TABLE media_metadata;
