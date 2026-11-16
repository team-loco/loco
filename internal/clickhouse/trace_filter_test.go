package clickhouse

import (
	"strings"
	"testing"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/internal/logquery"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func FuzzTraceSummaryQueryBindsEveryValue(f *testing.F) {
	for _, seed := range []string{
		`@http.route:"/a:b" OR -@status:error`, "@duration:100..250 (@resource:ap* OR checkout)",
		`"'; DROP TABLE otel_traces; --"`, "@trace:ABC* -@user.id:42 @team:shared",
		`@user.name:"o'brien" @roles[0]:admin @http.response.status_code:>=500`,
	} {
		f.Add(seed)
	}
	req := &observabilityv1.ListTracesRequest{
		Scope: &observabilityv1.TraceScope{
			WorkspaceId:   testWorkspaceID,
			EnvironmentId: testEnvironmentID,
			ResourceIds:   []string{testResourceID},
		},
		StartTime: timestamppb.New(time.Unix(0, 0)),
		EndTime:   timestamppb.New(time.Unix(60, 0)),
	}
	resources := []ResourceName{{ID: testResourceID, Name: "api"}}
	f.Fuzz(func(t *testing.T, query string) {
		expr, err := logquery.Parse(query)
		if err != nil || ValidateTraceFilter(expr) != nil {
			return
		}
		b := &positionalBinder{}
		sql := buildTraceSummaryQuery(req, TraceFilter{Query: expr, Resources: resources}, b)
		if placeholders := strings.Count(sql, "?"); placeholders != len(b.args) {
			t.Fatalf("%q compiled to %d placeholders for %d arguments: %s", query, placeholders, len(b.args), sql)
		}
		if strings.ContainsAny(strings.ReplaceAll(sql, "ParentSpanId != ''", ""), "'\"`\\;") {
			t.Fatalf("%q compiled to SQL with a literal: %s", query, sql)
		}
	})
}

func TestValidateTraceFilter(t *testing.T) {
	for query, want := range map[string]string{
		"checkout @status:ERROR @duration:>100 @duration:1..2 @trace:ab* @resource:web @loco.io/build-id:b": "",
		"@http.response.status_code:>=500 @level:1..2 @region:x":                                            "",
		"a (@loco.workspace.id:w)": "@loco.workspace.id is reserved; use @resource at position 4",
		"@status:failed":           "@status is ok or error at position 1",
		"-@status:err*":            "@status is ok or error at position 2",
		"@status:>1":               "@status is ok or error at position 1",
		"@duration:100":            "@duration compares milliseconds, as in >100 or 100..500 at position 1",
		"@duration:1*":             "@duration compares milliseconds, as in >100 or 100..500 at position 1",
		"@trace:>1":                "@trace does not compare numbers at position 1",
		"x OR @resource:1..2":      "@resource does not compare numbers at position 6",
	} {
		err := ValidateTraceFilter(mustParse(t, query))
		if (want == "" && err != nil) || (want != "" && (err == nil || err.Error() != want)) {
			t.Errorf("ValidateTraceFilter(%q) = %v, want %q", query, err, want)
		}
	}
}
