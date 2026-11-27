-- +goose Up
-- Capacity observations live in ClickHouse. Keep heartbeat liveness and diagnostics.
ALTER TABLE cluster_heartbeats
    DROP COLUMN capacity_reported_at,
    DROP COLUMN reported_cpu_millicores,
    DROP COLUMN requested_cpu_millicores,
    DROP COLUMN reported_memory_bytes,
    DROP COLUMN requested_memory_bytes,
    DROP COLUMN capacity_pods,
    DROP COLUMN assigned_pods,
    DROP COLUMN running_pods;

-- +goose Down
ALTER TABLE cluster_heartbeats
    ADD COLUMN capacity_reported_at TIMESTAMPTZ,
    ADD COLUMN reported_cpu_millicores BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN requested_cpu_millicores BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN reported_memory_bytes BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN requested_memory_bytes BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN capacity_pods INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN assigned_pods INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN running_pods INTEGER NOT NULL DEFAULT 0;
