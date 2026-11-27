-- +goose NO TRANSACTION
-- +goose Up
-- The projection optimizer cannot match map lookups as aggregate keys on the pinned server.
ALTER TABLE ${DATABASE}.otel_metrics_gauge
    ADD COLUMN IF NOT EXISTS ClusterId LowCardinality(String) MATERIALIZED ResourceAttributes['loco.cluster.id'],
    ADD COLUMN IF NOT EXISTS NodeName LowCardinality(String) MATERIALIZED ResourceAttributes['k8s.node.name'],
    ADD COLUMN IF NOT EXISTS CollectionInterval LowCardinality(String) MATERIALIZED ResourceAttributes['loco.collection.interval'];

ALTER TABLE ${DATABASE}.otel_metrics_gauge ADD PROJECTION IF NOT EXISTS system_capacity_time
(
        SELECT WorkspaceId, ClusterId,
               MetricName, TimeUnix, _part_offset
        ORDER BY (WorkspaceId, ClusterId, MetricName, TimeUnix)
    );
ALTER TABLE ${DATABASE}.otel_metrics_gauge MATERIALIZE PROJECTION system_capacity_time;

ALTER TABLE ${DATABASE}.otel_metrics_gauge ADD PROJECTION IF NOT EXISTS latest_system_node_capacity
(
        SELECT WorkspaceId, ClusterId, MetricName, Flags,
               NodeName,
               argMax(Value, TimeUnix), max(TimeUnix),
               argMax(CollectionInterval, TimeUnix)
        GROUP BY WorkspaceId, ClusterId, MetricName, Flags,
                 NodeName
    );
ALTER TABLE ${DATABASE}.otel_metrics_gauge MATERIALIZE PROJECTION latest_system_node_capacity;

-- +goose Down
ALTER TABLE ${DATABASE}.otel_metrics_gauge DROP PROJECTION IF EXISTS latest_system_node_capacity;
ALTER TABLE ${DATABASE}.otel_metrics_gauge DROP PROJECTION IF EXISTS system_capacity_time;
ALTER TABLE ${DATABASE}.otel_metrics_gauge
    DROP COLUMN IF EXISTS CollectionInterval,
    DROP COLUMN IF EXISTS NodeName,
    DROP COLUMN IF EXISTS ClusterId;
