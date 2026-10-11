package config

import (
	"testing"
	"time"

	"github.com/team-loco/loco/observability-proxy/pkg/migrationlock"
)

const (
	testMigratorURL = "clickhouse://loco_migrator@clickhouse:9000"
	testDatabase    = "loco_obs"
	testLogsTTL     = 720 * time.Hour
	testTracesTTL   = 168 * time.Hour
	testMetricsTTL  = 2160 * time.Hour
	testBudget      = 2 * time.Minute
	testInterval    = 5 * time.Second
	testLeaseName   = "loco-obs-schema-migrations"
	testPodName     = "loco-obs-obs-proxy-abc"
	testNamespace   = "observability"
	testLease       = 15 * time.Second
	testRenew       = 10 * time.Second
	testRetry       = 2 * time.Second
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
	t.Setenv("MIGRATION_LOCK", string(MigrationLockKubernetes))
	t.Setenv("MIGRATION_LEASE_NAME", testLeaseName)
	t.Setenv("POD_NAME", testPodName)
	t.Setenv("POD_NAMESPACE", testNamespace)
	t.Setenv("MIGRATION_LEASE_DURATION", testLease.String())
	t.Setenv("MIGRATION_LEASE_RENEW_DEADLINE", testRenew.String())
	t.Setenv("MIGRATION_LEASE_RETRY_PERIOD", testRetry.String())
}

var leaseSettings = []string{
	"MIGRATION_LEASE_NAME",
	"POD_NAME",
	"POD_NAMESPACE",
	"MIGRATION_LEASE_DURATION",
	"MIGRATION_LEASE_RENEW_DEADLINE",
	"MIGRATION_LEASE_RETRY_PERIOD",
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
	required := append([]string{
		"CLICKHOUSE_MIGRATOR_URL",
		"CLICKHOUSE_DB",
		"CLICKHOUSE_LOGS_TTL",
		"CLICKHOUSE_TRACES_TTL",
		"CLICKHOUSE_METRICS_TTL",
		"MIGRATION_RETRY_BUDGET",
		"MIGRATION_RETRY_INTERVAL",
		"MIGRATION_LOCK",
	}, leaseSettings...)
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
		"CLICKHOUSE_DB":                  {"loco-obs", "loco_obs; DROP", "1loco"},
		"CLICKHOUSE_LOGS_TTL":            {"30d", "-1h", "0s", "1500ms"},
		"CLICKHOUSE_TRACES_TTL":          {"forever"},
		"CLICKHOUSE_METRICS_TTL":         {"-24h"},
		"MIGRATION_RETRY_BUDGET":         {"0s", "soon"},
		"MIGRATION_RETRY_INTERVAL":       {"-5s"},
		"PORT":                           {"eighty"},
		"MAX_LIMIT":                      {"10k"},
		"MIGRATION_LOCK":                 {"etcd", "Kubernetes", "None"},
		"MIGRATION_LEASE_NAME":           {"Loco_Lease", "-lease"},
		"POD_NAMESPACE":                  {"Observability"},
		"MIGRATION_LEASE_DURATION":       {"0s", "10s", "5s", "soon"},
		"MIGRATION_LEASE_RENEW_DEADLINE": {"-1s", "15s", "2s"},
		"MIGRATION_LEASE_RETRY_PERIOD":   {"0s", "9s"},
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

func TestLoadReadsKubernetesLeaseSettings(t *testing.T) {
	setRequiredEnv(t)

	cfg := Load()

	if cfg.MigrationLock != MigrationLockKubernetes {
		t.Errorf("MigrationLock = %q, want %q", cfg.MigrationLock, MigrationLockKubernetes)
	}
	lease := cfg.MigrationLease
	names := map[string][2]string{
		"Name":      {lease.Name, testLeaseName},
		"Namespace": {lease.Namespace, testNamespace},
		"Identity":  {lease.Identity, testPodName},
	}
	for name, pair := range names {
		if pair[0] != pair[1] {
			t.Errorf("MigrationLease.%s = %q, want %q", name, pair[0], pair[1])
		}
	}
	durations := map[string][2]time.Duration{
		"LeaseDuration": {lease.LeaseDuration, testLease},
		"RenewDeadline": {lease.RenewDeadline, testRenew},
		"RetryPeriod":   {lease.RetryPeriod, testRetry},
	}
	for name, pair := range durations {
		if pair[0] != pair[1] {
			t.Errorf("MigrationLease.%s = %s, want %s", name, pair[0], pair[1])
		}
	}
}

func TestLoadWithoutLockIgnoresLeaseSettings(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("MIGRATION_LOCK", string(MigrationLockNone))
	for _, name := range leaseSettings {
		t.Setenv(name, "")
	}

	cfg := Load()

	if cfg.MigrationLock != MigrationLockNone {
		t.Errorf("MigrationLock = %q, want %q", cfg.MigrationLock, MigrationLockNone)
	}
	if cfg.MigrationLease != (migrationlock.Config{}) {
		t.Errorf("MigrationLease = %+v, want zero without a lock", cfg.MigrationLease)
	}
}
