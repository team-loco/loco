-- +goose NO TRANSACTION
-- +goose Up
ALTER TABLE ${DATABASE}.otel_metrics_gauge ADD PROJECTION IF NOT EXISTS system_capacity_time
(
        SELECT WorkspaceId, ResourceAttributes['loco.cluster.id'] AS ClusterId,
               MetricName, TimeUnix, _part_offset
        ORDER BY (WorkspaceId, ClusterId, MetricName, TimeUnix)
    );
ALTER TABLE ${DATABASE}.otel_metrics_gauge MATERIALIZE PROJECTION system_capacity_time;

ALTER TABLE ${DATABASE}.otel_metrics_gauge ADD PROJECTION IF NOT EXISTS latest_system_node_capacity
(
        SELECT WorkspaceId, ResourceAttributes['loco.cluster.id'], MetricName, Flags,
               ResourceAttributes['k8s.node.name'],
               argMax(Value, TimeUnix), max(TimeUnix),
               argMax(ResourceAttributes['loco.collection.interval'], TimeUnix)
        GROUP BY WorkspaceId, ResourceAttributes['loco.cluster.id'], MetricName, Flags,
                 ResourceAttributes['k8s.node.name']
    );
ALTER TABLE ${DATABASE}.otel_metrics_gauge MATERIALIZE PROJECTION latest_system_node_capacity;

-- +goose Down
ALTER TABLE ${DATABASE}.otel_metrics_gauge DROP PROJECTION IF EXISTS latest_system_node_capacity;
ALTER TABLE ${DATABASE}.otel_metrics_gauge DROP PROJECTION IF EXISTS system_capacity_time;
