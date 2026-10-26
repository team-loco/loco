package config

import (
	"strconv"
	"testing"
	"time"
)

const (
	testMigratorURL = "clickhouse://loco_migrator@clickhouse:9000"
	testDatabase    = "loco_obs_override"
	testLogsTTL     = 720 * time.Hour
	testTracesTTL   = 168 * time.Hour
	testMetricsTTL  = 2160 * time.Hour
	testBudget      = 2 * time.Minute
	testInterval    = 5 * time.Second
	testLockTTL     = 15 * time.Second
	testLockRetry   = 2 * time.Second
	testIngestURL   = "clickhouse://loco_ingest@clickhouse:9000"
)

const (
	databaseVar         = "CLICKHOUSE_DB"
	portVar             = "PORT"
	testPort            = "8080"
	testControlPlaneURL = "http://api.test"
	testQueryURL        = "clickhouse://loco_reader@clickhouse:9000"
	testGRPCPort        = 4317
	testHTTPPort        = 4318
)

func setCommonEnv(t *testing.T) {
	t.Helper()
	t.Setenv(portVar, testPort)
	t.Setenv(databaseVar, testDatabase)
}

func setQueryEnv(t *testing.T) {
	t.Helper()
	setCommonEnv(t)
	t.Setenv("CONTROL_PLANE_URL", testControlPlaneURL)
	t.Setenv("CLICKHOUSE_URL", testQueryURL)
	t.Setenv("CLICKHOUSE_MIGRATOR_URL", testMigratorURL)
}

func setIngestEnv(t *testing.T) {
	t.Helper()
	setCommonEnv(t)
	t.Setenv("QUERY_ENABLED", "false")
	t.Setenv("INGEST_ENABLED", "true")
	t.Setenv("CLICKHOUSE_INGEST_URL", testIngestURL)
	t.Setenv("OTLP_GRPC_PORT", strconv.Itoa(testGRPCPort))
	t.Setenv("OTLP_HTTP_PORT", strconv.Itoa(testHTTPPort))
}

func setRequiredEnv(t *testing.T) {
	t.Helper()
	setQueryEnv(t)
}

func setBothRolesEnv(t *testing.T) {
	t.Helper()
	setIngestEnv(t)
	setQueryEnv(t)
	t.Setenv("QUERY_ENABLED", "true")
}

func setOverrides(t *testing.T) {
	t.Helper()
	t.Setenv("CLICKHOUSE_LOGS_TTL", testLogsTTL.String())
	t.Setenv("CLICKHOUSE_TRACES_TTL", testTracesTTL.String())
	t.Setenv("CLICKHOUSE_METRICS_TTL", testMetricsTTL.String())
	t.Setenv("MIGRATION_RETRY_BUDGET", testBudget.String())
	t.Setenv("MIGRATION_RETRY_INTERVAL", testInterval.String())
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
		"LogsTTL":                {cfg.LogsTTL, defaultRetention},
		"TracesTTL":              {cfg.TracesTTL, defaultRetention},
		"MetricsTTL":             {cfg.MetricsTTL, defaultRetention},
		"MigrationRetryBudget":   {cfg.MigrationRetryBudget, defaultMigrationRetryBudget},
		"MigrationRetryInterval": {cfg.MigrationRetryInterval, defaultMigrationRetryInterval},
		"MigrationLockTTL":       {cfg.MigrationLockTTL, defaultMigrationLockTTL},
		"MigrationLockRetry":     {cfg.MigrationLockRetry, defaultMigrationLockRetry},
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
		"LogsTTL":                {cfg.LogsTTL, testLogsTTL},
		"TracesTTL":              {cfg.TracesTTL, testTracesTTL},
		"MetricsTTL":             {cfg.MetricsTTL, testMetricsTTL},
		"MigrationRetryBudget":   {cfg.MigrationRetryBudget, testBudget},
		"MigrationRetryInterval": {cfg.MigrationRetryInterval, testInterval},
		"MigrationLockTTL":       {cfg.MigrationLockTTL, testLockTTL},
		"MigrationLockRetry":     {cfg.MigrationLockRetry, testLockRetry},
	}
	for name, pair := range durations {
		if pair[0] != pair[1] {
			t.Errorf("%s = %s, want %s", name, pair[0], pair[1])
		}
	}
}

func TestLoadPanicsWithoutRequiredSettings(t *testing.T) {
	roles := map[string]struct {
		set      func(*testing.T)
		required []string
	}{
		"query": {
			set:      setQueryEnv,
			required: []string{portVar, databaseVar, "CONTROL_PLANE_URL", "CLICKHOUSE_URL", "CLICKHOUSE_MIGRATOR_URL"},
		},
		"ingest": {
			set: setIngestEnv,
			required: []string{
				portVar, databaseVar, "CLICKHOUSE_INGEST_URL", "OTLP_GRPC_PORT", "OTLP_HTTP_PORT",
			},
		},
	}
	for role, spec := range roles {
		for _, name := range spec.required {
			t.Run(role+" "+name, func(t *testing.T) {
				spec.set(t)
				t.Setenv(name, "")
				expectPanic(t, name)
			})
		}
	}
}

func TestLoadPanicsOnInvalidValues(t *testing.T) {
	invalid := map[string][]string{
		databaseVar:                      {"loco-obs", "loco_obs; DROP", "1loco"},
		"CLICKHOUSE_LOGS_TTL":            {"30d", "-1h", "0s", "1500ms"},
		"CLICKHOUSE_TRACES_TTL":          {"forever"},
		"CLICKHOUSE_METRICS_TTL":         {"-24h"},
		"MIGRATION_RETRY_BUDGET":         {"0s", "soon"},
		"MIGRATION_RETRY_INTERVAL":       {"-5s"},
		portVar:                          {"eighty"},
		"OTLP_HTTP_PORT":                 {"http"},
		"MAX_LIMIT":                      {"10k"},
		"MIGRATION_LOCK_TTL":             {"0s", "3s", "soon"},
		"MIGRATION_LOCK_RETRY_PERIOD":    {"-1s", "8s"},
		"QUERY_ENABLED":                  {"maybe"},
		"INGEST_ENABLED":                 {"yes please"},
		"OTLP_GRPC_PORT":                 {"grpc"},
		"OTLP_MAX_REQUEST_BYTES":         {"0", "-1"},
		"CLICKHOUSE_INGEST_TIMEOUT":      {"0s"},
		"CLICKHOUSE_INGEST_DIAL_TIMEOUT": {"later"},
	}
	for name, values := range invalid {
		for _, value := range values {
			t.Run(name+"="+value, func(t *testing.T) {
				setBothRolesEnv(t)
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

func TestCORSOrigins(t *testing.T) {
	setRequiredEnv(t)
	t.Setenv("CORS_ALLOWED_ORIGINS", " https://app.example.test, http://localhost:5173, ")
	cfg := Load()
	if len(cfg.CORSAllowedOrigins) != 2 || cfg.CORSAllowedOrigins[0] != "https://app.example.test" ||
		cfg.CORSAllowedOrigins[1] != "http://localhost:5173" {
		t.Fatalf("origins = %v", cfg.CORSAllowedOrigins)
	}
	invalidOrigins := []string{
		"*", "https://app.example.test/path", "ftp://app.example.test", "https://user:password@app.example.test",
	}
	for _, origin := range invalidOrigins {
		t.Run(origin, func(t *testing.T) {
			t.Setenv("CORS_ALLOWED_ORIGINS", origin)
			defer func() {
				if recover() == nil {
					t.Fatal("invalid origin accepted")
				}
			}()
			Load()
		})
	}
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	if origins := Load().CORSAllowedOrigins; len(origins) != 0 {
		t.Fatalf("empty allowlist = %v", origins)
	}
}

func TestLoadRoles(t *testing.T) {
	t.Run("query only by default", func(t *testing.T) {
		setRequiredEnv(t)
		cfg := Load()
		if !cfg.QueryEnabled || cfg.IngestEnabled {
			t.Fatalf("QueryEnabled, IngestEnabled = %t, %t, want true, false", cfg.QueryEnabled, cfg.IngestEnabled)
		}
	})
	t.Run("ingest only needs no migrator", func(t *testing.T) {
		setIngestEnv(t)
		cfg := Load()
		if cfg.ClickHouseMigratorURL != "" {
			t.Fatalf("ClickHouseMigratorURL = %q, want none", cfg.ClickHouseMigratorURL)
		}
		ingest := cfg.Ingest("test")
		if ingest.DSN != testIngestURL || ingest.Database != testDatabase {
			t.Fatalf("Ingest() DSN, Database = %q, %q", ingest.DSN, ingest.Database)
		}
		if cfg.OTLPGRPCPort != testGRPCPort || cfg.OTLPHTTPPort != testHTTPPort {
			t.Fatalf("OTLP ports = %d, %d", cfg.OTLPGRPCPort, cfg.OTLPHTTPPort)
		}
	})
	t.Run("query only needs no ingest settings", func(t *testing.T) {
		setQueryEnv(t)
		cfg := Load()
		if cfg.ClickHouseURL != testQueryURL || cfg.Port != 8080 {
			t.Fatalf("ClickHouseURL, Port = %q, %d", cfg.ClickHouseURL, cfg.Port)
		}
		if cfg.ClickHouseIngestURL != "" || cfg.OTLPGRPCPort != 0 || cfg.OTLPHTTPPort != 0 {
			t.Fatalf("ingest settings read for the query role")
		}
	})
	t.Run("both roles need both role settings", func(t *testing.T) {
		setQueryEnv(t)
		t.Setenv("INGEST_ENABLED", "true")
		expectPanic(t, "CLICKHOUSE_INGEST_URL")
	})
	t.Run("a role is required", func(t *testing.T) {
		setRequiredEnv(t)
		t.Setenv("QUERY_ENABLED", "false")
		expectPanic(t, "QUERY_ENABLED")
	})
}
