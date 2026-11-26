package config

import (
	"strconv"
	"testing"
	"time"

	"github.com/team-loco/loco/observability-proxy/pkg/ingest"
)

const (
	testMigratorURL = "clickhouse://loco_migrator@clickhouse:9000"
	testDatabase    = "loco_obs_override"
	testBudget      = 2 * time.Minute
	testLockTTL     = 15 * time.Second
	testLockRetry   = 2 * time.Second
	testIngestURL   = "clickhouse://loco_ingest@clickhouse:9000"
)

const (
	databaseVar         = "CLICKHOUSE_DB"
	portVar             = "PORT"
	fractionalSeconds   = "1500ms"
	testPort            = "8080"
	testControlPlaneURL = "http://api.test"
	testGRPCPort        = 4317
	testHTTPPort        = 4318
)

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv(portVar, testPort)
	t.Setenv(databaseVar, testDatabase)
	t.Setenv("CONTROL_PLANE_URL", testControlPlaneURL)
	t.Setenv("CLICKHOUSE_MIGRATOR_URL", testMigratorURL)
	t.Setenv("CLICKHOUSE_INGEST_URL", testIngestURL)
	t.Setenv("OTLP_GRPC_PORT", strconv.Itoa(testGRPCPort))
	t.Setenv("OTLP_HTTP_PORT", strconv.Itoa(testHTTPPort))
}

func setOverrides(t *testing.T) {
	t.Helper()
	t.Setenv("MIGRATION_RETRY_BUDGET", testBudget.String())
	t.Setenv("MIGRATION_LOCK_TTL", testLockTTL.String())
	t.Setenv("MIGRATION_LOCK_RETRY_PERIOD", testLockRetry.String())
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

func TestLoadDefaultsOptionalSettings(t *testing.T) {
	setRequiredEnv(t)

	cfg := Load()

	durations := map[string][2]time.Duration{
		"MigrationRetryBudget": {cfg.MigrationRetryBudget, defaultMigrationRetryBudget},
		"MigrationLockTTL":     {cfg.MigrationLockTTL, defaultMigrationLockTTL},
		"MigrationLockRetry":   {cfg.MigrationLockRetry, defaultMigrationLockRetry},
	}
	for name, pair := range durations {
		if pair[0] != pair[1] {
			t.Errorf("%s = %s, want %s", name, pair[0], pair[1])
		}
	}
}

func TestLoadReadsSchemaOverrides(t *testing.T) {
	setRequiredEnv(t)
	setOverrides(t)

	cfg := Load()

	if cfg.ClickHouseMigratorURL != testMigratorURL {
		t.Errorf("ClickHouseMigratorURL = %q, want %q", cfg.ClickHouseMigratorURL, testMigratorURL)
	}
	if cfg.ClickHouseDB != testDatabase {
		t.Errorf("ClickHouseDB = %q, want %q", cfg.ClickHouseDB, testDatabase)
	}
	durations := map[string][2]time.Duration{
		"MigrationRetryBudget": {cfg.MigrationRetryBudget, testBudget},
		"MigrationLockTTL":     {cfg.MigrationLockTTL, testLockTTL},
		"MigrationLockRetry":   {cfg.MigrationLockRetry, testLockRetry},
	}
	for name, pair := range durations {
		if pair[0] != pair[1] {
			t.Errorf("%s = %s, want %s", name, pair[0], pair[1])
		}
	}
}

func TestLoadPanicsWithoutRequiredSettings(t *testing.T) {
	required := []string{
		portVar,
		databaseVar,
		"CONTROL_PLANE_URL",
		"CLICKHOUSE_MIGRATOR_URL",
		"CLICKHOUSE_INGEST_URL",
		"OTLP_GRPC_PORT",
		"OTLP_HTTP_PORT",
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
		databaseVar:                         {"loco-obs", "loco_obs; DROP", "1loco"},
		"MIGRATION_RETRY_BUDGET":            {"0s", "soon"},
		portVar:                             {"eighty"},
		"OTLP_HTTP_PORT":                    {"http"},
		"MIGRATION_LOCK_TTL":                {"0s", "3s", "soon"},
		"MIGRATION_LOCK_RETRY_PERIOD":       {"-1s", "8s"},
		"OTLP_GRPC_PORT":                    {"grpc"},
		"OTLP_MAX_REQUEST_BYTES":            {"0", "-1"},
		"INGEST_JWKS_REFRESH_INTERVAL":      {"-1s"},
		"INGEST_PLACEMENT_CACHE_TTL":        {"-30s"},
		"INGEST_RETENTION_CACHE_TTL":        {"0s"},
		"INGEST_PLACEMENT_REFETCH_INTERVAL": {"0s"},
		"INGEST_CLOCK_SKEW":                 {"-5m"},
		"INGEST_MAX_RECORD_LATENESS":        {"0s"},
		"INGEST_RETRY_AFTER":                {fractionalSeconds, "0s"},
		"SYSTEM_RETENTION_DAYS":             {"0", "-1", "month"},
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

func TestLoadBuildsTheMigrationLockFromTheMigratorSettings(t *testing.T) {
	setRequiredEnv(t)
	setOverrides(t)

	lock := Load().MigrationLock()

	if lock.DSN != testMigratorURL || lock.Database != testDatabase {
		t.Errorf(
			"MigrationLock() DSN, Database = %q, %q, want %q, %q",
			lock.DSN,
			lock.Database,
			testMigratorURL,
			testDatabase,
		)
	}
	if lock.TTL != testLockTTL || lock.RetryPeriod != testLockRetry {
		t.Errorf(
			"MigrationLock() TTL, RetryPeriod = %s, %s, want %s, %s",
			lock.TTL,
			lock.RetryPeriod,
			testLockTTL,
			testLockRetry,
		)
	}
}

func TestLoadReadsIngestAuthSettings(t *testing.T) {
	setRequiredEnv(t)
	cfg := Load()
	gate := cfg.IngestGate()
	wantGate := ingest.GateConfig{
		ControlPlaneURL:     testControlPlaneURL,
		RequestTimeout:      ingestAuthTimeout,
		JWKSRefreshInterval: defaultIngestJWKSRefreshInterval,
		PlacementTTL:        defaultIngestPlacementCacheTTL,
		RetentionTTL:        defaultIngestRetentionCacheTTL,
		RefetchInterval:     defaultIngestPlacementRefetch,
		ClockSkew:           defaultIngestClockSkew,
		MaxRecordLateness:   defaultIngestMaxRecordLateness,
		SystemRetentionDays: defaultSystemRetentionDays,
	}
	if gate != wantGate {
		t.Fatalf("IngestGate() = %+v, want %+v", gate, wantGate)
	}
	t.Setenv("INGEST_CLOCK_SKEW", "2m")
	t.Setenv("INGEST_MAX_RECORD_LATENESS", "3h")
	t.Setenv("INGEST_RETRY_AFTER", "10s")
	t.Setenv("SYSTEM_INGEST_TOKEN", "system-token")
	t.Setenv("SYSTEM_RETENTION_DAYS", "14")
	overridden := Load()
	wantGate.ClockSkew = 2 * time.Minute
	wantGate.MaxRecordLateness = 3 * time.Hour
	wantGate.SystemToken = "system-token"
	wantGate.SystemRetentionDays = 14
	if overridden.IngestGate() != wantGate {
		t.Fatal("IngestGate() does not carry the overridden settings")
	}
	if got := overridden.IngestService().RetryAfter; got != 10*time.Second {
		t.Fatalf("RetryAfter = %s, want 10s", got)
	}
}

func TestLoadReadsIngestSettings(t *testing.T) {
	setRequiredEnv(t)
	cfg := Load()
	writer := cfg.Ingest("test")
	if writer.DSN != testIngestURL || writer.Database != testDatabase {
		t.Fatalf("Ingest() DSN, Database = %q, %q", writer.DSN, writer.Database)
	}
	if cfg.Port != 8080 || cfg.OTLPGRPCPort != testGRPCPort || cfg.OTLPHTTPPort != testHTTPPort {
		t.Fatalf("ports = %d, %d, %d", cfg.Port, cfg.OTLPGRPCPort, cfg.OTLPHTTPPort)
	}
}

func TestTracingEnabledOnlyWithExporterEndpoint(t *testing.T) {
	for _, endpoint := range []string{"", "OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"} {
		t.Run(endpoint, func(t *testing.T) {
			setRequiredEnv(t)
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
			t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "")
			if endpoint != "" {
				t.Setenv(endpoint, "http://collector:4318")
			}
			if got := Load().TracingEnabled; got != (endpoint != "") {
				t.Fatalf("tracing enabled = %v for %q", got, endpoint)
			}
		})
	}
}
