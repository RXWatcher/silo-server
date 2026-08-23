-- +goose Up
-- Persist ffprobe's real audio metadata for first-class music discovery.
CREATE TABLE IF NOT EXISTS music_file_tags (
    media_file_id INTEGER PRIMARY KEY REFERENCES media_files(id) ON DELETE CASCADE,
    tags JSONB NOT NULL DEFAULT '{}'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS music_file_tags_artist_idx ON music_file_tags ((lower(tags->>'artist')));
CREATE INDEX IF NOT EXISTS music_file_tags_album_idx ON music_file_tags ((lower(tags->>'album')));

-- +goose Down
DROP TABLE IF EXISTS music_file_tags;
