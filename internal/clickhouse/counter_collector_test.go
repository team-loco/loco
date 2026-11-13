package clickhouse

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"io"
	"maps"
	"net"
	"net/http"
	"os"
	"strconv"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/internal/ingesttoken/ingesttest"
)

const (
	collectorQueryTimeout       = 60 * time.Second
	collectorMetric             = "demo.payment.transactions"
	collectorEnvironment        = "66666666-7777-8888-9999-000000000000"
	collectorOtherEnvironment   = "77777777-8888-9999-0000-111111111111"
	collectorOtherResource      = "ffffffff-eeee-dddd-cccc-bbbbbbbbbbbb"
	collectorPollTimeout        = 30 * time.Second
	collectorPollInterval       = 100 * time.Millisecond
	collectorInterval           = 60
	collectorValue              = 7.0
	collectorOtherValue         = 900.0
	collectorUnrecordedValue    = 10000.0
	collectorPlacedBefore       = time.Hour
	collectorStartTimeOffset    = time.Minute
	collectorRecordedFlags      = 2
	collectorAggregation        = "max"
	collectorWorkerRegion       = "eu-west-1"
	collectorOtherEnvValue      = 100
	collectorOtherRegionValue   = 200
	collectorOtherResourceValue = 300
)

func collectorRequest(workspace string, timestamp time.Time) *observabilityv1.QueryMetricsRequest {
	return &observabilityv1.QueryMetricsRequest{
		WorkspaceId: workspace, ResourceIds: []string{testResourceID},
		EnvironmentId: collectorEnvironment, Region: testRegion,
		StartTime: timestamppb.New(timestamp.Add(-time.Second)), EndTime: timestamppb.New(timestamp.Add(time.Minute)),
		MetricName: collectorMetric, IntervalSeconds: collectorInterval, Aggregation: collectorAggregation,
	}
}

// TestCounterMetricsFromTheCollectorAreQueryable sends a counter through the chart-pinned
// Collector to a running observability proxy, which `mise run test:counter-metrics` starts, and
// reads it back per workspace.
func TestCounterMetricsFromTheCollectorAreQueryable(t *testing.T) {
	endpoint := os.Getenv("LOCO_TEST_COUNTER_OTLP_URL")
	if endpoint == "" {
		t.Skip("LOCO_TEST_COUNTER_OTLP_URL not set")
	}
	dsn := os.Getenv(testClickHouseEnv)
	database := os.Getenv("LOCO_TEST_COUNTER_DATABASE")
	ingestKey := os.Getenv("LOCO_TEST_COUNTER_INGEST_KEY")
	controlPlaneAddr := os.Getenv("LOCO_TEST_COUNTER_CONTROL_PLANE_ADDR")
	if dsn == "" || database == "" || ingestKey == "" || controlPlaneAddr == "" {
		t.Fatal(
			"counter integration requires ClickHouse, its database, an ingest signing key file and a control plane address",
		)
	}
	ch, err := NewClient(dsn, database, testMaxConns, collectorQueryTimeout)
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
	startCollectorControlPlane(t, ingestKey, controlPlaneAddr, timestamp.Add(-collectorPlacedBefore))
	postCollectorMetrics(t, endpoint, collectorPayload(t, timestamp))

	for _, tc := range []struct {
		workspace string
		want      float64
	}{
		{testWorkspaceID, collectorValue},
		{testOtherWorkspaceID, collectorOtherValue},
	} {
		deadline := time.Now().Add(collectorPollTimeout)
		for {
			series, queryErr := ch.QueryMetrics(t.Context(), collectorRequest(tc.workspace, timestamp))
			if queryErr != nil {
				t.Fatal(queryErr)
			}
			if len(series) > 0 {
				if len(series) != 1 {
					t.Fatalf("series=%v, want one scoped counter", series)
				}
				if series[0].GetResourceId() != testResourceID || len(series[0].GetPoints()) != 1 ||
					series[0].GetPoints()[0].GetValue() != tc.want {
					t.Fatalf("series=%v, want scoped max=%v", series[0], tc.want)
				}
				labels := series[0].GetLabels()
				if labels["metric_type"] != sumMetricType || labels["aggregation_temporality"] != "2" ||
					labels["is_monotonic"] != "true" {
					t.Fatalf("counter metadata=%v", labels)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("exported counter did not become queryable")
			}
			time.Sleep(collectorPollInterval)
		}
	}
}

//nolint:gosec // the endpoint is the test Collector the task starts
func postCollectorMetrics(t *testing.T, endpoint string, payload []byte) {
	t.Helper()
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
}

func startCollectorControlPlane(t *testing.T, keyFile, addr string, placedAt time.Time) {
	t.Helper()
	signer, err := ingesttest.ReadSigner(keyFile)
	if err != nil {
		t.Fatal(err)
	}
	var lc net.ListenConfig
	listener, err := lc.Listen(t.Context(), "tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	controlPlane := ingesttest.Serve(listener, []ingesttest.Signer{signer})
	t.Cleanup(controlPlane.Close)
	for _, workspace := range []string{testWorkspaceID, testOtherWorkspaceID} {
		controlPlane.Place(workspace, placedAt, time.Time{})
	}
}

// collectorPayload holds one recorded counter point per workspace, plus points in another
// environment, region and resource and points flagged as not recorded, none of which the query
// may return.
func collectorPayload(t *testing.T, timestamp time.Time) []byte {
	t.Helper()
	rows := []struct {
		workspace, environment, region, resource string
		value                                    float64
	}{
		{testWorkspaceID, collectorEnvironment, testRegion, testResourceID, collectorValue},
		{testOtherWorkspaceID, collectorEnvironment, testRegion, testResourceID, collectorOtherValue},
		{testWorkspaceID, collectorOtherEnvironment, testRegion, testResourceID, collectorOtherEnvValue},
		{testWorkspaceID, collectorEnvironment, collectorWorkerRegion, testResourceID, collectorOtherRegionValue},
		{testWorkspaceID, collectorEnvironment, testRegion, collectorOtherResource, collectorOtherResourceValue},
	}
	resources := make([]any, 0, len(rows))
	for _, row := range rows {
		var attrs []any
		attributes := map[string]string{
			workspaceAttributeKey: row.workspace,
			environmentAttrKey:    row.environment,
			resourceAttributeKey:  row.resource,
			"loco.region":         row.region,
		}
		for key, value := range attributes {
			attrs = append(attrs, map[string]any{"key": key, "value": map[string]string{"stringValue": value}})
		}
		point := map[string]any{
			"timeUnixNano":      strconv.FormatInt(timestamp.UnixNano(), 10),
			"startTimeUnixNano": strconv.FormatInt(timestamp.Add(-collectorStartTimeOffset).UnixNano(), 10),
			"asDouble":          row.value,
			"flags":             collectorRecordedFlags,
		}
		unrecordedFlags := []uint32{1, 3}
		points := make([]any, 1, 1+len(unrecordedFlags))
		points[0] = point
		for _, flags := range unrecordedFlags {
			marker := maps.Clone(point)
			marker["asDouble"] = collectorUnrecordedValue
			marker["flags"] = flags
			points = append(points, marker)
		}
		metric := map[string]any{
			"name":        collectorMetric,
			sumMetricType: map[string]any{"aggregationTemporality": 2, "isMonotonic": true, "dataPoints": points},
		}
		resources = append(resources, map[string]any{
			"resource":     map[string]any{"attributes": attrs},
			"scopeMetrics": []any{map[string]any{"metrics": []any{metric}}},
		})
	}
	encoded, err := json.Marshal(map[string]any{"resourceMetrics": resources})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
