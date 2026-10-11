package config

import (
	"testing"
	"time"
)

const (
	testMigratorURL = "clickhouse://loco_migrator@clickhouse:9000"
	testDatabase    = "loco_obs"
	testLogsTTL     = 720 * time.Hour
	testTracesTTL   = 168 * time.Hour
	testMetricsTTL  = 2160 * time.Hour
	testBudget      = 2 * time.Minute
	testInterval    = 5 * time.Second
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("CLICKHOUSE_MIGRATOR_URL", testMigratorURL)
	t.Setenv("CLICKHOUSE_DB", testDatabase)
	t.Setenv("CLICKHOUSE_LOGS_TTL", testLogsTTL.String())
	t.Setenv("CLICKHOUSE_TRACES_TTL", testTracesTTL.String())
	t.Setenv("CLICKHOUSE_METRICS_TTL", testMetricsTTL.String())
	t.Setenv("MIGRATION_RETRY_BUDGET", testBudget.String())
	t.Setenv("MIGRATION_RETRY_INTERVAL", testInterval.String())
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

	if cfg.ClickHouseMigratorURL != testMigratorURL {
		t.Errorf("ClickHouseMigratorURL = %q, want %q", cfg.ClickHouseMigratorURL, testMigratorURL)
	}
	if cfg.ClickHouseDB != testDatabase {
		t.Errorf("ClickHouseDB = %q, want %q", cfg.ClickHouseDB, testDatabase)
	}
	durations := map[string][2]time.Duration{
		"LogsTTL":                {cfg.LogsTTL, testLogsTTL},
		"TracesTTL":              {cfg.TracesTTL, testTracesTTL},
		"MetricsTTL":             {cfg.MetricsTTL, testMetricsTTL},
		"MigrationRetryBudget":   {cfg.MigrationRetryBudget, testBudget},
		"MigrationRetryInterval": {cfg.MigrationRetryInterval, testInterval},
	}
	for name, pair := range durations {
		if pair[0] != pair[1] {
			t.Errorf("%s = %s, want %s", name, pair[0], pair[1])
		}
	}
}

func TestLoadPanicsWithoutRequiredSettings(t *testing.T) {
	required := []string{
		"CLICKHOUSE_MIGRATOR_URL",
		"CLICKHOUSE_DB",
		"CLICKHOUSE_LOGS_TTL",
		"CLICKHOUSE_TRACES_TTL",
		"CLICKHOUSE_METRICS_TTL",
		"MIGRATION_RETRY_BUDGET",
		"MIGRATION_RETRY_INTERVAL",
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
		"CLICKHOUSE_DB":            {"loco-obs", "loco_obs; DROP", "1loco"},
		"CLICKHOUSE_LOGS_TTL":      {"30d", "-1h", "0s", "1500ms"},
		"CLICKHOUSE_TRACES_TTL":    {"forever"},
		"CLICKHOUSE_METRICS_TTL":   {"-24h"},
		"MIGRATION_RETRY_BUDGET":   {"0s", "soon"},
		"MIGRATION_RETRY_INTERVAL": {"-5s"},
		"PORT":                     {"eighty"},
		"MAX_LIMIT":                {"10k"},
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
