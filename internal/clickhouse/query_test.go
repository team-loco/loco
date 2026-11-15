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

func TestInvalidLogCursorsAreRejected(t *testing.T) {
	timestamp := time.Now().UTC().Format(time.RFC3339Nano)
	cases := []string{
		"not a cursor",
		base64.RawURLEncoding.EncodeToString([]byte(timestamp)),
		base64.RawURLEncoding.EncodeToString([]byte(timestamp + "/bad/0")),
		base64.RawURLEncoding.EncodeToString([]byte(timestamp + "/0/-1")),
	}
	for _, cursor := range cases {
		_, _, err := buildLogPageQuery(LogQuery{
			WorkspaceID: testWorkspaceID,
			Start:       time.Now().Add(-time.Hour),
			End:         time.Now(),
			Limit:       testPageSize,
			Cursor:      cursor,
			Order:       observabilityv1.LogOrder_LOG_ORDER_OLDEST_FIRST,
		}, testQueryTimeout)
		if !errors.Is(err, ErrInvalidLogCursor) {
			t.Fatalf("cursor %q error = %v, want invalid cursor", cursor, err)
		}
	}
}

func TestBuildLogsQueryRegionFiltersKeepWorkspaceScope(t *testing.T) {
	start, end := time.Now().Add(-time.Hour), time.Now()
	query, args, err := buildLogPageQuery(LogQuery{
		WorkspaceID:     testWorkspaceID,
		Start:           start,
		End:             end,
		Regions:         []string{"US-*", "eu-west-1"},
		ExcludedRegions: []string{"us-west-*"},
		Limit:           testPageSize,
		Order:           observabilityv1.LogOrder_LOG_ORDER_NEWEST_FIRST,
	}, testQueryTimeout)
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

func TestBuildTailLogQueryBindsEveryValueByName(t *testing.T) {
	const search = "needle ? {tail_seen:String}"
	query, parameters, err := buildTailLogQuery(LogQuery{
		WorkspaceID:   testWorkspaceID,
		EnvironmentID: "env",
		ResourceIDs:   []string{testResourceID},
		Start:         time.Now().Add(-time.Hour),
		Search:        search,
		Labels:        []*observabilityv1.LogFieldFilter{{Key: "app.loco.io/component", Values: []string{"api"}}},
		Regions:       []string{"us-*"},
		Limit:         testPageSize,
	}, testQueryTimeout, TailLogCursor{"all_1/2": 3})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(query, "?") {
		t.Errorf("positional placeholder in tail query: %s", query)
	}
	if strings.Contains(query, search) || strings.Contains(query, testWorkspaceID) {
		t.Errorf("value interpolated into SQL: %s", query)
	}
	if got := parameters["tail_seen"]; got != `{"all_1/2":3}` {
		t.Errorf("tail_seen = %q", got)
	}
	for _, value := range []string{testWorkspaceID, search, "us-", "loco.region"} {
		var found bool
		for _, got := range parameters {
			found = found || got == value
		}
		if !found {
			t.Errorf("missing parameter %q: %v", value, parameters)
		}
	}
}

func TestBuildLogsQueryBindsIncludedAndExcludedFilterValues(t *testing.T) {
	const trace = "0123456789abcdef0123456789abcdef"
	query, args, err := buildLogPageQuery(LogQuery{
		WorkspaceID:           testWorkspaceID,
		Start:                 time.Now().Add(-time.Hour),
		End:                   time.Now(),
		ExcludedResourceIDs:   []string{testResourceID},
		ExcludedLevels:        []string{"DEBUG"},
		ExcludedLabels:        []*observabilityv1.LogFieldFilter{{Key: "k8s.pod.name", Values: []string{"web-1"}}},
		LogAttributes:         []*observabilityv1.LogFieldFilter{{Key: "http.request.method", Values: []string{"GET"}}},
		ExcludedLogAttributes: []*observabilityv1.LogFieldFilter{{Key: "url.path", Values: []string{"/healthz"}}},
		JSONFields: []*observabilityv1.LogFieldFilter{
			{Key: "customer.tier", Values: []string{"gold", "silver"}, Prefixes: []string{"plat"}},
		},
		TraceIDs:         []string{trace},
		ExcludedTraceIDs: []string{trace},
		Limit:            testPageSize,
	}, testQueryTimeout)
	if err != nil {
		t.Fatal(err)
	}
	bound := []string{
		testResourceID,
		"DEBUG",
		"web-1",
		"GET",
		"/healthz",
		trace,
		"customer",
		"tier",
		"gold",
		"silver",
		"plat",
	}
	for _, value := range bound {
		if strings.Contains(query, value) {
			t.Errorf("value %q interpolated into SQL: %s", value, query)
		}
		if !slices.Contains(args, any(value)) {
			t.Errorf("missing argument %q: %v", value, args)
		}
	}
}
