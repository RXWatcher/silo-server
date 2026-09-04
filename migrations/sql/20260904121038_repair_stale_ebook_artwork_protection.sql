-- +goose Up
-- +goose StatementBegin
WITH repaired AS (
    SELECT
        state.content_id,
        ARRAY(
            SELECT DISTINCT field
            FROM unnest(state.protected_fields) AS existing(field)
            WHERE field NOT IN ('poster_path', 'backdrop_path', 'logo_path')
               OR (
                    field = 'poster_path'
                    AND trim(COALESCE(item.poster_path, '')) <> ''
                    AND lower(trim(item.poster_path)) NOT LIKE 'http://%'
                    AND lower(trim(item.poster_path)) NOT LIKE 'https://%'
                    AND lower(trim(item.poster_path)) NOT LIKE 'ebook-metadata/ebooks/%'
               )
               OR (
                    field = 'backdrop_path'
                    AND trim(COALESCE(item.backdrop_path, '')) <> ''
                    AND lower(trim(item.backdrop_path)) NOT LIKE 'http://%'
                    AND lower(trim(item.backdrop_path)) NOT LIKE 'https://%'
                    AND lower(trim(item.backdrop_path)) NOT LIKE 'ebook-metadata/ebooks/%'
               )
               OR (
                    field = 'logo_path'
                    AND trim(COALESCE(item.logo_path, '')) <> ''
                    AND lower(trim(item.logo_path)) NOT LIKE 'http://%'
                    AND lower(trim(item.logo_path)) NOT LIKE 'https://%'
                    AND lower(trim(item.logo_path)) NOT LIKE 'ebook-metadata/ebooks/%'
               )
            ORDER BY field
        ) AS protected_fields
    FROM ebook_enrichment_state AS state
    JOIN media_items AS item ON item.content_id = state.content_id
)
UPDATE ebook_enrichment_state AS state
SET
    protected_fields = repaired.protected_fields,
    updated_at = now()
FROM repaired
WHERE state.content_id = repaired.content_id
  AND state.protected_fields IS DISTINCT FROM repaired.protected_fields;
-- +goose StatementEnd

-- +goose StatementBegin
WITH coverless_files AS (
    SELECT file.id
    FROM media_files AS file
    JOIN media_items AS item ON item.content_id = file.content_id
    JOIN media_folders AS folder ON folder.id = file.media_folder_id
    WHERE folder.type = 'ebooks'
      AND trim(COALESCE(item.poster_path, '')) = ''
      AND file.missing_since IS NULL
      AND file.group_key_version >= 2
)
UPDATE media_files AS file
SET
    group_key_version = 1,
    updated_at = now()
FROM coverless_files
WHERE coverless_files.id = file.id;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Removed entries represented artwork that no longer existed or was owned by
-- a remote provider. Recreating those stale protections would restore the bug.
SELECT 1;
-- +goose StatementEnd
