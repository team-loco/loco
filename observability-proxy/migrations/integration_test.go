package migrations

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/pressly/goose/v3"
)

const (
	testClickHouseEnv  = "LOCO_TEST_CLICKHOUSE_URL"
	databaseNameBytes  = 4
	concurrentRunners  = 2
	testLogsTTL        = 72 * time.Hour
	testTracesTTL      = 36 * time.Hour
	testMetricsTTL     = 90 * time.Minute
	testWorkspaceID    = "11111111-2222-3333-4444-555555555555"
	testEnvironmentID  = "66666666-7777-8888-9999-000000000000"
	testResourceID     = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	testTraceID        = "0af7651916cd43dd8448eb211c80319c"
	testSpanID         = "b7ad6b7169203331"
	testDroppedAttrs   = uint32(0)
	testFlags          = uint32(1)
	testTemporality    = int32(2)
	testCount          = uint64(4)
	testValue          = 1.5
	testScale          = int32(3)
	testOffset         = int32(1)
	testSeverityNumber = uint8(9)
	testTraceFlags     = uint8(1)
	testSpanDuration   = uint64(1500)
)

const (
	logsTable                = "otel_logs"
	tracesTable              = "otel_traces"
	traceLookupTable         = "otel_traces_trace_id_ts"
	gaugeTable               = "otel_metrics_gauge"
	sumTable                 = "otel_metrics_sum"
	histogramTable           = "otel_metrics_histogram"
	expHistogramTable        = "otel_metrics_exponential_histogram"
	summaryTable             = "otel_metrics_summary"
	temporalityColumn        = "AggregationTemporality"
	countColumn              = "Count"
	flagsColumn              = "Flags"
	resourceAttributesColumn = "ResourceAttributes"
	scopeNameColumn          = "ScopeName"
	scopeVersionColumn       = "ScopeVersion"
	serviceNameColumn        = "ServiceName"
	testScopeVersion         = "1.0"
	testServiceName          = "web"
	metricsTTLExpr           = "TTL toDateTime(TimeUnix) + toIntervalMinute(90)"
	sumColumn                = "Sum"
	testScopeName            = "scope"
)

var expectedTables = []string{
	VersionTable,
	logsTable,
	expHistogramTable,
	gaugeTable,
	histogramTable,
	sumTable,
	summaryTable,
	tracesTable,
	traceLookupTable,
	"otel_traces_trace_id_ts_mv",
}

type testDatabase struct {
	admin driver.Conn
	cfg   Config
}

func newTestDatabase(t *testing.T) *testDatabase {
	t.Helper()
	dsn := os.Getenv(testClickHouseEnv)
	if dsn == "" {
		t.Skip(testClickHouseEnv + " not set")
	}
	opts, err := clickhouse.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse %s: %v", testClickHouseEnv, err)
	}
	admin, err := clickhouse.Open(opts)
	if err != nil {
		t.Fatalf("open clickhouse: %v", err)
	}
	suffix := make([]byte, databaseNameBytes)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random database name: %v", err)
	}
	database := "loco_obs_test_" + hex.EncodeToString(suffix)
	t.Cleanup(func() {
		if err := admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+database+" SYNC"); err != nil {
			t.Errorf("drop %s: %v", database, err)
		}
		if err := admin.Close(); err != nil {
			t.Errorf("close clickhouse: %v", err)
		}
	})
	return &testDatabase{
		admin: admin,
		cfg: Config{
			DSN:        dsn,
			Database:   database,
			LogsTTL:    testLogsTTL,
			TracesTTL:  testTracesTTL,
			MetricsTTL: testMetricsTTL,
		},
	}
}

func (d *testDatabase) tables(t *testing.T) []string {
	t.Helper()
	query := "SELECT name FROM system.tables WHERE database = ? ORDER BY name"
	rows, err := d.admin.Query(t.Context(), query, d.cfg.Database)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table: %v", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list tables: %v", err)
	}
	return names
}

func (d *testDatabase) appliedVersions(t *testing.T) []int64 {
	t.Helper()
	query := "SELECT DISTINCT version_id FROM " + d.cfg.Database + "." + VersionTable + " ORDER BY version_id"
	rows, err := d.admin.Query(t.Context(), query)
	if err != nil {
		t.Fatalf("list applied versions: %v", err)
	}
	defer rows.Close()
	var versions []int64
	for rows.Next() {
		var version int64
		if err := rows.Scan(&version); err != nil {
			t.Fatalf("scan version: %v", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list applied versions: %v", err)
	}
	return versions
}

func (d *testDatabase) metadataTimes(t *testing.T) map[string]time.Time {
	t.Helper()
	rows, err := d.admin.Query(
		t.Context(),
		"SELECT name, metadata_modification_time FROM system.tables WHERE database = ?",
		d.cfg.Database,
	)
	if err != nil {
		t.Fatalf("list table metadata: %v", err)
	}
	defer rows.Close()
	times := map[string]time.Time{}
	for rows.Next() {
		var name string
		var modified time.Time
		if err := rows.Scan(&name, &modified); err != nil {
			t.Fatalf("scan table metadata: %v", err)
		}
		times[name] = modified
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list table metadata: %v", err)
	}
	return times
}

func (d *testDatabase) versionRows(t *testing.T) uint64 {
	t.Helper()
	var count uint64
	query := "SELECT count() FROM " + d.cfg.Database + "." + VersionTable
	if err := d.admin.QueryRow(t.Context(), query).Scan(&count); err != nil {
		t.Fatalf("count version rows: %v", err)
	}
	return count
}

func wantVersions(t *testing.T) []int64 {
	t.Helper()
	versions, err := sourceVersions()
	if err != nil {
		t.Fatalf("source versions: %v", err)
	}
	return append([]int64{0}, versions...)
}

func (d *testDatabase) assertLatestSchema(t *testing.T) {
	t.Helper()
	if got := d.tables(t); !slices.Equal(got, expectedTables) {
		t.Errorf("tables = %v, want %v", got, expectedTables)
	}
	if got, want := d.appliedVersions(t), wantVersions(t); !slices.Equal(got, want) {
		t.Errorf("applied versions = %v, want %v", got, want)
	}
}

func TestUpCreatesLatestSchemaInEmptyDatabase(t *testing.T) {
	db := newTestDatabase(t)
	if err := Up(t.Context(), db.cfg); err != nil {
		t.Fatalf("Up: %v", err)
	}
	db.assertLatestSchema(t)
}

func TestSecondUpIsNoop(t *testing.T) {
	db := newTestDatabase(t)
	if err := Up(t.Context(), db.cfg); err != nil {
		t.Fatalf("first Up: %v", err)
	}
	rowsBefore := db.versionRows(t)
	timesBefore := db.metadataTimes(t)

	applied, err := up(t.Context(), db.cfg)
	if err != nil {
		t.Fatalf("second Up: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("second Up applied %d migrations, want none", len(applied))
	}
	if rowsAfter := db.versionRows(t); rowsAfter != rowsBefore {
		t.Errorf("version rows = %d after second Up, want %d", rowsAfter, rowsBefore)
	}
	for name, after := range db.metadataTimes(t) {
		if before := timesBefore[name]; !after.Equal(before) {
			t.Errorf("%s metadata changed from %s to %s on second Up", name, before, after)
		}
	}
	db.assertLatestSchema(t)
}

func TestConcurrentUpConverges(t *testing.T) {
	db := newTestDatabase(t)
	errs := make([]error, concurrentRunners)
	var wg sync.WaitGroup
	for runner := range concurrentRunners {
		wg.Go(func() {
			errs[runner] = Up(t.Context(), db.cfg)
		})
	}
	wg.Wait()
	for runner, err := range errs {
		if err != nil {
			t.Errorf("runner %d: Up: %v", runner, err)
		}
	}
	db.assertLatestSchema(t)

	applied, err := up(t.Context(), db.cfg)
	if err != nil {
		t.Fatalf("Up after concurrent runs: %v", err)
	}
	if len(applied) != 0 {
		t.Errorf("Up after concurrent runs applied %d migrations, want none", len(applied))
	}
}

func TestTTLsFromConfigLandOnTables(t *testing.T) {
	db := newTestDatabase(t)
	if err := Up(t.Context(), db.cfg); err != nil {
		t.Fatalf("Up: %v", err)
	}
	want := map[string]string{
		logsTable:         "TTL toDateTime(Timestamp) + toIntervalDay(3)",
		tracesTable:       "TTL toDateTime(Timestamp) + toIntervalHour(36)",
		traceLookupTable:  "TTL toDateTime(Start) + toIntervalHour(36)",
		gaugeTable:        metricsTTLExpr,
		sumTable:          metricsTTLExpr,
		histogramTable:    metricsTTLExpr,
		expHistogramTable: metricsTTLExpr,
		summaryTable:      metricsTTLExpr,
	}
	for table, ttl := range want {
		var engine string
		err := db.admin.QueryRow(
			t.Context(),
			"SELECT engine_full FROM system.tables WHERE database = ? AND name = ?",
			db.cfg.Database,
			table,
		).Scan(&engine)
		if err != nil {
			t.Fatalf("read %s engine: %v", table, err)
		}
		if !strings.Contains(engine, ttl) {
			t.Errorf("%s engine %q does not contain %q", table, engine, ttl)
		}
		if !strings.Contains(engine, "ttl_only_drop_parts = 1") {
			t.Errorf("%s engine %q does not drop whole parts on expiry", table, engine)
		}
	}
}

func tenantResource() map[string]string {
	return map[string]string{
		"service.name":        testServiceName,
		"loco.workspace.id":   testWorkspaceID,
		"loco.environment.id": testEnvironmentID,
		"loco.resource.id":    testResourceID,
	}
}

type exporterInsert struct {
	table   string
	columns []string
	row     func(now time.Time) []any
}

func exporterInserts() []exporterInsert {
	attrs := map[string]string{"http.route": "/"}
	exemplarColumns := []string{
		"Exemplars.FilteredAttributes",
		"Exemplars.TimeUnix",
		"Exemplars.Value",
		"Exemplars.SpanId",
		"Exemplars.TraceId",
	}
	metricColumns := []string{
		resourceAttributesColumn,
		"ResourceSchemaUrl",
		scopeNameColumn,
		scopeVersionColumn,
		"ScopeAttributes",
		"ScopeDroppedAttrCount",
		"ScopeSchemaUrl",
		serviceNameColumn,
		"MetricName",
		"MetricDescription",
		"MetricUnit",
		"Attributes",
		"StartTimeUnix",
		"TimeUnix",
	}
	metricRow := func(now time.Time) []any {
		return []any{
			tenantResource(), "", testScopeName, testScopeVersion, attrs, testDroppedAttrs, "", testServiceName,
			"http.server.requests", "requests", "1", attrs, now, now,
		}
	}
	exemplars := func(now time.Time) []any {
		return []any{
			[]map[string]string{
				attrs,
			},
			[]time.Time{now},
			[]float64{testValue},
			[]string{testSpanID},
			[]string{testTraceID},
		}
	}
	return []exporterInsert{
		{
			table: logsTable,
			columns: []string{
				"Timestamp",
				"TraceId",
				"SpanId",
				"TraceFlags",
				"SeverityText",
				"SeverityNumber",
				serviceNameColumn,
				"Body",
				"ResourceSchemaUrl",
				resourceAttributesColumn,
				"ScopeSchemaUrl",
				scopeNameColumn,
				scopeVersionColumn,
				"ScopeAttributes",
				"LogAttributes",
				"EventName",
			},
			row: func(now time.Time) []any {
				return []any{
					now, testTraceID, testSpanID, testTraceFlags, "INFO", testSeverityNumber, testServiceName, "hello",
					"", tenantResource(), "", testScopeName, testScopeVersion, attrs, attrs, "",
				}
			},
		},
		{
			table: tracesTable,
			columns: []string{
				"Timestamp",
				"TraceId",
				"SpanId",
				"ParentSpanId",
				"TraceState",
				"SpanName",
				"SpanKind",
				serviceNameColumn,
				resourceAttributesColumn,
				scopeNameColumn,
				scopeVersionColumn,
				"SpanAttributes",
				"Duration",
				"StatusCode",
				"StatusMessage",
				"Events.Timestamp",
				"Events.Name",
				"Events.Attributes",
				"Links.TraceId",
				"Links.SpanId",
				"Links.TraceState",
				"Links.Attributes",
			},
			row: func(now time.Time) []any {
				return []any{
					now,
					testTraceID,
					testSpanID,
					"",
					"",
					"GET /",
					"Server",
					testServiceName,
					tenantResource(),
					testScopeName,
					testScopeVersion,
					attrs,
					testSpanDuration,
					"Ok",
					"",
					[]time.Time{now},
					[]string{"event"},
					[]map[string]string{attrs},
					[]string{testTraceID},
					[]string{testSpanID},
					[]string{""},
					[]map[string]string{attrs},
				}
			},
		},
		{
			table:   gaugeTable,
			columns: slices.Concat(metricColumns, []string{"Value", flagsColumn}, exemplarColumns),
			row: func(now time.Time) []any {
				return slices.Concat(metricRow(now), []any{testValue, testFlags}, exemplars(now))
			},
		},
		{
			table: sumTable,
			columns: slices.Concat(
				metricColumns,
				[]string{"Value", flagsColumn},
				exemplarColumns,
				[]string{temporalityColumn, "IsMonotonic"},
			),
			row: func(now time.Time) []any {
				return slices.Concat(
					metricRow(now), []any{testValue, testFlags}, exemplars(now), []any{testTemporality, true},
				)
			},
		},
		{
			table: histogramTable,
			columns: slices.Concat(
				metricColumns,
				[]string{countColumn, sumColumn, "BucketCounts", "ExplicitBounds"},
				exemplarColumns,
				[]string{flagsColumn, "Min", "Max", temporalityColumn},
			),
			row: func(now time.Time) []any {
				return slices.Concat(
					metricRow(now),
					[]any{testCount, testValue, []uint64{testCount}, []float64{testValue}},
					exemplars(now),
					[]any{testFlags, testValue, testValue, testTemporality},
				)
			},
		},
		{
			table: expHistogramTable,
			columns: slices.Concat(
				metricColumns,
				[]string{
					countColumn,
					sumColumn,
					"Scale",
					"ZeroCount",
					"PositiveOffset",
					"PositiveBucketCounts",
					"NegativeOffset",
					"NegativeBucketCounts",
				},
				exemplarColumns,
				[]string{flagsColumn, "Min", "Max", temporalityColumn},
			),
			row: func(now time.Time) []any {
				return slices.Concat(
					metricRow(now),
					[]any{
						testCount, testValue, testScale, testCount, testOffset, []uint64{testCount}, testOffset,
						[]uint64{testCount},
					},
					exemplars(now),
					[]any{testFlags, testValue, testValue, testTemporality},
				)
			},
		},
		{
			table: summaryTable,
			columns: slices.Concat(
				metricColumns,
				[]string{countColumn, sumColumn, "ValueAtQuantiles.Quantile", "ValueAtQuantiles.Value", flagsColumn},
			),
			row: func(now time.Time) []any {
				return slices.Concat(
					metricRow(now),
					[]any{testCount, testValue, []float64{testValue}, []float64{testValue}, testFlags},
				)
			},
		},
	}
}

func TestExporterShapedRowsInsertAndCarryTenancy(t *testing.T) {
	db := newTestDatabase(t)
	if err := Up(t.Context(), db.cfg); err != nil {
		t.Fatalf("Up: %v", err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for _, insert := range exporterInserts() {
		t.Run(insert.table, func(t *testing.T) {
			query := fmt.Sprintf(
				"INSERT INTO %s.%s (%s)",
				db.cfg.Database,
				insert.table,
				strings.Join(insert.columns, ", "),
			)
			batch, err := db.admin.PrepareBatch(t.Context(), query)
			if err != nil {
				t.Fatalf("prepare insert: %v", err)
			}
			row := insert.row(now)
			if err := batch.Append(row...); err != nil {
				t.Fatalf("append row: %v", err)
			}
			if err := batch.Send(); err != nil {
				t.Fatalf("send row: %v", err)
			}
			db.assertTenancy(t, insert.table)
		})
	}
	t.Run(traceLookupTable, func(t *testing.T) {
		var workspaceID, traceID string
		var start, end time.Time
		err := db.admin.QueryRow(
			t.Context(),
			"SELECT WorkspaceId, TraceId, Start, End FROM "+db.cfg.Database+".otel_traces_trace_id_ts",
		).Scan(&workspaceID, &traceID, &start, &end)
		if err != nil {
			t.Fatalf("read trace lookup: %v", err)
		}
		if workspaceID != testWorkspaceID || traceID != testTraceID {
			t.Errorf("trace lookup = (%q, %q), want (%q, %q)", workspaceID, traceID, testWorkspaceID, testTraceID)
		}
		if !start.Equal(now) || !end.Equal(now) {
			t.Errorf("trace lookup span = %s..%s, want %s", start, end, now)
		}
	})
}

func (d *testDatabase) assertTenancy(t *testing.T, table string) {
	t.Helper()
	var workspaceID, environmentID, resourceID string
	err := d.admin.QueryRow(
		t.Context(),
		"SELECT WorkspaceId, EnvironmentId, ResourceId FROM "+d.cfg.Database+"."+table,
	).Scan(&workspaceID, &environmentID, &resourceID)
	if err != nil {
		t.Fatalf("read tenancy columns: %v", err)
	}
	got := []string{workspaceID, environmentID, resourceID}
	want := []string{testWorkspaceID, testEnvironmentID, testResourceID}
	if !slices.Equal(got, want) {
		t.Errorf("tenancy columns = %v, want %v", got, want)
	}
}

func TestOrderingKeysLeadWithWorkspace(t *testing.T) {
	db := newTestDatabase(t)
	if err := Up(t.Context(), db.cfg); err != nil {
		t.Fatalf("Up: %v", err)
	}
	rows, err := db.admin.Query(
		t.Context(),
		"SELECT name, sorting_key FROM system.tables "+
			"WHERE database = ? AND name LIKE 'otel\\_%' AND engine LIKE '%MergeTree'",
		db.cfg.Database,
	)
	if err != nil {
		t.Fatalf("list sorting keys: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name, key string
		if err := rows.Scan(&name, &key); err != nil {
			t.Fatalf("scan sorting key: %v", err)
		}
		if !strings.HasPrefix(key, "WorkspaceId, ") {
			t.Errorf("%s sorting key %q does not lead with WorkspaceId", name, key)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("list sorting keys: %v", err)
	}
}

func sourceVersions() ([]int64, error) {
	names, err := fs.Glob(files, "*.sql")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	versions := make([]int64, 0, len(names))
	for _, name := range names {
		version, err := goose.NumericComponent(name)
		if err != nil {
			return nil, fmt.Errorf("migration %s: %w", name, err)
		}
		versions = append(versions, version)
	}
	return versions, nil
}

func TestInsertOnlyUserPopulatesTraceLookup(t *testing.T) {
	db := newTestDatabase(t)
	if err := Up(t.Context(), db.cfg); err != nil {
		t.Fatalf("Up: %v", err)
	}
	user := db.cfg.Database + "_ingest"
	password := hex.EncodeToString([]byte(user))
	statements := []string{
		"CREATE USER " + user + " IDENTIFIED WITH plaintext_password BY '" + password + "'",
		"GRANT INSERT ON " + db.cfg.Database + ".* TO " + user,
	}
	for _, statement := range statements {
		if err := db.admin.Exec(t.Context(), statement); err != nil {
			t.Fatalf("%s: %v", statement, err)
		}
	}
	t.Cleanup(func() {
		if err := db.admin.Exec(context.Background(), "DROP USER IF EXISTS "+user); err != nil {
			t.Errorf("drop %s: %v", user, err)
		}
	})
	opts, err := clickhouse.ParseDSN(db.cfg.DSN)
	if err != nil {
		t.Fatalf("parse DSN: %v", err)
	}
	opts.Auth.Username = user
	opts.Auth.Password = password
	opts.Auth.Database = db.cfg.Database
	ingest, err := clickhouse.Open(opts)
	if err != nil {
		t.Fatalf("open as %s: %v", user, err)
	}
	t.Cleanup(func() {
		if err := ingest.Close(); err != nil {
			t.Errorf("close %s connection: %v", user, err)
		}
	})

	now := time.Now().UTC().Truncate(time.Second)
	for _, insert := range exporterInserts() {
		query := fmt.Sprintf("INSERT INTO %s (%s)", insert.table, strings.Join(insert.columns, ", "))
		batch, err := ingest.PrepareBatch(t.Context(), query)
		if err != nil {
			t.Fatalf("prepare %s insert as %s: %v", insert.table, user, err)
		}
		if err := batch.Append(insert.row(now)...); err != nil {
			t.Fatalf("append %s row: %v", insert.table, err)
		}
		if err := batch.Send(); err != nil {
			t.Fatalf("insert into %s as %s: %v", insert.table, user, err)
		}
	}
	var lookups uint64
	query := "SELECT count() FROM " + db.cfg.Database + ".otel_traces_trace_id_ts WHERE WorkspaceId = ?"
	if err := db.admin.QueryRow(t.Context(), query, testWorkspaceID).Scan(&lookups); err != nil {
		t.Fatalf("count trace lookups: %v", err)
	}
	if lookups != 1 {
		t.Errorf("trace lookups = %d, want 1", lookups)
	}
}
