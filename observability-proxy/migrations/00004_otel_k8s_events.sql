-- +goose NO TRANSACTION
-- +goose Up
CREATE TABLE IF NOT EXISTS ${DATABASE}.otel_k8s_events
(
    `Timestamp` DateTime64(9) CODEC(Delta(8), ZSTD(1)),
    `TraceId` String CODEC(ZSTD(1)),
    `SpanId` String CODEC(ZSTD(1)),
    `TraceFlags` UInt8,
    `SeverityText` LowCardinality(String) CODEC(ZSTD(1)),
    `SeverityNumber` UInt8,
    `ServiceName` LowCardinality(String) CODEC(ZSTD(1)),
    `Body` String CODEC(ZSTD(1)),
    `ResourceSchemaUrl` LowCardinality(String) CODEC(ZSTD(1)),
    `ResourceAttributes` Map(LowCardinality(String), String) CODEC(ZSTD(1)),
    `ScopeSchemaUrl` LowCardinality(String) CODEC(ZSTD(1)),
    `ScopeName` String CODEC(ZSTD(1)),
    `ScopeVersion` LowCardinality(String) CODEC(ZSTD(1)),
    `ScopeAttributes` Map(LowCardinality(String), String) CODEC(ZSTD(1)),
    `LogAttributes` Map(LowCardinality(String), String) CODEC(ZSTD(1)),
    `EventName` String CODEC(ZSTD(1)),
    `OrganizationId` LowCardinality(String) MATERIALIZED ResourceAttributes['loco.organization.id'] CODEC(ZSTD(1)),
    `WorkspaceId` LowCardinality(String) MATERIALIZED ResourceAttributes['loco.workspace.id'] CODEC(ZSTD(1)),
    `EnvironmentId` LowCardinality(String) MATERIALIZED ResourceAttributes['loco.environment.id'] CODEC(ZSTD(1)),
    `ResourceId` LowCardinality(String) MATERIALIZED ResourceAttributes['loco.resource.id'] CODEC(ZSTD(1)),
    `EventUid` String MATERIALIZED JSONExtractString(Body, 'object', 'metadata', 'uid') CODEC(ZSTD(1)),
    `EventType` LowCardinality(String) MATERIALIZED JSONExtractString(Body, 'object', 'type') CODEC(ZSTD(1)),
    `Reason` LowCardinality(String) MATERIALIZED JSONExtractString(Body, 'object', 'reason') CODEC(ZSTD(1)),
    `Message` String MATERIALIZED JSONExtractString(Body, 'object', 'note') CODEC(ZSTD(1)),
    `ObjectKind` LowCardinality(String) MATERIALIZED JSONExtractString(Body, 'object', 'regarding', 'kind') CODEC(ZSTD(1)),
    `ObjectName` String MATERIALIZED JSONExtractString(Body, 'object', 'regarding', 'name') CODEC(ZSTD(1)),
    `Count` UInt64 MATERIALIZED greatest(1, JSONExtractUInt(Body, 'object', 'deprecatedCount'), JSONExtractUInt(Body, 'object', 'series', 'count'))
)
ENGINE = MergeTree
PARTITION BY toDate(Timestamp)
ORDER BY (WorkspaceId, ResourceId, Timestamp)
TTL toDateTime(Timestamp) + ${LOGS_TTL}
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;
