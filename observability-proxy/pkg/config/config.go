package config

import (
	"os"
	"strconv"
	"time"
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
		Port:            getEnvInt("PORT", 8080),
		ControlPlaneURL: getEnvOrDefault("CONTROL_PLANE_URL", "http://localhost:8000"),
		ProxyAuthToken:  os.Getenv("PROXY_AUTH_TOKEN"),
		ClickHouseURL:   getEnvOrDefault("CLICKHOUSE_URL", "clickhouse://localhost:9000"),
		ClickHouseDB:    getEnvOrDefault("CLICKHOUSE_DB", "default"),

		DefaultLimit:       int32(getEnvInt("DEFAULT_LIMIT", 1000)),
		MaxLimit:           int32(getEnvInt("MAX_LIMIT", 10000)),
		MaxTimeRange:       time.Duration(getEnvInt("MAX_TIME_RANGE_HOURS", 24)) * time.Hour,
		QueryTimeout:       getEnvInt("QUERY_TIMEOUT_SECONDS", 10),
		MaxConcurrent:      getEnvInt("MAX_CONCURRENT_QUERIES", 5),
		MaxTailDuration:    time.Duration(getEnvInt("MAX_TAIL_DURATION_MINUTES", 30)) * time.Minute,
		MaxConcurrentTails: getEnvInt("MAX_CONCURRENT_TAILS", 3),

		TokenCacheTTL: time.Duration(getEnvInt("TOKEN_CACHE_TTL_SECONDS", 30)) * time.Second,
	}
}

func getEnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
