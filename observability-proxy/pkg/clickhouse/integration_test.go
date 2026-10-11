package clickhouse

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/observability-proxy/migrations"
)

const (
	testClickHouseEnv      = "LOCO_TEST_CLICKHOUSE_URL"
	databaseNameBytes      = 4
	testMaxConns           = 2
	testQueryTimeout       = 10
	testLogsTTL            = 72 * time.Hour
	testTracesTTL          = 36 * time.Hour
	testMetricsTTL         = 90 * time.Minute
	testDataAge            = 10 * time.Minute
	testRowSpacing         = time.Second
	testPageSize           = int32(2)
	testFullPage           = int32(100)
	testIntervalSeconds    = int32(60)
	testMetricName         = "k8s.pod.cpu.usage"
	testOtherWorkspaceID   = "99999999-8888-7777-6666-555555555555"
	testWorkerResourceID   = "ffffffff-eeee-dddd-cccc-bbbbbbbbbbbb"
	testUnknownWorkspaceID = "12121212-3434-5656-7878-909090909090"
	testSharedLabelKey     = "team"
	testSharedLabelValue   = "shared"
	workspaceAttributeKey  = "loco.workspace.id"
	resourceAttributeKey   = "loco.resource.id"
	environmentAttrKey     = "loco.environment.id"
	testEnvironmentID      = "66666666-7777-8888-9999-000000000000"
	avgAggregation         = "avg"
	logsInsertColumns      = "Timestamp, SeverityText, Body, ResourceAttributes, TraceId, SpanId, LogAttributes"
	gaugeInsertColumns     = "ResourceAttributes, MetricName, Attributes, TimeUnix, Value"
	testSeverity           = "INFO"
	testWebValue           = 1.0
	testWebSecondValue     = 3.0
	testWorkerValue        = 5.0
	testOtherWorkspaceVal  = 1000.0
	testWebAverage         = (testWebValue + testWebSecondValue) / 2
	testSecondGaugeOffset  = 10 * time.Second
	testOtherWorkspaceBody = "other workspace"
)

type logRow struct {
	workspaceID string
	resourceID  string
	body        string
}

var testLogRows = []logRow{
	{workspaceID: testWorkspaceID, resourceID: testResourceID, body: "web 1"},
	{workspaceID: testOtherWorkspaceID, resourceID: testResourceID, body: testOtherWorkspaceBody},
	{workspaceID: testWorkspaceID, resourceID: testWorkerResourceID, body: "worker 1"},
	{workspaceID: testWorkspaceID, resourceID: testResourceID, body: "web 2"},
	{workspaceID: testOtherWorkspaceID, resourceID: testWorkerResourceID, body: testOtherWorkspaceBody},
	{workspaceID: testWorkspaceID, resourceID: testWorkerResourceID, body: "worker 2"},
	{workspaceID: testWorkspaceID, resourceID: testResourceID, body: "web 3"},
}

type gaugeRow struct {
	workspaceID string
	resourceID  string
	offset      time.Duration
	value       float64
}

var testGaugeRows = []gaugeRow{
	{workspaceID: testWorkspaceID, resourceID: testResourceID, value: testWebValue},
	{
		workspaceID: testWorkspaceID,
		resourceID:  testResourceID,
		offset:      testSecondGaugeOffset,
		value:       testWebSecondValue,
	},
	{workspaceID: testWorkspaceID, resourceID: testWorkerResourceID, value: testWorkerValue},
	{workspaceID: testOtherWorkspaceID, resourceID: testResourceID, value: testOtherWorkspaceVal},
	{workspaceID: testOtherWorkspaceID, resourceID: testWorkerResourceID, value: testOtherWorkspaceVal},
}

type queryFixture struct {
	conn  driver.Conn
	start time.Time
	end   time.Time
}

func newQueryFixture(t *testing.T) *queryFixture {
	t.Helper()
	dsn := os.Getenv(testClickHouseEnv)
	if dsn == "" {
		t.Skip(testClickHouseEnv + " not set")
	}
	suffix := make([]byte, databaseNameBytes)
	if _, err := rand.Read(suffix); err != nil {
		t.Fatalf("random database name: %v", err)
	}
	database := "loco_obs_query_test_" + hex.EncodeToString(suffix)
	cfg := migrations.Config{
		DSN:        dsn,
		Database:   database,
		LogsTTL:    testLogsTTL,
		TracesTTL:  testTracesTTL,
		MetricsTTL: testMetricsTTL,
	}
	if err := migrations.Up(t.Context(), cfg); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	client, err := NewClient(dsn, database, testMaxConns)
	if err != nil {
		t.Fatalf("open client: %v", err)
	}
	t.Cleanup(func() {
		if err := client.Conn().Exec(context.Background(), "DROP DATABASE IF EXISTS "+database+" SYNC"); err != nil {
			t.Errorf("drop %s: %v", database, err)
		}
		client.Close()
	})
	start := time.Now().UTC().Truncate(time.Minute).Add(-testDataAge)
	fixture := &queryFixture{conn: client.Conn(), start: start, end: start.Add(testDataAge)}
	fixture.insertLogs(t)
	fixture.insertGauges(t)
	return fixture
}

func tenantAttributes(workspaceID, resourceID string) map[string]string {
	return map[string]string{
		"service.name":        "web",
		testSharedLabelKey:    testSharedLabelValue,
		workspaceAttributeKey: workspaceID,
		environmentAttrKey:    testEnvironmentID,
		resourceAttributeKey:  resourceID,
	}
}

func (f *queryFixture) logTime(index int) time.Time {
	offset := time.Duration(index) * testRowSpacing
	return f.start.Add(offset)
}

func (f *queryFixture) insertLogs(t *testing.T) {
	t.Helper()
	batch, err := f.conn.PrepareBatch(t.Context(), "INSERT INTO otel_logs ("+logsInsertColumns+")")
	if err != nil {
		t.Fatalf("prepare logs insert: %v", err)
	}
	for index, row := range testLogRows {
		attrs := tenantAttributes(row.workspaceID, row.resourceID)
		ts := f.logTime(index)
		if err := batch.Append(ts, testSeverity, row.body, attrs, "", "", map[string]string{}); err != nil {
			t.Fatalf("append log row: %v", err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("send log rows: %v", err)
	}
}

func (f *queryFixture) insertGauges(t *testing.T) {
	t.Helper()
	batch, err := f.conn.PrepareBatch(t.Context(), "INSERT INTO otel_metrics_gauge ("+gaugeInsertColumns+")")
	if err != nil {
		t.Fatalf("prepare gauge insert: %v", err)
	}
	for _, row := range testGaugeRows {
		attrs := tenantAttributes(row.workspaceID, row.resourceID)
		ts := f.start.Add(row.offset)
		if err := batch.Append(attrs, testMetricName, map[string]string{}, ts, row.value); err != nil {
			t.Fatalf("append gauge row: %v", err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatalf("send gauge rows: %v", err)
	}
}

type logQuery struct {
	workspaceID string
	resourceIDs []string
	labels      map[string]string
	limit       int32
	order       observabilityv1.LogOrder
}

func (f *queryFixture) page(t *testing.T, q logQuery, cursor string) ([]*observabilityv1.LogEntry, string) {
	t.Helper()
	entries, next, err := QueryLogs(
		t.Context(),
		f.conn,
		q.workspaceID,
		q.resourceIDs,
		f.start,
		f.end,
		"",
		nil,
		q.labels,
		q.limit,
		cursor,
		q.order,
		testQueryTimeout,
	)
	if err != nil {
		t.Fatalf("QueryLogs: %v", err)
	}
	return entries, next
}

func (f *queryFixture) allPages(t *testing.T, q logQuery) []string {
	t.Helper()
	var bodies []string
	cursor := ""
	for range testLogRows {
		entries, next := f.page(t, q, cursor)
		for _, entry := range entries {
			if entry.GetResourceId() == "" {
				t.Errorf("entry %q has no resource id", entry.GetBody())
			}
			bodies = append(bodies, entry.GetBody())
		}
		if next == "" {
			return bodies
		}
		cursor = next
	}
	t.Fatalf("pagination did not end after %d pages", len(testLogRows))
	return nil
}

func wantBodies(workspaceID string, resourceIDs []string, order observabilityv1.LogOrder) []string {
	var bodies []string
	for _, row := range testLogRows {
		if row.workspaceID != workspaceID {
			continue
		}
		if len(resourceIDs) > 0 && !slices.Contains(resourceIDs, row.resourceID) {
			continue
		}
		bodies = append(bodies, row.body)
	}
	if order != observabilityv1.LogOrder_LOG_ORDER_OLDEST_FIRST {
		slices.Reverse(bodies)
	}
	return bodies
}

func TestQueryLogsScopesToWorkspace(t *testing.T) {
	f := newQueryFixture(t)
	newest := observabilityv1.LogOrder_LOG_ORDER_NEWEST_FIRST
	oldest := observabilityv1.LogOrder_LOG_ORDER_OLDEST_FIRST
	workerOnly := []string{testWorkerResourceID}
	cases := []struct {
		name  string
		query logQuery
		want  []string
	}{
		{
			name:  "workspace newest first",
			query: logQuery{workspaceID: testWorkspaceID, limit: testFullPage, order: newest},
			want:  wantBodies(testWorkspaceID, nil, newest),
		},
		{
			name:  "workspace oldest first",
			query: logQuery{workspaceID: testWorkspaceID, limit: testFullPage, order: oldest},
			want:  wantBodies(testWorkspaceID, nil, oldest),
		},
		{
			name:  "resource filter",
			query: logQuery{workspaceID: testWorkspaceID, resourceIDs: workerOnly, limit: testFullPage, order: newest},
			want:  wantBodies(testWorkspaceID, workerOnly, newest),
		},
		{
			name:  "paged newest first",
			query: logQuery{workspaceID: testWorkspaceID, limit: testPageSize, order: newest},
			want:  wantBodies(testWorkspaceID, nil, newest),
		},
		{
			name:  "paged oldest first",
			query: logQuery{workspaceID: testWorkspaceID, limit: testPageSize, order: oldest},
			want:  wantBodies(testWorkspaceID, nil, oldest),
		},
		{
			name: "paged resource filter",
			query: logQuery{
				workspaceID: testWorkspaceID,
				resourceIDs: workerOnly,
				limit:       testPageSize - 1,
				order:       oldest,
			},
			want: wantBodies(testWorkspaceID, workerOnly, oldest),
		},
		{
			name:  "other workspace",
			query: logQuery{workspaceID: testOtherWorkspaceID, limit: testFullPage, order: oldest},
			want:  wantBodies(testOtherWorkspaceID, nil, oldest),
		},
		{
			name: "label shared across workspaces",
			query: logQuery{
				workspaceID: testWorkspaceID,
				labels:      map[string]string{testSharedLabelKey: testSharedLabelValue},
				limit:       testFullPage,
				order:       newest,
			},
			want: wantBodies(testWorkspaceID, nil, newest),
		},
		{
			name: "label naming another workspace",
			query: logQuery{
				workspaceID: testWorkspaceID,
				labels:      map[string]string{workspaceAttributeKey: testOtherWorkspaceID},
				limit:       testFullPage,
				order:       newest,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := f.allPages(t, tc.query)
			if !slices.Equal(got, tc.want) {
				t.Errorf("bodies = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestQueryLogsReturnsTenantResourceID(t *testing.T) {
	f := newQueryFixture(t)
	q := logQuery{
		workspaceID: testWorkspaceID,
		limit:       testFullPage,
		order:       observabilityv1.LogOrder_LOG_ORDER_OLDEST_FIRST,
	}
	entries, _ := f.page(t, q, "")
	got := make([]string, 0, len(entries))
	for _, entry := range entries {
		got = append(got, entry.GetResourceId())
	}
	var want []string
	for _, row := range testLogRows {
		if row.workspaceID == testWorkspaceID {
			want = append(want, row.resourceID)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("resource ids = %v, want %v", got, want)
	}
}

func (f *queryFixture) metrics(t *testing.T, workspaceID string, resourceIDs []string) map[string][]float64 {
	t.Helper()
	series, err := QueryMetrics(
		t.Context(),
		f.conn,
		workspaceID,
		resourceIDs,
		f.start,
		f.end,
		testMetricName,
		testIntervalSeconds,
		avgAggregation,
		testQueryTimeout,
	)
	if err != nil {
		t.Fatalf("QueryMetrics: %v", err)
	}
	values := map[string][]float64{}
	for _, s := range series {
		for _, point := range s.GetPoints() {
			if !point.GetTimestamp().AsTime().Equal(f.start) {
				t.Errorf("%s point at %s, want bucket %s", s.GetResourceId(), point.GetTimestamp().AsTime(), f.start)
			}
			values[s.GetResourceId()] = append(values[s.GetResourceId()], point.GetValue())
		}
	}
	return values
}

func TestQueryMetricsScopesToWorkspace(t *testing.T) {
	f := newQueryFixture(t)
	both := []string{testResourceID, testWorkerResourceID}
	cases := []struct {
		name        string
		workspaceID string
		resourceIDs []string
		want        map[string][]float64
	}{
		{
			name:        "both resources",
			workspaceID: testWorkspaceID,
			resourceIDs: both,
			want: map[string][]float64{
				testResourceID:       {testWebAverage},
				testWorkerResourceID: {testWorkerValue},
			},
		},
		{
			name:        "resource filter",
			workspaceID: testWorkspaceID,
			resourceIDs: []string{testWorkerResourceID},
			want:        map[string][]float64{testWorkerResourceID: {testWorkerValue}},
		},
		{
			name:        "other workspace",
			workspaceID: testOtherWorkspaceID,
			resourceIDs: both,
			want: map[string][]float64{
				testResourceID:       {testOtherWorkspaceVal},
				testWorkerResourceID: {testOtherWorkspaceVal},
			},
		},
		{
			name:        "unknown workspace",
			workspaceID: testUnknownWorkspaceID,
			resourceIDs: both,
			want:        map[string][]float64{},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := f.metrics(t, tc.workspaceID, tc.resourceIDs)
			if len(got) != len(tc.want) {
				t.Errorf("series = %v, want %v", got, tc.want)
			}
			for resourceID, want := range tc.want {
				if !slices.Equal(got[resourceID], want) {
					t.Errorf("%s = %v, want %v", resourceID, got[resourceID], want)
				}
			}
		})
	}
}
