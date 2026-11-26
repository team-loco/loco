-- +goose Up
CREATE INDEX events_subject_seq_idx ON events (subject_type, subject_id, seq DESC) WHERE subject_id IS NOT NULL;
CREATE INDEX events_deployment_seq_idx ON events (deployment_id, seq DESC) WHERE deployment_id IS NOT NULL;

-- +goose Down
DROP INDEX events_deployment_seq_idx;
DROP INDEX events_subject_seq_idx;
