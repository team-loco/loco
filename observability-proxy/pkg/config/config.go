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

type MigrationLock string

const (
	MigrationLockKubernetes MigrationLock = "kubernetes"
	MigrationLockNone       MigrationLock = "none"
)

type Config struct {
	Port            int
	ControlPlaneURL string
	ProxyAuthToken  string // Bearer token for calling ValidateObservabilityToken
	ClickHouseURL   string
	ClickHouseDB    string

	ClickHouseMigratorURL  string
	LogsTTL                time.Duration
	TracesTTL              time.Duration
	MetricsTTL             time.Duration
	MigrationRetryBudget   time.Duration
	MigrationRetryInterval time.Duration
	MigrationLock          MigrationLock
	MigrationLease         migrationlock.Config

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
	lock := MigrationLock(enumEnv("MIGRATION_LOCK", string(MigrationLockKubernetes), string(MigrationLockNone)))
	var lease migrationlock.Config
	if lock == MigrationLockKubernetes {
		lease = leaseConfig()
	}
	return &Config{
		Port:            intEnv("PORT", defaultPort),
		ControlPlaneURL: stringEnv("CONTROL_PLANE_URL", defaultControlPlaneURL),
		ProxyAuthToken:  stringEnv("PROXY_AUTH_TOKEN", ""),
		ClickHouseURL:   stringEnv("CLICKHOUSE_URL", defaultClickHouseURL),
		ClickHouseDB:    identifierEnv("CLICKHOUSE_DB"),

		ClickHouseMigratorURL:  requiredStringEnv("CLICKHOUSE_MIGRATOR_URL"),
		LogsTTL:                ttlEnv("CLICKHOUSE_LOGS_TTL"),
		TracesTTL:              ttlEnv("CLICKHOUSE_TRACES_TTL"),
		MetricsTTL:             ttlEnv("CLICKHOUSE_METRICS_TTL"),
		MigrationRetryBudget:   requiredPositiveDurationEnv("MIGRATION_RETRY_BUDGET"),
		MigrationRetryInterval: requiredPositiveDurationEnv("MIGRATION_RETRY_INTERVAL"),
		MigrationLock:          lock,
		MigrationLease:         lease,

		DefaultLimit:       int32Env("DEFAULT_LIMIT", defaultLimit),
		MaxLimit:           int32Env("MAX_LIMIT", defaultMaxLimit),
		MaxTimeRange:       time.Duration(intEnv("MAX_TIME_RANGE_HOURS", defaultMaxTimeRangeHours)) * time.Hour,
		QueryTimeout:       intEnv("QUERY_TIMEOUT_SECONDS", defaultQueryTimeout),
		MaxConcurrent:      intEnv("MAX_CONCURRENT_QUERIES", defaultMaxConcurrent),
		MaxTailDuration:    time.Duration(intEnv("MAX_TAIL_DURATION_MINUTES", defaultMaxTailMinutes)) * time.Minute,
		MaxConcurrentTails: intEnv("MAX_CONCURRENT_TAILS", defaultMaxConcurrentTails),

		TokenCacheTTL: time.Duration(intEnv("TOKEN_CACHE_TTL_SECONDS", defaultTokenCacheSeconds)) * time.Second,
	}
}

func leaseConfig() migrationlock.Config {
	lease := migrationlock.Config{
		Name:          requiredStringEnv("MIGRATION_LEASE_NAME"),
		Namespace:     requiredStringEnv("POD_NAMESPACE"),
		Identity:      requiredStringEnv("POD_NAME"),
		LeaseDuration: requiredPositiveDurationEnv("MIGRATION_LEASE_DURATION"),
		RenewDeadline: requiredPositiveDurationEnv("MIGRATION_LEASE_RENEW_DEADLINE"),
		RetryPeriod:   requiredPositiveDurationEnv("MIGRATION_LEASE_RETRY_PERIOD"),
	}
	if err := lease.Validate(); err != nil {
		panic(fmt.Errorf("migration lease: %w", err))
	}
	return lease
}
