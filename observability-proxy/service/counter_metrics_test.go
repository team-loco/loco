package service

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"io"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/observability-proxy/migrations"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
)

const (
	counterHTTPQueryTimeout       = 60
	counterHTTPWorkspaceAttribute = "loco.workspace.id"
	counterHTTPMetric             = "demo.payment.transactions"
	counterHTTPResource           = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	counterHTTPEnvironment        = "66666666-7777-8888-9999-000000000000"
	counterHTTPOtherEnvironment   = "77777777-8888-9999-0000-111111111111"
	counterHTTPOtherResource      = "ffffffff-eeee-dddd-cccc-bbbbbbbbbbbb"
	counterHTTPRegion             = "us-east-1"
	counterHTTPPollTimeout        = 30 * time.Second
	counterHTTPPollInterval       = 100 * time.Millisecond
	counterHTTPInterval           = 60
	counterHTTPValue              = 7.0
	counterOtherValue             = 900.0
)

func counterRequest(workspace string, timestamp time.Time) *observabilityv1.QueryMetricsRequest {
	return &observabilityv1.QueryMetricsRequest{
		WorkspaceId: workspace, ResourceIds: []string{counterHTTPResource},
		EnvironmentId: counterHTTPEnvironment, Region: counterHTTPRegion,
		StartTime: timestamppb.New(timestamp.Add(-time.Second)), EndTime: timestamppb.New(timestamp.Add(time.Minute)),
		MetricName: counterHTTPMetric, IntervalSeconds: counterHTTPInterval, Aggregation: "max",
	}
}

func TestCounterMetricsHTTPRequiresWorkspacePermission(t *testing.T) {
	client := traceTestClient(t, nil)
	for _, tc := range []struct {
		token, workspace string
		code             connect.Code
	}{
		{"", tailTestWorkspace, connect.CodeUnauthenticated},
		{traceTestReader, traceTestOtherWorkspace, connect.CodePermissionDenied},
		{"unknown", tailTestWorkspace, connect.CodePermissionDenied},
	} {
		ctx, info := connect.NewClientContext(t.Context())
		if tc.token != "" {
			info.RequestHeader().Set("Authorization", "Bearer "+tc.token)
		}
		_, err := client.QueryMetrics(ctx, counterRequest(tc.workspace, time.Now()))
		if connect.CodeOf(err) != tc.code {
			t.Fatalf("error=%v, want %v", err, tc.code)
		}
	}
}

//nolint:gosec
func TestCounterMetricsCollectorToAuthenticatedHTTP(t *testing.T) {
	endpoint := os.Getenv("LOCO_TEST_COUNTER_OTLP_URL")
	if endpoint == "" {
		t.Skip("LOCO_TEST_COUNTER_OTLP_URL not set")
	}
	dsn := os.Getenv("LOCO_TEST_CLICKHOUSE_URL")
	database := os.Getenv("LOCO_TEST_COUNTER_DATABASE")
	if dsn == "" || database == "" {
		t.Fatal("counter integration requires ClickHouse URL and database")
	}
	cfg := migrations.Config{
		DSN:        dsn,
		Database:   database,
		LogsTTL:    time.Hour,
		TracesTTL:  time.Hour,
		MetricsTTL: time.Hour,
	}
	if err := migrations.Up(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	ch, err := chClient.NewClient(dsn, database, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if dropErr := ch.Conn().Exec(context.Background(), "DROP DATABASE "+database+" SYNC"); dropErr != nil {
			t.Error(dropErr)
		}
		ch.Close()
	})
	timestamp := time.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	payload := counterPayload(t, timestamp)
	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		endpoint+"/v1/metrics",
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("collector response %d: %s", response.StatusCode, body)
	}
	client := traceTestClient(t, ch, counterHTTPQueryTimeout)
	for _, tc := range []struct {
		token, workspace string
		want             float64
	}{
		{traceTestReader, tailTestWorkspace, counterHTTPValue},
		{"other-reader", traceTestOtherWorkspace, counterOtherValue},
	} {
		ctx, info := connect.NewClientContext(t.Context())
		info.RequestHeader().Set("Authorization", "Bearer "+tc.token)
		deadline := time.Now().Add(counterHTTPPollTimeout)
		for {
			result, queryErr := client.QueryMetrics(ctx, counterRequest(tc.workspace, timestamp))
			if queryErr != nil {
				t.Fatal(queryErr)
			}
			if len(result.GetSeries()) > 0 {
				if len(result.GetSeries()) != 1 {
					t.Fatalf("series=%v, want one scoped counter", result.GetSeries())
				}
				series := result.GetSeries()[0]
				if series.GetResourceId() != counterHTTPResource || len(series.GetPoints()) != 1 ||
					series.GetPoints()[0].GetValue() != tc.want {
					t.Fatalf("series=%v, want scoped max=%v", series, tc.want)
				}
				labels := series.GetLabels()
				if labels["metric_type"] != "sum" || labels["aggregation_temporality"] != "2" ||
					labels["is_monotonic"] != "true" {
					t.Fatalf("counter metadata=%v", labels)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("exported counter did not become queryable")
			}
			time.Sleep(counterHTTPPollInterval)
		}
	}
}

func counterPayload(t *testing.T, timestamp time.Time) []byte {
	t.Helper()
	rows := []struct {
		workspace, environment, region, resource string
		value                                    float64
	}{
		{tailTestWorkspace, counterHTTPEnvironment, counterHTTPRegion, counterHTTPResource, counterHTTPValue},
		{traceTestOtherWorkspace, counterHTTPEnvironment, counterHTTPRegion, counterHTTPResource, counterOtherValue},
		{tailTestWorkspace, counterHTTPOtherEnvironment, counterHTTPRegion, counterHTTPResource, 100},
		{tailTestWorkspace, counterHTTPEnvironment, "eu-west-1", counterHTTPResource, 200},
		{tailTestWorkspace, counterHTTPEnvironment, counterHTTPRegion, counterHTTPOtherResource, 300},
	}
	resources := make([]any, 0, len(rows))
	for _, row := range rows {
		var attrs []any
		attributes := map[string]string{
			counterHTTPWorkspaceAttribute: row.workspace,
			"loco.environment.id":         row.environment,
			"loco.resource.id":            row.resource,
			"loco.region":                 row.region,
		}
		for key, value := range attributes {
			attrs = append(attrs, map[string]any{"key": key, "value": map[string]string{"stringValue": value}})
		}
		point := map[string]any{
			"timeUnixNano":      strconv.FormatInt(timestamp.UnixNano(), 10),
			"startTimeUnixNano": strconv.FormatInt(timestamp.Add(-time.Minute).UnixNano(), 10),
			"asDouble":          row.value,
		}
		metric := map[string]any{
			"name": counterHTTPMetric,
			"sum":  map[string]any{"aggregationTemporality": 2, "isMonotonic": true, "dataPoints": []any{point}},
		}
		resources = append(
			resources,
			map[string]any{
				"resource":     map[string]any{"attributes": attrs},
				"scopeMetrics": []any{map[string]any{"metrics": []any{metric}}},
			},
		)
	}
	encoded, err := json.Marshal(map[string]any{"resourceMetrics": resources})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
