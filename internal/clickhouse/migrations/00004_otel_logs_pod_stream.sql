-- +goose NO TRANSACTION
-- +goose Up
ALTER TABLE ${DATABASE}.otel_logs
    ADD COLUMN IF NOT EXISTS `PodUid` String MATERIALIZED ResourceAttributes['k8s.pod.uid'] CODEC(ZSTD(3)) AFTER RetentionDays,
    ADD COLUMN IF NOT EXISTS `LogIOStream` LowCardinality(String) MATERIALIZED LogAttributes['log.iostream'] CODEC(ZSTD(3)) AFTER PodUid;
