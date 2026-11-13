package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/team-loco/loco/internal/clickhouse/migrations"
	"github.com/team-loco/loco/internal/envconfig"
)

var (
	errMissing     = errors.New("is required")
	errInvalidInt  = errors.New("is not an integer")
	errFractional  = errors.New("must be a whole number of seconds")
	errInvalidName = errors.New("is not a ClickHouse identifier")
)

func requiredStringEnv(name string) string {
	value := os.Getenv(name)
	if value == "" {
		panic(fmt.Errorf("%s %w", name, errMissing))
	}
	return value
}

func requiredIdentifierEnv(name string) string {
	value := requiredStringEnv(name)
	if !migrations.IsIdentifier(value) {
		panic(fmt.Errorf("%s %q %w", name, value, errInvalidName))
	}
	return value
}

func intEnv(name string, fallback int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		panic(fmt.Errorf("%s %q %w", name, raw, errInvalidInt))
	}
	return parsed
}

func requiredIntEnv(name string) int {
	raw := requiredStringEnv(name)
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		panic(fmt.Errorf("%s %q %w", name, raw, errInvalidInt))
	}
	return parsed
}

func positiveIntEnv(name string, fallback int) int {
	value := intEnv(name, fallback)
	if value <= 0 {
		panic(fmt.Errorf("%s %d %w", name, value, envconfig.ErrNotPositive))
	}
	return value
}

func ttlEnv(name string, fallback time.Duration) time.Duration {
	ttl := envconfig.PositiveDuration(name, fallback)
	if ttl%time.Second != 0 {
		panic(fmt.Errorf("%s %q %w", name, ttl, errFractional))
	}
	return ttl
}
