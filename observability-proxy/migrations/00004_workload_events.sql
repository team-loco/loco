-- +goose NO TRANSACTION
-- +goose Up
CREATE TABLE IF NOT EXISTS ${DATABASE}.otel_workload_events
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
    `ClusterUid` LowCardinality(String) MATERIALIZED ResourceAttributes['k8s.cluster.uid'],
    `Region` LowCardinality(String) MATERIALIZED ResourceAttributes['loco.region'],
    `ObjectUid` String MATERIALIZED JSONExtractString(Body, 'uid'),
    `ObjectKind` LowCardinality(String) MATERIALIZED JSONExtractString(Body, 'kind'),
    `ObjectNamespace` LowCardinality(String) MATERIALIZED JSONExtractString(Body, 'namespace'),
    `ResourceVersion` String MATERIALIZED JSONExtractString(Body, 'resourceVersion'),
    `SubjectUid` String MATERIALIZED JSONExtractString(Body, 'regarding', 'uid'),
    `SubjectKind` LowCardinality(String) MATERIALIZED JSONExtractString(Body, 'regarding', 'kind'),
    `SubjectNamespace` LowCardinality(String) MATERIALIZED JSONExtractString(Body, 'regarding', 'namespace'),
    `CreatedAt` DateTime64(9) MATERIALIZED parseDateTime64BestEffortOrZero(JSONExtractString(Body, 'creationTimestamp'), 9, 'UTC'),
    `Occurrences` UInt64 MATERIALIZED multiIf(JSONHas(Body, 'series', 'count'), JSONExtractUInt(Body, 'series', 'count'), JSONHas(Body, 'deprecatedCount'), JSONExtractUInt(Body, 'deprecatedCount'), 1),
    `FirstSeen` DateTime64(9) MATERIALIZED parseDateTime64BestEffortOrZero(coalesce(nullIf(JSONExtractString(Body, 'deprecatedFirstTimestamp'), ''), nullIf(JSONExtractString(Body, 'eventTime'), ''), JSONExtractString(Body, 'creationTimestamp')), 9, 'UTC'),
    `LastSeen` DateTime64(9) MATERIALIZED parseDateTime64BestEffortOrZero(coalesce(nullIf(JSONExtractString(Body, 'series', 'lastObservedTime'), ''), nullIf(JSONExtractString(Body, 'deprecatedLastTimestamp'), ''), nullIf(JSONExtractString(Body, 'eventTime'), ''), JSONExtractString(Body, 'creationTimestamp')), 9, 'UTC')
)
ENGINE = MergeTree
PARTITION BY toDate(Timestamp)
ORDER BY (ClusterUid, ObjectUid, Timestamp, ResourceVersion)
TTL toDateTime(Timestamp) + ${EVENTS_TTL} + toIntervalSecond(1)
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

CREATE TABLE IF NOT EXISTS ${DATABASE}.otel_workload_owners
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
    `ClusterUid` LowCardinality(String) MATERIALIZED ResourceAttributes['k8s.cluster.uid'],
    `Region` LowCardinality(String) MATERIALIZED ResourceAttributes['loco.region'],
    `ObjectUid` String MATERIALIZED JSONExtractString(Body, 'uid'),
    `ObjectKind` LowCardinality(String) MATERIALIZED JSONExtractString(Body, 'kind'),
    `ObjectNamespace` LowCardinality(String) MATERIALIZED JSONExtractString(Body, 'namespace'),
    `ResourceVersion` String MATERIALIZED JSONExtractString(Body, 'resourceVersion'),
    `ApplicationUid` String MATERIALIZED JSONExtractString(Body, 'applicationUid'),
    `OwnerUids` Array(String) MATERIALIZED arrayMap(owner -> JSONExtractString(owner, 'uid'), arrayFilter(owner -> JSONExtractBool(owner, 'controller'), JSONExtractArrayRaw(Body, 'ownerReferences'))),
    `OwnerKinds` Array(String) MATERIALIZED arrayMap(owner -> JSONExtractString(owner, 'kind'), arrayFilter(owner -> JSONExtractBool(owner, 'controller'), JSONExtractArrayRaw(Body, 'ownerReferences'))),
    `OwnerApiVersions` Array(String) MATERIALIZED arrayMap(owner -> JSONExtractString(owner, 'apiVersion'), arrayFilter(owner -> JSONExtractBool(owner, 'controller'), JSONExtractArrayRaw(Body, 'ownerReferences'))),
    `OrganizationId` LowCardinality(String) MATERIALIZED JSONExtractString(Body, 'organizationId'),
    `WorkspaceId` LowCardinality(String) MATERIALIZED JSONExtractString(Body, 'workspaceId'),
    `EnvironmentId` LowCardinality(String) MATERIALIZED JSONExtractString(Body, 'environmentId'),
    `ResourceId` LowCardinality(String) MATERIALIZED JSONExtractString(Body, 'resourceId'),
    `ResourceName` String MATERIALIZED JSONExtractString(Body, 'resourceName')
)
ENGINE = MergeTree
PARTITION BY toDate(Timestamp)
ORDER BY (ClusterUid, ObjectUid, Timestamp, ResourceVersion)
TTL toDateTime(Timestamp) + ${EVENTS_TTL} + ${EVENTS_TTL} + toIntervalSecond(1)
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 1;

