package ingest

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/plog/plogotlp"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/pmetric/pmetricotlp"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"go.uber.org/zap/zaptest"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/observability-proxy/migrations"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
)

const (
	testClickHouseEnv    = "LOCO_TEST_CLICKHOUSE_URL"
	databaseNameBytes    = 4
	testTTL              = 24 * time.Hour
	testIngestTimeout    = 5 * time.Second
	testDialTimeout      = 10 * time.Second
	testWaitBudget       = 300 * time.Millisecond
	testWaitInterval     = 50 * time.Millisecond
	testQueryTimeout     = 10
	testQueryLimit       = int32(10)
	testMaxConns         = 2
	testIntervalSeconds  = int32(60)
	testWindow           = 10 * time.Minute
	testWorkspaceID      = "11111111-2222-3333-4444-555555555555"
	testEnvironmentID    = "66666666-7777-8888-9999-000000000000"
	testResourceID       = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	testOrganizationID   = "99999999-8888-7777-6666-555555555555"
	testRegion           = "us-east-1"
	testServiceName      = "web"
	testEventName        = "deploy.started"
	testIngestedLogBody  = "ingested over otlp http"
	testSpanName         = "GET /"
	testMetricName       = "http.server.active_requests"
	testMetricValue      = 7.5
	testSpanDuration     = 1500 * time.Microsecond
	ingestGrants         = "INSERT, SHOW COLUMNS"
	avgAggregation       = "avg"
	sumAggregation       = "sum"
	regionAttribute      = "loco.region"
	serviceNameAttribute = "service.name"
)

var (
	testTraceID = pcommon.TraceID{
		0x0a,
		0xf7,
		0x65,
		0x19,
		0x16,
		0xcd,
		0x43,
		0xdd,
		0x84,
		0x48,
		0xeb,
		0x21,
		0x1c,
		0x80,
		0x31,
		0x9c,
	}
	testSpanID = pcommon.SpanID{0xb7, 0xad, 0x6b, 0x71, 0x69, 0x20, 0x33, 0x31}
)

type ingestFixture struct {
	admin     driver.Conn
	adminDSN  string
	ingestDSN string
	database  string
}

func newIngestFixture(t *testing.T) *ingestFixture {
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
	if _, err = rand.Read(suffix); err != nil {
		t.Fatalf("random database name: %v", err)
	}
	database := "loco_obs_ingest_test_" + hex.EncodeToString(suffix)
	user := database + "_writer"
	password := hex.EncodeToString(suffix)
	statements := []string{
		"CREATE USER " + user + " IDENTIFIED WITH plaintext_password BY '" + password + "'",
		"GRANT " + ingestGrants + " ON " + database + ".* TO " + user,
	}
	for _, statement := range statements {
		if execErr := admin.Exec(t.Context(), statement); execErr != nil {
			t.Fatalf("%s: %v", statement, execErr)
		}
	}
	t.Cleanup(func() {
		ctx := context.Background()
		if dropErr := admin.Exec(ctx, "DROP DATABASE IF EXISTS "+database+" SYNC"); dropErr != nil {
			t.Errorf("drop %s: %v", database, dropErr)
		}
		if dropErr := admin.Exec(ctx, "DROP USER IF EXISTS "+user); dropErr != nil {
			t.Errorf("drop %s: %v", user, dropErr)
		}
		if closeErr := admin.Close(); closeErr != nil {
			t.Errorf("close clickhouse: %v", closeErr)
		}
	})
	ingestURL, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	ingestURL.User = url.UserPassword(user, password)
	return &ingestFixture{admin: admin, adminDSN: dsn, ingestDSN: ingestURL.String(), database: database}
}

func (f *ingestFixture) migrate(t *testing.T) {
	t.Helper()
	cfg := migrations.Config{
		DSN: f.adminDSN, Database: f.database, LogsTTL: testTTL, TracesTTL: testTTL, MetricsTTL: testTTL,
	}
	if err := migrations.Up(t.Context(), cfg); err != nil {
		t.Fatalf("migrate: %v", err)
	}
}

func (f *ingestFixture) writer(t *testing.T) *Writer {
	t.Helper()
	cfg := Config{
		DSN:         f.ingestDSN,
		Database:    f.database,
		Timeout:     testIngestTimeout,
		DialTimeout: testDialTimeout,
		Version:     "test",
	}
	writer, err := NewWriter(t.Context(), cfg, zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	return writer
}

func (f *ingestFixture) ingestConn(t *testing.T) driver.Conn {
	t.Helper()
	client, err := chClient.NewClient(f.ingestDSN, f.database, testMaxConns)
	if err != nil {
		t.Fatalf("open ingest connection: %v", err)
	}
	t.Cleanup(client.Close)
	return client.Conn()
}

func tenantResource(resource pcommon.Resource) {
	attrs := resource.Attributes()
	attrs.PutStr(serviceNameAttribute, testServiceName)
	attrs.PutStr("loco.organization.id", testOrganizationID)
	attrs.PutStr("loco.workspace.id", testWorkspaceID)
	attrs.PutStr("loco.environment.id", testEnvironmentID)
	attrs.PutStr("loco.resource.id", testResourceID)
	attrs.PutStr(regionAttribute, testRegion)
}

func testLogs(now time.Time) plog.Logs {
	logs := plog.NewLogs()
	resourceLogs := logs.ResourceLogs().AppendEmpty()
	tenantResource(resourceLogs.Resource())
	record := resourceLogs.ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	record.SetTimestamp(pcommon.NewTimestampFromTime(now))
	record.SetSeverityText("INFO")
	record.SetSeverityNumber(plog.SeverityNumberInfo)
	record.SetEventName(testEventName)
	record.SetTraceID(testTraceID)
	record.SetSpanID(testSpanID)
	record.Body().SetStr(testIngestedLogBody)
	return logs
}

func testTraces(now time.Time) ptrace.Traces {
	traces := ptrace.NewTraces()
	resourceSpans := traces.ResourceSpans().AppendEmpty()
	tenantResource(resourceSpans.Resource())
	span := resourceSpans.ScopeSpans().AppendEmpty().Spans().AppendEmpty()
	span.SetName(testSpanName)
	span.SetTraceID(testTraceID)
	span.SetSpanID(testSpanID)
	span.SetKind(ptrace.SpanKindServer)
	span.SetStartTimestamp(pcommon.NewTimestampFromTime(now))
	span.SetEndTimestamp(pcommon.NewTimestampFromTime(now.Add(testSpanDuration)))
	return traces
}

func testMetrics(now time.Time) pmetric.Metrics {
	metrics := pmetric.NewMetrics()
	resourceMetrics := metrics.ResourceMetrics().AppendEmpty()
	tenantResource(resourceMetrics.Resource())
	metric := resourceMetrics.ScopeMetrics().AppendEmpty().Metrics().AppendEmpty()
	metric.SetName(testMetricName)
	point := metric.SetEmptyGauge().DataPoints().AppendEmpty()
	point.SetTimestamp(pcommon.NewTimestampFromTime(now))
	point.SetDoubleValue(testMetricValue)
	return metrics
}

func postOTLP(t *testing.T, endpoint, authorization, contentType string, body []byte) []byte {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", contentType)
	request.Header.Set(authorizationHeader, authorization)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("POST %s: %v", endpoint, err)
	}
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if err := response.Body.Close(); err != nil {
		t.Errorf("close body: %v", err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("POST %s status = %d", endpoint, response.StatusCode)
	}
	return payload
}

func startServers(t *testing.T, service *Service) (string, ptraceotlp.GRPCClient) {
	t.Helper()
	httpServer := httptest.NewServer(service.Handler())
	t.Cleanup(httpServer.Close)
	var lc net.ListenConfig
	listener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	grpcServer := service.GRPCServer()
	go func() {
		if serveErr := grpcServer.Serve(listener); serveErr != nil {
			t.Errorf("serve grpc: %v", serveErr)
		}
	}()
	t.Cleanup(grpcServer.Stop)
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial grpc: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Errorf("close grpc: %v", err)
		}
	})
	return httpServer.URL, ptraceotlp.NewGRPCClient(conn)
}

func (f *ingestFixture) startedWriter(t *testing.T) *Writer {
	t.Helper()
	f.migrate(t)
	writer := f.writer(t)
	if err := WaitForSchema(t.Context(), f.ingestConn(t), f.database, writer.Tables(), testWaitInterval); err != nil {
		t.Fatalf("WaitForSchema: %v", err)
	}
	if err := writer.Start(t.Context()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := writer.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return writer
}

func TestOTLPIngestIsReadableThroughTheQueryPath(t *testing.T) {
	f := newIngestFixture(t)
	writer := f.startedWriter(t)
	cp := newPlacedControlPlane(t)
	service := newGatedService(writer.Consumers(), cp)
	httpURL, tracesClient := startServers(t, service)

	now := time.Now().UTC().Truncate(time.Second)
	logsRequest := plogotlp.NewExportRequestFromLogs(testLogs(now))
	logsBody, err := logsRequest.MarshalProto()
	if err != nil {
		t.Fatalf("marshal logs: %v", err)
	}
	postOTLP(t, httpURL+LogsPath, cp.bearer(), protobufContentType, logsBody)
	metricsRequest := pmetricotlp.NewExportRequestFromMetrics(testMetrics(now))
	metricsBody, err := metricsRequest.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal metrics: %v", err)
	}
	postOTLP(t, httpURL+MetricsPath, cp.bearer(), jsonContentType, metricsBody)
	tracesRequest := ptraceotlp.NewExportRequestFromTraces(testTraces(now))
	tracesCtx := metadata.AppendToOutgoingContext(t.Context(), authorizationMetadata, cp.bearer())
	if _, err = tracesClient.Export(tracesCtx, tracesRequest); err != nil {
		t.Fatalf("export traces over grpc: %v", err)
	}

	reader := f.readerConn(t)
	start := now.Add(-testWindow)
	end := now.Add(testWindow)
	entries, _, err := chClient.QueryLogs(
		t.Context(), reader, testWorkspaceID, []string{testResourceID}, start, end, "", nil, nil,
		testQueryLimit, "", observabilityv1.LogOrder_LOG_ORDER_NEWEST_FIRST, testQueryTimeout, chClient.LogScope{},
	)
	if err != nil {
		t.Fatalf("QueryLogs: %v", err)
	}
	if len(entries) != 1 || entries[0].GetBody() != testIngestedLogBody ||
		entries[0].GetResourceId() != testResourceID {
		t.Fatalf("QueryLogs = %v, want the ingested record", entries)
	}

	traces, err := chClient.QueryTraces(t.Context(), reader, &observabilityv1.QueryTracesRequest{
		WorkspaceId: testWorkspaceID,
		StartTime:   timestamppb.New(start),
		EndTime:     timestamppb.New(end),
	}, testQueryLimit, testQueryTimeout)
	if err != nil {
		t.Fatalf("QueryTraces: %v", err)
	}
	spans := traces.GetSpans()
	if len(spans) != 1 || spans[0].GetName() != testSpanName || spans[0].GetTraceId() != testTraceID.String() {
		t.Fatalf("QueryTraces = %v, want the ingested span", spans)
	}

	series, err := chClient.QueryMetrics(
		t.Context(), reader, testWorkspaceID, testEnvironmentID, testRegion, []string{testResourceID},
		start, end, testMetricName, testIntervalSeconds, avgAggregation, testQueryTimeout,
	)
	if err != nil {
		t.Fatalf("QueryMetrics: %v", err)
	}
	if len(series) != 1 || len(series[0].GetPoints()) != 1 || series[0].GetPoints()[0].GetValue() != testMetricValue {
		t.Fatalf("QueryMetrics = %v, want the ingested point", series)
	}

	var eventName, organizationID string
	row := f.admin.QueryRow(t.Context(), "SELECT EventName, OrganizationId FROM "+f.database+".otel_logs")
	if err := row.Scan(&eventName, &organizationID); err != nil {
		t.Fatalf("read log columns: %v", err)
	}
	if eventName != testEventName || organizationID != testOrganizationID {
		t.Fatalf("EventName = %q, OrganizationId = %q", eventName, organizationID)
	}
}

func TestOnlyRecordsOfPlacedWorkspacesReachClickHouse(t *testing.T) {
	f := newIngestFixture(t)
	writer := f.startedWriter(t)
	cp := newPlacedControlPlane(t)
	service := newGatedService(writer.Consumers(), cp)
	httpURL, _ := startServers(t, service)

	now := time.Now().UTC().Truncate(time.Second)
	logs := testLogs(now)
	foreign := logs.ResourceLogs().AppendEmpty()
	testLogs(now).ResourceLogs().At(0).CopyTo(foreign)
	foreign.Resource().Attributes().PutStr(workspaceAttribute, testOtherWorkspaceID)
	body, err := plogotlp.NewExportRequestFromLogs(logs).MarshalProto()
	if err != nil {
		t.Fatalf("marshal logs: %v", err)
	}
	payload := postOTLP(t, httpURL+LogsPath, cp.bearer(), protobufContentType, body)
	response := plogotlp.NewExportResponse()
	if decodeErr := response.UnmarshalProto(payload); decodeErr != nil {
		t.Fatalf("decode response: %v", decodeErr)
	}
	if response.PartialSuccess().RejectedLogRecords() != 1 {
		t.Fatalf("partial success = %v, want one rejected record", response.PartialSuccess())
	}

	countQuery := "SELECT WorkspaceId, count() FROM " + f.database + ".otel_logs GROUP BY WorkspaceId"
	rows, err := f.admin.Query(t.Context(), countQuery)
	if err != nil {
		t.Fatalf("count rows: %v", err)
	}
	defer rows.Close()
	stored := map[string]uint64{}
	for rows.Next() {
		var workspaceID string
		var count uint64
		if err := rows.Scan(&workspaceID, &count); err != nil {
			t.Fatalf("scan: %v", err)
		}
		stored[workspaceID] = count
	}
	if len(stored) != 1 || stored[testWorkspaceID] != 1 {
		t.Fatalf("stored rows by workspace = %v, want one row for %s", stored, testWorkspaceID)
	}
}

func clusterLevel(resource pcommon.Resource) {
	resource.Attributes().Remove(workspaceAttribute)
	resource.Attributes().PutStr(clusterAttribute, testForgedClusterID)
}

func TestClusterLevelRowsStayOutOfTenantQueries(t *testing.T) {
	f := newIngestFixture(t)
	writer := f.startedWriter(t)
	cp := newPlacedControlPlane(t)
	service := newGatedService(writer.Consumers(), cp)
	httpURL, tracesClient := startServers(t, service)

	now := time.Now().UTC().Truncate(time.Second)
	logs := testLogs(now)
	testLogs(now).ResourceLogs().At(0).CopyTo(logs.ResourceLogs().AppendEmpty())
	clusterLevel(logs.ResourceLogs().At(1).Resource())
	traces := testTraces(now)
	testTraces(now).ResourceSpans().At(0).CopyTo(traces.ResourceSpans().AppendEmpty())
	clusterLevel(traces.ResourceSpans().At(1).Resource())
	metrics := testMetrics(now)
	testMetrics(now).ResourceMetrics().At(0).CopyTo(metrics.ResourceMetrics().AppendEmpty())
	clusterLevel(metrics.ResourceMetrics().At(1).Resource())

	logsBody, err := plogotlp.NewExportRequestFromLogs(logs).MarshalProto()
	if err != nil {
		t.Fatalf("marshal logs: %v", err)
	}
	postOTLP(t, httpURL+LogsPath, cp.bearer(), protobufContentType, logsBody)
	metricsBody, err := pmetricotlp.NewExportRequestFromMetrics(metrics).MarshalProto()
	if err != nil {
		t.Fatalf("marshal metrics: %v", err)
	}
	postOTLP(t, httpURL+MetricsPath, cp.bearer(), protobufContentType, metricsBody)
	tracesCtx := metadata.AppendToOutgoingContext(t.Context(), authorizationMetadata, cp.bearer())
	if _, err = tracesClient.Export(tracesCtx, ptraceotlp.NewExportRequestFromTraces(traces)); err != nil {
		t.Fatalf("export traces: %v", err)
	}

	for _, table := range []string{"otel_logs", "otel_traces", "otel_metrics_gauge"} {
		var count uint64
		query := "SELECT count() FROM " + f.database + "." + table +
			" WHERE WorkspaceId = '' AND ResourceAttributes[?] = ?"
		row := f.admin.QueryRow(t.Context(), query, clusterAttribute, testClusterID)
		if scanErr := row.Scan(&count); scanErr != nil {
			t.Fatalf("operator read of %s: %v", table, scanErr)
		}
		if count != 1 {
			t.Fatalf(
				"operator read of %s found %d cluster-level rows stamped with %s, want 1",
				table,
				count,
				testClusterID,
			)
		}
	}

	reader := f.readerConn(t)
	start := now.Add(-testWindow)
	end := now.Add(testWindow)
	entries, _, err := chClient.QueryLogs(
		t.Context(), reader, testWorkspaceID, nil, start, end, "", nil, nil,
		testQueryLimit, "", observabilityv1.LogOrder_LOG_ORDER_NEWEST_FIRST, testQueryTimeout, chClient.LogScope{},
	)
	if err != nil {
		t.Fatalf("QueryLogs: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("QueryLogs returned %d entries, want only the workspace's record", len(entries))
	}
	traceResult, err := chClient.QueryTraces(t.Context(), reader, &observabilityv1.QueryTracesRequest{
		WorkspaceId: testWorkspaceID,
		StartTime:   timestamppb.New(start),
		EndTime:     timestamppb.New(end),
	}, testQueryLimit, testQueryTimeout)
	if err != nil {
		t.Fatalf("QueryTraces: %v", err)
	}
	if len(traceResult.GetSpans()) != 1 {
		t.Fatalf("QueryTraces returned %d spans, want only the workspace's span", len(traceResult.GetSpans()))
	}
	series, err := chClient.QueryMetrics(
		t.Context(), reader, testWorkspaceID, testEnvironmentID, testRegion, []string{testResourceID},
		start, end, testMetricName, testIntervalSeconds, sumAggregation, testQueryTimeout,
	)
	if err != nil {
		t.Fatalf("QueryMetrics: %v", err)
	}
	if len(series) != 1 || len(series[0].GetPoints()) != 1 || series[0].GetPoints()[0].GetValue() != testMetricValue {
		t.Fatalf("QueryMetrics = %v, want only the workspace's point summed", series)
	}
}

func (f *ingestFixture) readerConn(t *testing.T) driver.Conn {
	t.Helper()
	client, err := chClient.NewClient(f.adminDSN, f.database, testMaxConns)
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	t.Cleanup(client.Close)
	return client.Conn()
}

func TestWaitForSchemaBlocksUntilMigrated(t *testing.T) {
	f := newIngestFixture(t)
	writer := f.writer(t)
	conn := f.ingestConn(t)
	ctx, cancel := context.WithTimeout(t.Context(), testWaitBudget)
	defer cancel()
	err := WaitForSchema(ctx, conn, f.database, writer.Tables(), testWaitInterval)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitForSchema before the database exists = %v, want an expired budget", err)
	}
	if err = f.admin.Exec(t.Context(), "CREATE DATABASE "+f.database); err != nil {
		t.Fatalf("create database: %v", err)
	}
	ctx, cancel = context.WithTimeout(t.Context(), testWaitBudget)
	defer cancel()
	err = WaitForSchema(ctx, conn, f.database, writer.Tables(), testWaitInterval)
	if !errors.Is(err, errTableMissing) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitForSchema before migrations = %v, want a missing table and an expired budget", err)
	}
	f.migrate(t)
	if err := WaitForSchema(t.Context(), conn, f.database, writer.Tables(), testWaitInterval); err != nil {
		t.Fatalf("WaitForSchema after migrations = %v", err)
	}
}
