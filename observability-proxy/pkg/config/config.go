package config

import (
	"errors"
	"fmt"
	"time"

	"github.com/team-loco/loco/observability-proxy/pkg/ingest"
	"github.com/team-loco/loco/observability-proxy/pkg/migrationlock"
)

const (
	defaultPort               = 8080
	defaultControlPlaneURL    = "http://localhost:8000"
	defaultClickHouseURL      = "clickhouse://localhost:9000"
	defaultLimit              = 1000
	defaultMaxLimit           = 10000
	defaultMaxTimeRangeHours  = 24
	defaultQueryTimeout       = 10
	defaultMaxConcurrent      = 5
	defaultMaxTailMinutes     = 30
	defaultMaxConcurrentTails = 3
	defaultTokenCacheSeconds  = 30

	defaultClickHouseDB           = "loco_obs"
	defaultRetention              = 365 * 24 * time.Hour
	defaultMigrationRetryBudget   = 2 * time.Minute
	defaultMigrationRetryInterval = 5 * time.Second
	defaultMigrationLockTTL       = 15 * time.Second
	defaultMigrationLockRetry     = 2 * time.Second

	defaultOTLPGRPCPort        = 4317
	defaultOTLPHTTPPort        = 4318
	defaultOTLPMaxRequestBytes = 4 << 20
	defaultIngestTimeout       = 5 * time.Second
	defaultIngestDialTimeout   = 10 * time.Second
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

	// Guardrails
	DefaultLimit       int32
	MaxLimit           int32
	MaxTimeRange       time.Duration
	QueryTimeout       int // seconds, passed to ClickHouse max_execution_time
	MaxConcurrent      int // max concurrent queries per workspace
	MaxTailDuration    time.Duration
	MaxConcurrentTails int

	// Token cache
	TokenCacheTTL time.Duration

	ClickHouseIngestURL string
	OTLPGRPCPort        int
	OTLPHTTPPort        int
	OTLPMaxRequestBytes int
	IngestTimeout       time.Duration
	IngestDialTimeout   time.Duration
}

func Load() *Config {
	cfg := &Config{
		QueryEnabled:       boolEnv("QUERY_ENABLED", true),
		IngestEnabled:      boolEnv("INGEST_ENABLED", false),
		Port:               intEnv("PORT", defaultPort),
		ControlPlaneURL:    stringEnv("CONTROL_PLANE_URL", defaultControlPlaneURL),
		CORSAllowedOrigins: originsEnv("CORS_ALLOWED_ORIGINS"),
		ClickHouseURL:      stringEnv("CLICKHOUSE_URL", defaultClickHouseURL),
		ClickHouseDB:       identifierEnv("CLICKHOUSE_DB", defaultClickHouseDB),

		LogsTTL:                ttlEnv("CLICKHOUSE_LOGS_TTL", defaultRetention),
		TracesTTL:              ttlEnv("CLICKHOUSE_TRACES_TTL", defaultRetention),
		MetricsTTL:             ttlEnv("CLICKHOUSE_METRICS_TTL", defaultRetention),
		MigrationRetryBudget:   positiveDurationEnv("MIGRATION_RETRY_BUDGET", defaultMigrationRetryBudget),
		MigrationRetryInterval: positiveDurationEnv("MIGRATION_RETRY_INTERVAL", defaultMigrationRetryInterval),
		MigrationLockTTL:       positiveDurationEnv("MIGRATION_LOCK_TTL", defaultMigrationLockTTL),
		MigrationLockRetry:     positiveDurationEnv("MIGRATION_LOCK_RETRY_PERIOD", defaultMigrationLockRetry),

		DefaultLimit:       int32Env("DEFAULT_LIMIT", defaultLimit),
		MaxLimit:           int32Env("MAX_LIMIT", defaultMaxLimit),
		MaxTimeRange:       time.Duration(intEnv("MAX_TIME_RANGE_HOURS", defaultMaxTimeRangeHours)) * time.Hour,
		QueryTimeout:       intEnv("QUERY_TIMEOUT_SECONDS", defaultQueryTimeout),
		MaxConcurrent:      intEnv("MAX_CONCURRENT_QUERIES", defaultMaxConcurrent),
		MaxTailDuration:    time.Duration(intEnv("MAX_TAIL_DURATION_MINUTES", defaultMaxTailMinutes)) * time.Minute,
		MaxConcurrentTails: intEnv("MAX_CONCURRENT_TAILS", defaultMaxConcurrentTails),

		TokenCacheTTL: time.Duration(intEnv("TOKEN_CACHE_TTL_SECONDS", defaultTokenCacheSeconds)) * time.Second,

		OTLPGRPCPort:        intEnv("OTLP_GRPC_PORT", defaultOTLPGRPCPort),
		OTLPHTTPPort:        intEnv("OTLP_HTTP_PORT", defaultOTLPHTTPPort),
		OTLPMaxRequestBytes: positiveIntEnv("OTLP_MAX_REQUEST_BYTES", defaultOTLPMaxRequestBytes),
		IngestTimeout:       positiveDurationEnv("CLICKHOUSE_INGEST_TIMEOUT", defaultIngestTimeout),
		IngestDialTimeout:   positiveDurationEnv("CLICKHOUSE_INGEST_DIAL_TIMEOUT", defaultIngestDialTimeout),
	}
	if !cfg.QueryEnabled && !cfg.IngestEnabled {
		panic(errNoRole)
	}
	if cfg.QueryEnabled {
		cfg.ClickHouseMigratorURL = requiredStringEnv("CLICKHOUSE_MIGRATOR_URL")
		if err := cfg.MigrationLock().Validate(); err != nil {
			panic(fmt.Errorf("migration lock: %w", err))
		}
	}
	if cfg.IngestEnabled {
		cfg.ClickHouseIngestURL = requiredStringEnv("CLICKHOUSE_INGEST_URL")
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

func (c *Config) Ingest(version string) ingest.Config {
	return ingest.Config{
		DSN:         c.ClickHouseIngestURL,
		Database:    c.ClickHouseDB,
		Timeout:     c.IngestTimeout,
		DialTimeout: c.IngestDialTimeout,
		Version:     version,
	}
}
