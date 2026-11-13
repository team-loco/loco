package config

import (
	"fmt"
	"time"

	"github.com/team-loco/loco/internal/clickhouse/migrationlock"
	"github.com/team-loco/loco/internal/envconfig"
	"github.com/team-loco/loco/observability-proxy/pkg/ingest"
)

const (
	defaultRetention              = 365 * 24 * time.Hour
	defaultMigrationRetryBudget   = 2 * time.Minute
	defaultMigrationRetryInterval = 5 * time.Second
	defaultMigrationLockTTL       = 15 * time.Second
	defaultMigrationLockRetry     = 2 * time.Second

	defaultOTLPMaxRequestBytes = 4 << 20
	defaultIngestTimeout       = 5 * time.Second
	defaultIngestDialTimeout   = 10 * time.Second

	defaultIngestAuthTimeout         = 5 * time.Second
	defaultIngestJWKSRefreshInterval = 30 * time.Second
	defaultIngestPlacementCacheTTL   = 30 * time.Second
	defaultIngestPlacementRefetch    = 5 * time.Second
	defaultIngestClockSkew           = 5 * time.Minute
	defaultIngestRetryAfter          = 30 * time.Second
)

type Config struct {
	Port            int
	ControlPlaneURL string
	ClickHouseDB    string

	ClickHouseMigratorURL  string
	LogsTTL                time.Duration
	TracesTTL              time.Duration
	MetricsTTL             time.Duration
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

	IngestAuthTimeout         time.Duration
	IngestJWKSRefreshInterval time.Duration
	IngestPlacementCacheTTL   time.Duration
	IngestPlacementRefetch    time.Duration
	IngestClockSkew           time.Duration
	IngestRetryAfter          time.Duration
}

func Load() *Config {
	cfg := &Config{
		Port:            requiredIntEnv("PORT"),
		ControlPlaneURL: requiredStringEnv("CONTROL_PLANE_URL"),
		ClickHouseDB:    requiredIdentifierEnv("CLICKHOUSE_DB"),

		ClickHouseMigratorURL:  requiredStringEnv("CLICKHOUSE_MIGRATOR_URL"),
		LogsTTL:                ttlEnv("CLICKHOUSE_LOGS_TTL", defaultRetention),
		TracesTTL:              ttlEnv("CLICKHOUSE_TRACES_TTL", defaultRetention),
		MetricsTTL:             ttlEnv("CLICKHOUSE_METRICS_TTL", defaultRetention),
		MigrationRetryBudget:   envconfig.PositiveDuration("MIGRATION_RETRY_BUDGET", defaultMigrationRetryBudget),
		MigrationRetryInterval: envconfig.PositiveDuration("MIGRATION_RETRY_INTERVAL", defaultMigrationRetryInterval),
		MigrationLockTTL:       envconfig.PositiveDuration("MIGRATION_LOCK_TTL", defaultMigrationLockTTL),
		MigrationLockRetry:     envconfig.PositiveDuration("MIGRATION_LOCK_RETRY_PERIOD", defaultMigrationLockRetry),

		ClickHouseIngestURL: requiredStringEnv("CLICKHOUSE_INGEST_URL"),
		OTLPGRPCPort:        requiredIntEnv("OTLP_GRPC_PORT"),
		OTLPHTTPPort:        requiredIntEnv("OTLP_HTTP_PORT"),
		OTLPMaxRequestBytes: positiveIntEnv("OTLP_MAX_REQUEST_BYTES", defaultOTLPMaxRequestBytes),
		IngestTimeout:       envconfig.PositiveDuration("CLICKHOUSE_INGEST_TIMEOUT", defaultIngestTimeout),
		IngestDialTimeout:   envconfig.PositiveDuration("CLICKHOUSE_INGEST_DIAL_TIMEOUT", defaultIngestDialTimeout),

		IngestAuthTimeout: envconfig.PositiveDuration("INGEST_AUTH_TIMEOUT", defaultIngestAuthTimeout),
		IngestJWKSRefreshInterval: envconfig.PositiveDuration(
			"INGEST_JWKS_REFRESH_INTERVAL",
			defaultIngestJWKSRefreshInterval,
		),
		IngestPlacementCacheTTL: envconfig.PositiveDuration(
			"INGEST_PLACEMENT_CACHE_TTL", defaultIngestPlacementCacheTTL,
		),
		IngestPlacementRefetch: envconfig.PositiveDuration(
			"INGEST_PLACEMENT_REFETCH_INTERVAL",
			defaultIngestPlacementRefetch,
		),
		IngestClockSkew:  envconfig.PositiveDuration("INGEST_CLOCK_SKEW", defaultIngestClockSkew),
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
		RequestTimeout:      c.IngestAuthTimeout,
		JWKSRefreshInterval: c.IngestJWKSRefreshInterval,
		PlacementTTL:        c.IngestPlacementCacheTTL,
		RefetchInterval:     c.IngestPlacementRefetch,
		ClockSkew:           c.IngestClockSkew,
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
