package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/team-loco/loco/internal/envconfig"
	"github.com/team-loco/loco/observability-proxy/pkg/ingest"
	"github.com/team-loco/loco/observability-proxy/pkg/migrationlock"
)

const (
	defaultLimit              = 1000
	defaultMaxLimit           = 10000
	defaultMaxConcurrent      = 5
	defaultMaxConcurrentTails = 3

	defaultMaxTimeRange           = 24 * time.Hour
	defaultQueryTimeout           = 10 * time.Second
	defaultMaxTailDuration        = 30 * time.Minute
	defaultTokenCacheTTL          = 30 * time.Second
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

var errNoRole = errors.New("QUERY_ENABLED or INGEST_ENABLED must be true")

type Config struct {
	QueryEnabled       bool
	IngestEnabled      bool
	Port               int
	ControlPlaneURL    string
	CORSAllowedOrigins []string
	ClickHouseURL      string
	ClickHouseDB       string

	ClickHouseMigratorURL  string
	LogsTTL                time.Duration
	TracesTTL              time.Duration
	MetricsTTL             time.Duration
	MigrationRetryBudget   time.Duration
	MigrationRetryInterval time.Duration
	MigrationLockTTL       time.Duration
	MigrationLockRetry     time.Duration

	DefaultLimit       int32
	MaxLimit           int32
	MaxTimeRange       time.Duration
	QueryTimeout       time.Duration
	MaxConcurrent      int
	MaxTailDuration    time.Duration
	MaxConcurrentTails int

	TokenCacheTTL time.Duration

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
		QueryEnabled:       envconfig.Bool("QUERY_ENABLED", true),
		IngestEnabled:      envconfig.Bool("INGEST_ENABLED", false),
		Port:               requiredIntEnv("PORT"),
		CORSAllowedOrigins: originsEnv("CORS_ALLOWED_ORIGINS"),
		ClickHouseDB:       requiredIdentifierEnv("CLICKHOUSE_DB"),

		LogsTTL:                ttlEnv("CLICKHOUSE_LOGS_TTL", defaultRetention),
		TracesTTL:              ttlEnv("CLICKHOUSE_TRACES_TTL", defaultRetention),
		MetricsTTL:             ttlEnv("CLICKHOUSE_METRICS_TTL", defaultRetention),
		MigrationRetryBudget:   envconfig.PositiveDuration("MIGRATION_RETRY_BUDGET", defaultMigrationRetryBudget),
		MigrationRetryInterval: envconfig.PositiveDuration("MIGRATION_RETRY_INTERVAL", defaultMigrationRetryInterval),
		MigrationLockTTL:       envconfig.PositiveDuration("MIGRATION_LOCK_TTL", defaultMigrationLockTTL),
		MigrationLockRetry:     envconfig.PositiveDuration("MIGRATION_LOCK_RETRY_PERIOD", defaultMigrationLockRetry),

		DefaultLimit:       envconfig.Int32("DEFAULT_LIMIT", defaultLimit),
		MaxLimit:           envconfig.Int32("MAX_LIMIT", defaultMaxLimit),
		MaxTimeRange:       envconfig.PositiveDuration("MAX_TIME_RANGE", defaultMaxTimeRange),
		QueryTimeout:       ttlEnv("QUERY_TIMEOUT", defaultQueryTimeout),
		MaxConcurrent:      intEnv("MAX_CONCURRENT_QUERIES", defaultMaxConcurrent),
		MaxTailDuration:    envconfig.PositiveDuration("MAX_TAIL_DURATION", defaultMaxTailDuration),
		MaxConcurrentTails: intEnv("MAX_CONCURRENT_TAILS", defaultMaxConcurrentTails),

		TokenCacheTTL: envconfig.PositiveDuration("TOKEN_CACHE_TTL", defaultTokenCacheTTL),

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
	if !cfg.QueryEnabled && !cfg.IngestEnabled {
		panic(errNoRole)
	}
	cfg.ControlPlaneURL = requiredStringEnv("CONTROL_PLANE_URL")
	if cfg.QueryEnabled {
		cfg.ClickHouseURL = requiredStringEnv("CLICKHOUSE_URL")
		cfg.ClickHouseMigratorURL = requiredStringEnv("CLICKHOUSE_MIGRATOR_URL")
		if err := cfg.MigrationLock().Validate(); err != nil {
			panic(fmt.Errorf("migration lock: %w", err))
		}
	}
	if cfg.IngestEnabled {
		cfg.ClickHouseIngestURL = requiredStringEnv("CLICKHOUSE_INGEST_URL")
		cfg.OTLPGRPCPort = requiredIntEnv("OTLP_GRPC_PORT")
		cfg.OTLPHTTPPort = requiredIntEnv("OTLP_HTTP_PORT")
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
