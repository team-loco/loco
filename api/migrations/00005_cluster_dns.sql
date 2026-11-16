-- +goose Up
ALTER TABLE clusters ADD COLUMN dns_records JSONB NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE clusters DROP COLUMN dns_records;
