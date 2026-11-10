package main

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/team-loco/loco/internal/envconfig"
	"github.com/team-loco/loco/internal/loglevel"
)

const listEnvSeparator = ","

var errNegative = errors.New("must not be negative")

func listEnv(name string) []string {
	raw := envconfig.String(name, "")
	values := []string{}
	if raw == "" {
		return values
	}
	values = strings.Split(raw, listEnvSeparator)
	for i := range values {
		values[i] = strings.TrimSpace(values[i])
	}
	return values
}

func positiveInt64Env(name string, fallback int64, invalid error) int64 {
	raw := envconfig.String(name, "")
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed <= 0 {
		panic(fmt.Errorf("%w: %q", invalid, raw))
	}
	return parsed
}

func positiveInt32Env(name string, fallback int32) int32 {
	value := envconfig.Int32(name, fallback)
	if value <= 0 {
		panic(fmt.Errorf("%s %d %w", name, value, envconfig.ErrNotPositive))
	}
	return value
}

func nonNegativeInt32Env(name string, fallback int32) int32 {
	value := envconfig.Int32(name, fallback)
	if value < 0 {
		panic(fmt.Errorf("%s %d %w", name, value, errNegative))
	}
	return value
}

func positiveDurationListEnv(name string, fallback []time.Duration) []time.Duration {
	raw := listEnv(name)
	if len(raw) == 0 {
		return fallback
	}
	values := make([]time.Duration, len(raw))
	for i, item := range raw {
		values[i] = envconfig.ParsePositiveDuration(name, item)
	}
	return values
}

func logLevelEnv(name string) (slog.Level, bool) {
	raw := envconfig.String(name, "")
	if raw == "" {
		return slog.Level(0), false
	}
	level, err := loglevel.Parse(raw)
	if err != nil {
		panic(fmt.Errorf("%s %w", name, err))
	}
	return level, true
}
