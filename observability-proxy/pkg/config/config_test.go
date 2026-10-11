package config

import (
	"testing"
)

const (
	testDatabase = "loco_obs"
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CLICKHOUSE_DB", testDatabase)
}

func expectPanic(t *testing.T, name string) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Fatalf("Load did not panic for invalid %s", name)
		}
	}()
	Load()
}

func TestLoadReadsSchemaSettings(t *testing.T) {
	setRequiredEnv(t)

	cfg := Load()

	if cfg.ClickHouseDB != testDatabase {
		t.Errorf("ClickHouseDB = %q, want %q", cfg.ClickHouseDB, testDatabase)
	}
}

func TestLoadPanicsWithoutRequiredSettings(t *testing.T) {
	required := []string{
		"CLICKHOUSE_DB",
	}
	for _, name := range required {
		t.Run(name, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv(name, "")
			expectPanic(t, name)
		})
	}
}

func TestLoadPanicsOnInvalidValues(t *testing.T) {
	invalid := map[string][]string{
		"CLICKHOUSE_DB": {"loco-obs", "loco_obs; DROP", "1loco"},
		"PORT":          {"eighty"},
		"MAX_LIMIT":     {"10k"},
	}
	for name, values := range invalid {
		for _, value := range values {
			t.Run(name+"="+value, func(t *testing.T) {
				setRequiredEnv(t)
				t.Setenv(name, value)
				expectPanic(t, name)
			})
		}
	}
}
