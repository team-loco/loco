package config

import (
	"fmt"
	"time"

	"github.com/team-loco/loco/internal/clickhouse/migrationlock"
	"github.com/team-loco/loco/internal/envconfig"
	"github.com/team-loco/loco/observability-proxy/pkg/ingest"
)

const (
	migrationRetryInterval = 5 * time.Second
	ingestTimeout          = 5 * time.Second
	ingestDialTimeout      = 10 * time.Second
	ingestAuthTimeout      = 5 * time.Second
)

const (
	defaultMigrationRetryBudget = 2 * time.Minute
	defaultMigrationLockTTL     = 15 * time.Second
	defaultMigrationLockRetry   = 2 * time.Second

	defaultOTLPMaxRequestBytes = 4 << 20

	defaultIngestJWKSRefreshInterval = 30 * time.Second
	defaultIngestPlacementCacheTTL   = 30 * time.Second
	defaultIngestRetentionCacheTTL   = 30 * time.Second
	defaultIngestPlacementRefetch    = 5 * time.Second
	defaultIngestClockSkew           = 5 * time.Minute
	defaultIngestMaxRecordLateness   = time.Hour
	defaultIngestRetryAfter          = 30 * time.Second
)

type Config struct {
	Port            int
	ControlPlaneURL string
	ClickHouseDB    string

	ClickHouseMigratorURL  string
	MigrationRetryBudget   time.Duration
	MigrationRetryInterval time.Duration
	MigrationLockTTL       time.Duration
	MigrationLockRetry     time.Duration

	ClickHouseIngestURL string
	OTLPGRPCPort        int
	OTLPHTTPPort        int
	OTLPMaxRequestBytes int
	IngestTimeout       time.Duration
	IngestDialTimeout   time.Duration

	IngestJWKSRefreshInterval time.Duration
	IngestPlacementCacheTTL   time.Duration
	IngestRetentionCacheTTL   time.Duration
	IngestPlacementRefetch    time.Duration
	IngestClockSkew           time.Duration
	IngestMaxRecordLateness   time.Duration
	IngestRetryAfter          time.Duration
}

func Load() *Config {
	cfg := &Config{
		Port:            requiredIntEnv("PORT"),
		ControlPlaneURL: requiredStringEnv("CONTROL_PLANE_URL"),
		ClickHouseDB:    requiredIdentifierEnv("CLICKHOUSE_DB"),

		ClickHouseMigratorURL:  requiredStringEnv("CLICKHOUSE_MIGRATOR_URL"),
		MigrationRetryBudget:   envconfig.PositiveDuration("MIGRATION_RETRY_BUDGET", defaultMigrationRetryBudget),
		MigrationRetryInterval: migrationRetryInterval,
		MigrationLockTTL:       envconfig.PositiveDuration("MIGRATION_LOCK_TTL", defaultMigrationLockTTL),
		MigrationLockRetry:     envconfig.PositiveDuration("MIGRATION_LOCK_RETRY_PERIOD", defaultMigrationLockRetry),

		ClickHouseIngestURL: requiredStringEnv("CLICKHOUSE_INGEST_URL"),
		OTLPGRPCPort:        requiredIntEnv("OTLP_GRPC_PORT"),
		OTLPHTTPPort:        requiredIntEnv("OTLP_HTTP_PORT"),
		OTLPMaxRequestBytes: positiveIntEnv("OTLP_MAX_REQUEST_BYTES", defaultOTLPMaxRequestBytes),
		IngestTimeout:       ingestTimeout,
		IngestDialTimeout:   ingestDialTimeout,

		IngestJWKSRefreshInterval: envconfig.PositiveDuration(
			"INGEST_JWKS_REFRESH_INTERVAL",
			defaultIngestJWKSRefreshInterval,
		),
		IngestPlacementCacheTTL: envconfig.PositiveDuration(
			"INGEST_PLACEMENT_CACHE_TTL", defaultIngestPlacementCacheTTL,
		),
		IngestRetentionCacheTTL: envconfig.PositiveDuration(
			"INGEST_RETENTION_CACHE_TTL", defaultIngestRetentionCacheTTL,
		),
		IngestPlacementRefetch: envconfig.PositiveDuration(
			"INGEST_PLACEMENT_REFETCH_INTERVAL",
			defaultIngestPlacementRefetch,
		),
		IngestClockSkew: envconfig.PositiveDuration("INGEST_CLOCK_SKEW", defaultIngestClockSkew),
		IngestMaxRecordLateness: envconfig.PositiveDuration(
			"INGEST_MAX_RECORD_LATENESS",
			defaultIngestMaxRecordLateness,
		),
		IngestRetryAfter: ttlEnv("INGEST_RETRY_AFTER", defaultIngestRetryAfter),
	}
	if err := cfg.MigrationLock().Validate(); err != nil {
		panic(fmt.Errorf("migration lock: %w", err))
	}
	return cfg
}

func (c *Config) MigrationLock() migrationlock.Config {
	return migrationlock.Config{
		DSN:         c.ClickHouseMigratorURL,
		Database:    c.ClickHouseDB,
		TTL:         c.MigrationLockTTL,
		RetryPeriod: c.MigrationLockRetry,
	}
}

func (c *Config) IngestGate() ingest.GateConfig {
	return ingest.GateConfig{
		ControlPlaneURL:     c.ControlPlaneURL,
		RequestTimeout:      ingestAuthTimeout,
		JWKSRefreshInterval: c.IngestJWKSRefreshInterval,
		PlacementTTL:        c.IngestPlacementCacheTTL,
		RetentionTTL:        c.IngestRetentionCacheTTL,
		RefetchInterval:     c.IngestPlacementRefetch,
		ClockSkew:           c.IngestClockSkew,
		MaxRecordLateness:   c.IngestMaxRecordLateness,
	}
}

func (c *Config) IngestService() ingest.ServiceConfig {
	return ingest.ServiceConfig{MaxRequestBytes: c.OTLPMaxRequestBytes, RetryAfter: c.IngestRetryAfter}
}

func (c *Config) Ingest(version string) ingest.Config {
	return ingest.Config{
		DSN:         c.ClickHouseIngestURL,
		Database:    c.ClickHouseDB,
		Timeout:     c.IngestTimeout,
		DialTimeout: c.IngestDialTimeout,
		Version:     version,
	}
}
