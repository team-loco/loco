-- +goose Up
ALTER TABLE secrets
    ADD COLUMN created_by_type TEXT,
    ADD COLUMN created_by_id UUID;

-- +goose Down
ALTER TABLE secrets
    DROP COLUMN created_by_type,
    DROP COLUMN created_by_id;
