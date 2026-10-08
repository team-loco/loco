package main

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

const listEnvSeparator = ","

var (
	errInvalidInt32    = errors.New("is not a 32-bit integer")
	errInvalidDuration = errors.New("is not a duration")
	errNotPositive     = errors.New("must be positive")
	errNegative        = errors.New("must not be negative")
)

func stringEnv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func listEnv(name string) []string {
	raw := stringEnv(name, "")
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

func boolEnv(name string, fallback bool, invalid error) bool {
	raw := stringEnv(name, "")
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		panic(fmt.Errorf("%w: %q", invalid, raw))
	}
	return parsed
}

func positiveInt64Env(name string, fallback int64, invalid error) int64 {
	raw := stringEnv(name, "")
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || parsed <= 0 {
		panic(fmt.Errorf("%w: %q", invalid, raw))
	}
	return parsed
}

func int32Env(name string, fallback int32) int32 {
	raw := stringEnv(name, "")
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		panic(fmt.Errorf("%s %q %w", name, raw, errInvalidInt32))
	}
	return int32(parsed)
}

func positiveInt32Env(name string, fallback int32) int32 {
	value := int32Env(name, fallback)
	if value <= 0 {
		panic(fmt.Errorf("%s %d %w", name, value, errNotPositive))
	}
	return value
}

func nonNegativeInt32Env(name string, fallback int32) int32 {
	value := int32Env(name, fallback)
	if value < 0 {
		panic(fmt.Errorf("%s %d %w", name, value, errNegative))
	}
	return value
}

func positiveDurationEnv(name string, fallback time.Duration) time.Duration {
	raw := stringEnv(name, "")
	if raw == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		panic(fmt.Errorf("%s %q %w", name, raw, errInvalidDuration))
	}
	if parsed <= 0 {
		panic(fmt.Errorf("%s %q %w", name, raw, errNotPositive))
	}
	return parsed
}

func logLevelEnv(name string) (slog.Level, bool) {
	raw := stringEnv(name, "")
	if raw == "" {
		return slog.Level(0), false
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		return slog.Level(0), false
	}
	return slog.Level(parsed), true
}
