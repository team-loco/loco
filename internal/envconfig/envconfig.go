// Package envconfig reads typed values from environment variables for the
// services' startup configuration. An unset or empty variable yields the
// fallback; an invalid value panics so that bad configuration stops startup.
package envconfig

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"
)

var (
	ErrInvalidBool     = errors.New("is not a boolean")
	ErrInvalidInt32    = errors.New("is not a 32-bit integer")
	ErrInvalidDuration = errors.New("is not a duration")
	ErrNotPositive     = errors.New("must be positive")
)

func String(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func Bool(name string, fallback bool) bool {
	raw := String(name, "")
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		panic(fmt.Errorf("%s %q %w", name, raw, ErrInvalidBool))
	}
	return parsed
}

func Int32(name string, fallback int32) int32 {
	raw := String(name, "")
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		panic(fmt.Errorf("%s %q %w", name, raw, ErrInvalidInt32))
	}
	return int32(parsed)
}

func PositiveDuration(name string, fallback time.Duration) time.Duration {
	raw := String(name, "")
	if raw == "" {
		return fallback
	}
	return ParsePositiveDuration(name, raw)
}

// ParsePositiveDuration parses one raw value of the variable name, for readers
// that split a variable into several durations.
func ParsePositiveDuration(name, raw string) time.Duration {
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		panic(fmt.Errorf("%s %q %w", name, raw, ErrInvalidDuration))
	}
	if parsed <= 0 {
		panic(fmt.Errorf("%s %q %w", name, raw, ErrNotPositive))
	}
	return parsed
}
