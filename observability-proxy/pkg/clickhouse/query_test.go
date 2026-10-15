package clickhouse

import (
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

const (
	testWorkspaceID = "11111111-2222-3333-4444-555555555555"
	testResourceID  = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
)

func TestBuildLogsQueryFiltersOnTenantColumns(t *testing.T) {
	end := time.Now()
	start := end.Add(-time.Hour)

	query, args, err := buildLogsQuery(
		testWorkspaceID,
		[]string{testResourceID},
		start,
		end,
		"",
		nil,
		map[string]string{"app.loco.io/component": "web"},
		100,
		"",
		observabilityv1.LogOrder_LOG_ORDER_NEWEST_FIRST,
		10, LogRegions{},
	)

	if err != nil {
		t.Fatal(err)
	}
	wantClauses := []string{
		"SELECT Timestamp, SeverityText, Body, ResourceId, TraceId, SpanId",
		"WHERE WorkspaceId = ?",
		"ResourceId IN (?)",
		"ResourceAttributes[?] = ?",
	}
	for _, clause := range wantClauses {
		if !strings.Contains(query, clause) {
			t.Errorf("query missing %q:\n%s", clause, query)
		}
	}
	if strings.Contains(query, "k8s.pod.labels.") {
		t.Errorf("query uses prefixed label keys:\n%s", query)
	}
	if strings.Contains(query, "ResourceAttributes['") {
		t.Errorf("query reads tenancy from resource attributes:\n%s", query)
	}

	if len(args) < 2 {
		t.Fatalf("got %d args, want at least 2", len(args))
	}
	if args[0] != testWorkspaceID {
		t.Errorf("args[0] = %v, want workspace id %q", args[0], testWorkspaceID)
	}
	if args[1] != testResourceID {
		t.Errorf("args[1] = %v, want resource id %q", args[1], testResourceID)
	}
	if !slices.Contains(args, any("app.loco.io/component")) {
		t.Errorf("args missing raw label key: %v", args)
	}
}

func TestBuildMetricsQueryFiltersOnTenantColumns(t *testing.T) {
	end := time.Now()
	start := end.Add(-time.Hour)

	query, args := buildMetricsQuery(
		testWorkspaceID,
		"",
		"",
		[]string{testResourceID},
		start,
		end,
		"k8s.pod.cpu.usage",
		60,
		"avg",
		10,
	)

	wantClauses := []string{
		"SELECT ResourceId,",
		"FROM otel_metrics_gauge",
		"WHERE WorkspaceId = ?",
		"ResourceId IN (?)",
		"GROUP BY ResourceId, bucket",
		"ORDER BY ResourceId, bucket",
	}
	for _, clause := range wantClauses {
		if !strings.Contains(query, clause) {
			t.Errorf("query missing %q:\n%s", clause, query)
		}
	}
	if strings.Contains(query, "k8s.pod.labels.") {
		t.Errorf("query uses prefixed label keys:\n%s", query)
	}
	if strings.Contains(query, "ResourceAttributes['") {
		t.Errorf("query reads tenancy from resource attributes:\n%s", query)
	}

	if len(args) < 2 {
		t.Fatalf("got %d args, want at least 2", len(args))
	}
	if args[0] != testWorkspaceID {
		t.Errorf("args[0] = %v, want workspace id %q", args[0], testWorkspaceID)
	}
	if args[1] != testResourceID {
		t.Errorf("args[1] = %v, want resource id %q", args[1], testResourceID)
	}
	if strings.Contains(query, "EnvironmentId") {
		t.Errorf("query filters on an environment none was requested for:\n%s", query)
	}
}

func TestBuildMetricsQueryFiltersOnEnvironment(t *testing.T) {
	end := time.Now()
	start := end.Add(-time.Hour)

	query, args := buildMetricsQuery(
		testWorkspaceID,
		testEnvironmentID,
		"",
		[]string{testResourceID},
		start,
		end,
		testMetricName,
		testIntervalSeconds,
		avgAggregation,
		testQueryTimeout,
	)

	if !strings.Contains(query, "WHERE WorkspaceId = ? AND EnvironmentId = ? AND ResourceId IN (?)") {
		t.Errorf("query does not filter on the environment column:\n%s", query)
	}
	wantArgs := []any{testWorkspaceID, testEnvironmentID, testResourceID}
	if len(args) < len(wantArgs) || !slices.Equal(args[:len(wantArgs)], wantArgs) {
		t.Errorf("args = %v, want prefix %v", args, wantArgs)
	}
}

func TestBuildMetricsQueryFiltersOnRegion(t *testing.T) {
	end := time.Now()
	start := end.Add(-time.Hour)

	query, args := buildMetricsQuery(
		testWorkspaceID,
		testEnvironmentID,
		testRegion,
		[]string{testResourceID},
		start,
		end,
		testMetricName,
		testIntervalSeconds,
		avgAggregation,
		testQueryTimeout,
	)

	if !strings.Contains(
		query,
		"WHERE WorkspaceId = ? AND EnvironmentId = ? AND ResourceAttributes[?] = ? AND ResourceId IN (?)",
	) {
		t.Errorf("query does not filter on the region attribute:\n%s", query)
	}
	wantArgs := []any{testWorkspaceID, testEnvironmentID, regionAttribute, testRegion, testResourceID}
	if len(args) < len(wantArgs) || !slices.Equal(args[:len(wantArgs)], wantArgs) {
		t.Errorf("args = %v, want prefix %v", args, wantArgs)
	}
}

func TestInvalidLogCursorsAreRejected(t *testing.T) {
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	cases := []string{
		"not a cursor",
		base64.RawURLEncoding.EncodeToString([]byte(timestamp)),
		base64.RawURLEncoding.EncodeToString([]byte(timestamp + "/bad/0")),
		base64.RawURLEncoding.EncodeToString([]byte(timestamp + "/0/-1")),
	}
	for _, cursor := range cases {
		_, _, err := buildLogsQuery(
			testWorkspaceID,
			nil,
			time.Now().Add(-time.Hour),
			time.Now(),
			"",
			nil,
			nil,
			testPageSize,
			cursor,
			observabilityv1.LogOrder_LOG_ORDER_OLDEST_FIRST,
			testQueryTimeout, LogRegions{},
		)
		if !errors.Is(err, ErrInvalidLogCursor) {
			t.Fatalf("cursor %q error = %v, want invalid cursor", cursor, err)
		}
	}
}

func TestBuildLogsQueryRegionFiltersKeepWorkspaceScope(t *testing.T) {
	start, end := time.Now().Add(-time.Hour), time.Now()
	query, args, err := buildLogsQuery(testWorkspaceID, nil, start, end, "", nil, nil,
		testPageSize, "", observabilityv1.LogOrder_LOG_ORDER_NEWEST_FIRST, testQueryTimeout,
		LogRegions{Include: []string{"US-*", "eu-west-1"}, Exclude: []string{"us-west-*"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, clause := range []string{
		"WHERE WorkspaceId = ? AND",
		"(startsWith(lowerUTF8(ResourceAttributes[?]), ?) OR lowerUTF8(ResourceAttributes[?]) = ?)",
		"NOT (startsWith(lowerUTF8(ResourceAttributes[?]), ?))",
		"ORDER BY Timestamp DESC, _block_number DESC, _block_offset DESC",
	} {
		if !strings.Contains(query, clause) {
			t.Errorf("missing %q in %s", clause, query)
		}
	}
	if args[0] != testWorkspaceID {
		t.Fatalf("first argument = %v", args[0])
	}
	for _, value := range []string{"us-", "eu-west-1", "us-west-"} {
		if strings.Contains(query, value) {
			t.Errorf("region value interpolated into SQL: %s", query)
		}
		if !slices.Contains(args, any(value)) {
			t.Errorf("missing region argument %q: %v", value, args)
		}
	}
}
