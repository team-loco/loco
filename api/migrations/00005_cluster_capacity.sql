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


CREATE TABLE node_capacity_reports (
    id UUID PRIMARY KEY,
    cluster_id UUID NOT NULL REFERENCES clusters(id) ON DELETE CASCADE,
    generation BIGINT NOT NULL,
    sequence INTEGER NOT NULL,
    node_count INTEGER NOT NULL,
    allocation_count INTEGER NOT NULL,
    reported_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    complete BOOLEAN NOT NULL DEFAULT false,
    UNIQUE (cluster_id, complete)
);
CREATE TABLE node_capacity (
    report_id UUID NOT NULL REFERENCES node_capacity_reports(id) ON DELETE CASCADE,
    name TEXT NOT NULL,
    ready BOOLEAN NOT NULL,
    unschedulable BOOLEAN NOT NULL,
    cpu_allocatable BIGINT NOT NULL CHECK (cpu_allocatable >= 0),
    memory_allocatable BIGINT NOT NULL CHECK (memory_allocatable >= 0),
    cpu_requested BIGINT NOT NULL CHECK (cpu_requested >= 0),
    memory_requested BIGINT NOT NULL CHECK (memory_requested >= 0),
    PRIMARY KEY (report_id, name)
);
CREATE TABLE node_allocations (
    report_id UUID NOT NULL,
    node TEXT NOT NULL,
    workspace_id TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    environment_id TEXT NOT NULL,
    build_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('resource', 'build', 'system')),
    cpu_millicores BIGINT NOT NULL CHECK (cpu_millicores >= 0),
    memory_bytes BIGINT NOT NULL CHECK (memory_bytes >= 0),
    pods INTEGER NOT NULL CHECK (pods > 0),
    PRIMARY KEY (report_id, node, workspace_id, resource_id, kind, environment_id, build_id),
    FOREIGN KEY (report_id, node) REFERENCES node_capacity(report_id, name) ON DELETE CASCADE
);
CREATE INDEX node_allocations_workspace ON node_allocations(workspace_id, report_id);

-- +goose Down

DROP TABLE node_allocations;
DROP TABLE node_capacity;
DROP TABLE node_capacity_reports;

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
