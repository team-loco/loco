package config

import (
	"time"
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
	Port            int
	ControlPlaneURL string
	ProxyAuthToken  string // Bearer token for calling ValidateObservabilityToken
	ClickHouseURL   string
	ClickHouseDB    string

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
	return &Config{
		Port:            intEnv("PORT", defaultPort),
		ControlPlaneURL: stringEnv("CONTROL_PLANE_URL", defaultControlPlaneURL),
		ProxyAuthToken:  stringEnv("PROXY_AUTH_TOKEN", ""),
		ClickHouseURL:   stringEnv("CLICKHOUSE_URL", defaultClickHouseURL),
		ClickHouseDB:    identifierEnv("CLICKHOUSE_DB"),

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
