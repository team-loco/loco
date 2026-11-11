-- +goose NO TRANSACTION
-- +goose Up
CREATE TABLE IF NOT EXISTS ${DATABASE}.otel_logs
(
    `Timestamp` DateTime64(9) CODEC(Delta(8), ZSTD(3)),
    `TraceId` String CODEC(ZSTD(3)),
    `SpanId` String CODEC(ZSTD(3)),
    `TraceFlags` UInt8,
    `SeverityText` LowCardinality(String) CODEC(ZSTD(3)),
    `SeverityNumber` UInt8,
    `ServiceName` LowCardinality(String) CODEC(ZSTD(3)),
    `Body` String CODEC(ZSTD(3)),
    `ResourceSchemaUrl` LowCardinality(String) CODEC(ZSTD(3)),
    `ResourceAttributes` Map(LowCardinality(String), String) CODEC(ZSTD(3)),
    `ScopeSchemaUrl` LowCardinality(String) CODEC(ZSTD(3)),
    `ScopeName` String CODEC(ZSTD(3)),
    `ScopeVersion` LowCardinality(String) CODEC(ZSTD(3)),
    `ScopeAttributes` Map(LowCardinality(String), String) CODEC(ZSTD(3)),
    `LogAttributes` Map(LowCardinality(String), String) CODEC(ZSTD(3)),
    `EventName` String CODEC(ZSTD(3)),
    `RetentionSeconds` UInt64 MATERIALIZED toUInt64(if(mapContains(ResourceAttributes, 'loco.log.retention.seconds'), ResourceAttributes['loco.log.retention.seconds'], '${LOGS_TTL_SECONDS}')) CODEC(ZSTD(3)),
    `OrganizationId` LowCardinality(String) MATERIALIZED ResourceAttributes['loco.organization.id'] CODEC(ZSTD(3)),
    `WorkspaceId` LowCardinality(String) MATERIALIZED ResourceAttributes['loco.workspace.id'] CODEC(ZSTD(3)),
    `EnvironmentId` LowCardinality(String) MATERIALIZED ResourceAttributes['loco.environment.id'] CODEC(ZSTD(3)),
    `ResourceId` LowCardinality(String) MATERIALIZED ResourceAttributes['loco.resource.id'] CODEC(ZSTD(3)),
    `ResourceAttributeItems` Array(String) ALIAS arrayMap((arr) -> concat(arr.1, '=', arr.2), ResourceAttributes::Array(Tuple(String, String))),
    `ScopeAttributeItems` Array(String) ALIAS arrayMap((arr) -> concat(arr.1, '=', arr.2), ScopeAttributes::Array(Tuple(String, String))),
    `LogAttributeItems` Array(String) ALIAS arrayMap((arr) -> concat(arr.1, '=', arr.2), LogAttributes::Array(Tuple(String, String))),
    INDEX idx_organization_id OrganizationId TYPE text(tokenizer = 'array'),
    INDEX idx_trace_id TraceId TYPE text(tokenizer = 'array'),
    INDEX idx_res_attr_key mapKeys(ResourceAttributes) TYPE text(tokenizer = 'array'),
    INDEX idx_res_attr_items ResourceAttributeItems TYPE text(tokenizer = 'array'),
    INDEX idx_scope_attr_key mapKeys(ScopeAttributes) TYPE text(tokenizer = 'array'),
    INDEX idx_scope_attr_items ScopeAttributeItems TYPE text(tokenizer = 'array'),
    INDEX idx_log_attr_key mapKeys(LogAttributes) TYPE text(tokenizer = 'array'),
    INDEX idx_log_attr_items LogAttributeItems TYPE text(tokenizer = 'array'),
    INDEX idx_lower_body lower(Body) TYPE text(tokenizer = 'splitByNonAlpha')
)
ENGINE = MergeTree
PARTITION BY toDate(Timestamp)
ORDER BY (WorkspaceId, ResourceId, toStartOfFiveMinutes(Timestamp), Timestamp)
TTL toDateTime(Timestamp) + toIntervalSecond(RetentionSeconds)
SETTINGS index_granularity = 8192, ttl_only_drop_parts = 0, enable_block_number_column = 1, enable_block_offset_column = 1;
