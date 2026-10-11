package config

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
)

var (
	errMissing     = errors.New("is required")
	errInvalidInt  = errors.New("is not an integer")
	errInvalidName = errors.New("is not a ClickHouse identifier")

	identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

func stringEnv(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func requiredStringEnv(name string) string {
	value := os.Getenv(name)
	if value == "" {
		panic(fmt.Errorf("%s %w", name, errMissing))
	}
	return value
}

func identifierEnv(name string) string {
	value := requiredStringEnv(name)
	if !identifierPattern.MatchString(value) {
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

func int32Env(name string, fallback int32) int32 {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		panic(fmt.Errorf("%s %q %w", name, raw, errInvalidInt))
	}
	return int32(parsed)
}
