-- +goose Up
ALTER TABLE clusters DROP COLUMN capacity_cpu_millicores, DROP COLUMN capacity_memory_bytes;
ALTER TABLE cluster_heartbeats
    ADD COLUMN capacity_reported_at TIMESTAMPTZ,
    ADD COLUMN reported_cpu_millicores BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN requested_cpu_millicores BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN reported_memory_bytes BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN requested_memory_bytes BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN capacity_pods INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN assigned_pods INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN running_pods INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN health_message TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE clusters ADD COLUMN capacity_cpu_millicores BIGINT, ADD COLUMN capacity_memory_bytes BIGINT;
ALTER TABLE cluster_heartbeats
    DROP COLUMN health_message,
    DROP COLUMN running_pods,
    DROP COLUMN assigned_pods,
    DROP COLUMN capacity_pods,
    DROP COLUMN requested_memory_bytes,
    DROP COLUMN reported_memory_bytes,
    DROP COLUMN requested_cpu_millicores,
    DROP COLUMN reported_cpu_millicores,
    DROP COLUMN capacity_reported_at;
