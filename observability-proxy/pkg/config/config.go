package config

import (
	"fmt"
	"time"

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
)

type Config struct {
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
}

func Load() *Config {
	cfg := &Config{
		Port:               intEnv("PORT", defaultPort),
		ControlPlaneURL:    stringEnv("CONTROL_PLANE_URL", defaultControlPlaneURL),
		CORSAllowedOrigins: originsEnv("CORS_ALLOWED_ORIGINS"),
		ClickHouseURL:      stringEnv("CLICKHOUSE_URL", defaultClickHouseURL),
		ClickHouseDB:       identifierEnv("CLICKHOUSE_DB"),

		ClickHouseMigratorURL:  requiredStringEnv("CLICKHOUSE_MIGRATOR_URL"),
		LogsTTL:                ttlEnv("CLICKHOUSE_LOGS_TTL"),
		TracesTTL:              ttlEnv("CLICKHOUSE_TRACES_TTL"),
		MetricsTTL:             ttlEnv("CLICKHOUSE_METRICS_TTL"),
		MigrationRetryBudget:   requiredPositiveDurationEnv("MIGRATION_RETRY_BUDGET"),
		MigrationRetryInterval: requiredPositiveDurationEnv("MIGRATION_RETRY_INTERVAL"),
		MigrationLockTTL:       requiredPositiveDurationEnv("MIGRATION_LOCK_TTL"),
		MigrationLockRetry:     requiredPositiveDurationEnv("MIGRATION_LOCK_RETRY_PERIOD"),

		DefaultLimit:       int32Env("DEFAULT_LIMIT", defaultLimit),
		MaxLimit:           int32Env("MAX_LIMIT", defaultMaxLimit),
		MaxTimeRange:       time.Duration(intEnv("MAX_TIME_RANGE_HOURS", defaultMaxTimeRangeHours)) * time.Hour,
		QueryTimeout:       intEnv("QUERY_TIMEOUT_SECONDS", defaultQueryTimeout),
		MaxConcurrent:      intEnv("MAX_CONCURRENT_QUERIES", defaultMaxConcurrent),
		MaxTailDuration:    time.Duration(intEnv("MAX_TAIL_DURATION_MINUTES", defaultMaxTailMinutes)) * time.Minute,
		MaxConcurrentTails: intEnv("MAX_CONCURRENT_TAILS", defaultMaxConcurrentTails),

		TokenCacheTTL: time.Duration(intEnv("TOKEN_CACHE_TTL_SECONDS", defaultTokenCacheSeconds)) * time.Second,
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
