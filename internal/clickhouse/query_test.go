package clickhouse

import (
	"encoding/base64"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/team-loco/loco/internal/logquery"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

const (
	testUSPrefix    = "us-"
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

func TestBuildLogsQueryKeepsWorkspaceScopeAndBindsFilterValues(t *testing.T) {
	start, end := time.Now().Add(-time.Hour), time.Now()
	query, args, err := buildLogPageQuery(LogQuery{
		WorkspaceID: testWorkspaceID,
		Start:       start,
		End:         end,
		Filter: mustParse(
			t,
			`(@region:US-* OR @region:eu-west-1) -@region:us-west-* @status:400..499 @user.tier:"gold plan"`,
		),
		Limit: testPageSize,
		Order: observabilityv1.LogOrder_LOG_ORDER_NEWEST_FIRST,
	}, testQueryTimeout)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "WHERE WorkspaceId = ? AND") || args[0] != testWorkspaceID {
		t.Fatalf("query %s with first argument %v does not lead with the workspace scope", query, args[0])
	}
	for _, value := range []any{testUSPrefix, "eu-west-1", "us-west-", 400.0, 499.0, "user.tier", "gold plan"} {
		if text, ok := value.(string); ok && strings.Contains(query, text) {
			t.Errorf("value %q interpolated into SQL: %s", text, query)
		}
		if !slices.Contains(args, value) {
			t.Errorf("missing argument %v: %v", value, args)
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
		Filter: logquery.And{Terms: []logquery.Expr{
			logquery.Text{Value: search},
			logquery.Field{Key: "app.loco.io/component", Value: "api"},
			logquery.Field{Key: RegionField, Match: logquery.Prefix, Value: testUSPrefix},
			logquery.Field{Key: "latency", Match: logquery.Greater, Low: 1.5},
		}},
		Limit: testPageSize,
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
	for _, value := range []string{testWorkspaceID, search, testUSPrefix, "loco.region", "app.loco.io/component", "1.5"} {
		var found bool
		for _, got := range parameters {
			found = found || got == value
		}
		if !found {
			t.Errorf("missing parameter %q: %v", value, parameters)
		}
	}
}

func mustParse(t *testing.T, query string) logquery.Expr {
	t.Helper()
	expr, err := logquery.Parse(query)
	if err != nil {
		t.Fatal(err)
	}
	return expr
}
