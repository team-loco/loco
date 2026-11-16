package clickhouse

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const summaryService = "summary-web"
const summaryHTTPStatus = "http.response.status_code"

func TestTraceSummaries(t *testing.T) {
	f, base := newTraceSummaryFixture(t)
	start := base.GetStartTime().AsTime()
	result := queryTraceSummaries(t, f, base, TraceFilter{}, testFullPage)
	if result.GetTotalCount() != 3 || len(result.GetSummaries()) != 3 {
		t.Fatalf("summary result: %v", result)
	}
	root := result.GetSummaries()[2]
	if root.GetTraceId() != fmt.Sprintf("%032x", 1) || root.GetRootSpanName() != "GET /cart" ||
		root.GetServiceName() != summaryService || root.GetResourceId() != testResourceID ||
		!root.GetStartTime().AsTime().Equal(start) || root.GetDurationNanos() != uint64(4*time.Second) ||
		root.GetStatus() != observabilityv1.TraceStatus_TRACE_STATUS_ERROR || root.GetSpanCount() != 2 ||
		root.GetHttpStatusCode() != 500 {
		t.Fatalf("incorrect summary: %v", root)
	}
	for _, tc := range []struct {
		name   string
		query  string
		change func(*observabilityv1.ListTracesRequest)
		want   []int
	}{
		{name: "minimum duration", query: "@duration:>=4000", want: []int{1}},
		{name: "maximum duration", query: "@duration:<=1000", want: []int{2, 3}},
		{name: "duration range", query: "@duration:900..1100", want: []int{2, 3}},
		{name: "error in child", query: "@status:error", want: []int{1}},
		{name: "ok", query: "@status:OK", want: []int{2, 3}},
		{name: "root name words", query: "get /CART", want: []int{1}},
		{name: "child name excluded", query: "child"},
		{name: "trace id", query: "@trace:" + strings.ToUpper(fmt.Sprintf("%032x", 1)), want: []int{1}},
		{name: "attribute on a child span", query: "@user.id:42", want: []int{1}},
		{name: "attribute absent from every span", query: "-@user.id:42", want: []int{2, 3}},
		{
			name:  "attribute outside the resource scope",
			query: "@user.id:42",
			change: func(r *observabilityv1.ListTracesRequest) {
				r.Scope.ResourceIds = []string{testResourceID}
			},
		},
		{name: "attribute or", query: "@user.id:42 OR @user.id:7", want: []int{2, 1}},
		{name: "attribute prefix", query: "@http.route:/ca*", want: []int{1}},
		{name: "attribute range", query: "@http.response.status_code:500..599", want: []int{1}},
		{name: "attribute and status", query: "@http.response.status_code:>=200 -@status:error", want: []int{2}},
		{name: "resource attribute fallback", query: "@team:shared", want: []int{2, 1}},
		{name: "resource of any span", query: "@resource:WORK*", want: []int{1}},
		{name: "unknown resource", query: "@resource:missing"},
		{name: "groups", query: "(@status:error OR @duration:<1500) -post", want: []int{2, 1}},
		{name: "duration descending", change: func(r *observabilityv1.ListTracesRequest) {
			r.Sort = observabilityv1.TraceSummarySort_TRACE_SUMMARY_SORT_DURATION
		}, want: []int{1, 2, 3}},
		{name: "duration ascending", change: func(r *observabilityv1.ListTracesRequest) {
			r.Sort = observabilityv1.TraceSummarySort_TRACE_SUMMARY_SORT_DURATION
			r.Ascending = true
		}, want: []int{2, 3, 1}},
		{name: "start ascending", change: func(r *observabilityv1.ListTracesRequest) {
			r.Ascending = true
		}, want: []int{1, 2, 3}},
		{name: "empty workspace", change: func(r *observabilityv1.ListTracesRequest) {
			r.Scope.WorkspaceId = testUnknownWorkspaceID
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := proto.CloneOf(base)
			if tc.change != nil {
				tc.change(req)
			}
			got := queryTraceSummaries(t, f, req, traceFilter(t, tc.query), testFullPage)
			ids := make([]int, 0, len(got.GetSummaries()))
			for _, summary := range got.GetSummaries() {
				var id int
				if _, err := fmt.Sscanf(summary.GetTraceId(), "%x", &id); err != nil {
					t.Fatal(err)
				}
				ids = append(ids, id)
			}
			if !slices.Equal(ids, tc.want) || got.GetTotalCount() != uint64(len(tc.want)) {
				t.Fatalf("got %v count %d, want %v", ids, got.GetTotalCount(), tc.want)
			}
		})
	}
}

func TestTraceSummarySortColumns(t *testing.T) {
	f, base := newTraceSummaryFixture(t)
	for _, tc := range []struct {
		sort                  observabilityv1.TraceSummarySort
		ascending, descending []int
	}{
		{observabilityv1.TraceSummarySort_TRACE_SUMMARY_SORT_START_TIME, []int{1, 2, 3}, []int{2, 3, 1}},
		{observabilityv1.TraceSummarySort_TRACE_SUMMARY_SORT_DURATION, []int{2, 3, 1}, []int{1, 2, 3}},
		{observabilityv1.TraceSummarySort_TRACE_SUMMARY_SORT_OPERATION, []int{1, 2, 3}, []int{3, 2, 1}},
		{observabilityv1.TraceSummarySort_TRACE_SUMMARY_SORT_RESOURCE, []int{3, 1, 2}, []int{1, 2, 3}},
		{observabilityv1.TraceSummarySort_TRACE_SUMMARY_SORT_STATUS, []int{3, 2, 1}, []int{1, 2, 3}},
		{observabilityv1.TraceSummarySort_TRACE_SUMMARY_SORT_SPAN_COUNT, []int{2, 3, 1}, []int{1, 2, 3}},
	} {
		for _, ascending := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", tc.sort, ascending), func(t *testing.T) {
				req := proto.CloneOf(base)
				req.Sort, req.Ascending = tc.sort, ascending
				want := tc.descending
				if ascending {
					want = tc.ascending
				}
				for offset, id := range want {
					req.Offset = int32(offset)
					filter := TraceFilter{Resources: []ResourceName{{ID: testResourceID, Name: "aaa"}}}
					result := queryTraceSummaries(t, f, req, filter, 1)
					if len(result.GetSummaries()) != 1 ||
						result.GetSummaries()[0].GetTraceId() != fmt.Sprintf("%032x", id) {
						t.Fatalf("offset %d: %v, want trace %d", offset, result, id)
					}
					if id == 2 && result.GetSummaries()[0].GetHttpStatusCode() != 200 {
						t.Fatalf("legacy HTTP code: %v", result.GetSummaries()[0])
					}
					if id == 3 && result.GetSummaries()[0].GetHttpStatusCode() != 0 {
						t.Fatalf("missing HTTP code: %v", result.GetSummaries()[0])
					}
				}
			})
		}
	}
}

// traceFilter parses query, naming the worker resource "worker".
func traceFilter(t *testing.T, query string) TraceFilter {
	t.Helper()
	expr := mustParse(t, query)
	if err := ValidateTraceFilter(expr); err != nil {
		t.Fatal(err)
	}
	return TraceFilter{Query: expr, Resources: []ResourceName{{ID: testWorkerResourceID, Name: "worker"}}}
}

func TestTraceSummaryPages(t *testing.T) {
	f, base := newTraceSummaryFixture(t)
	req := proto.CloneOf(base)
	var ids []string
	for offset := range 4 {
		req.Offset = int32(offset)
		got := queryTraceSummaries(t, f, req, TraceFilter{}, 1)
		if got.GetTotalCount() != 3 {
			t.Fatalf("count: %v", got)
		}
		if offset == 3 {
			if len(got.GetSummaries()) != 0 {
				t.Fatal("expected empty page past end")
			}
			continue
		}
		if len(got.GetSummaries()) != 1 {
			t.Fatalf("page: %v", got)
		}
		ids = append(ids, got.GetSummaries()[0].GetTraceId())
		if offset == 2 && got.GetSummaries()[0].GetSpanCount() != 2 {
			t.Fatal("split trace")
		}
	}
	want := []string{fmt.Sprintf("%032x", 2), fmt.Sprintf("%032x", 3), fmt.Sprintf("%032x", 1)}
	if !slices.Equal(ids, want) {
		t.Fatalf("trace pages = %v, want %v", ids, want)
	}
}

func TestTraceSummaryTenancy(t *testing.T) {
	f, base := newTraceSummaryFixture(t)
	req := proto.CloneOf(base)
	req.Scope.ResourceIds = []string{testWorkerResourceID}
	got := queryTraceSummaries(t, f, req, TraceFilter{}, testFullPage).GetSummaries()
	if len(got) != 1 || got[0].GetRootSpanName() != "child operation" || got[0].GetSpanCount() != 1 ||
		got[0].GetDurationNanos() != uint64(3*time.Second) {
		t.Fatalf("resource scope: %v", got)
	}
	req.Scope.ResourceIds = nil
	req.Scope.WorkspaceId = testOtherWorkspaceID
	got = queryTraceSummaries(t, f, req, TraceFilter{}, testFullPage).GetSummaries()
	if len(got) != 1 || got[0].GetRootSpanName() != "foreign workspace" || got[0].GetSpanCount() != 1 {
		t.Fatalf("workspace scope: %v", got)
	}
	req.Scope.WorkspaceId = testWorkspaceID
	req.Scope.EnvironmentId = testOtherEnvironmentID
	got = queryTraceSummaries(t, f, req, TraceFilter{}, testFullPage).GetSummaries()
	if len(got) != 1 || got[0].GetRootSpanName() != "separate environment" || got[0].GetSpanCount() != 1 {
		t.Fatalf("environment scope: %v", got)
	}
}

func newTraceSummaryFixture(t *testing.T) (*queryFixture, *observabilityv1.ListTracesRequest) {
	t.Helper()
	f := newQueryFixture(t)
	start := f.start.Add(time.Minute)
	batch, err := f.conn.PrepareBatch(t.Context(), "INSERT INTO otel_traces "+
		"(Timestamp, TraceId, SpanId, ParentSpanId, SpanName, ServiceName, Duration, StatusCode, ResourceAttributes, "+
		"SpanAttributes)")
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		trace                                                                 int
		span, parent, name, service, status, workspace, environment, resource string
		offset, duration                                                      time.Duration
		attributes                                                            map[string]string
	}{
		{
			1,
			"child",
			"root",
			"child operation",
			"worker",
			"Error",
			testWorkspaceID,
			testEnvironmentID,
			testWorkerResourceID,
			time.Second,
			3 * time.Second,
			map[string]string{"user.id": "42", "http.route": "/cart", summaryHTTPStatus: "404"},
		},
		{
			1,
			"root",
			"",
			"GET /cart",
			summaryService,
			"",
			testWorkspaceID,
			testEnvironmentID,
			testResourceID,
			0,
			2 * time.Second,
			map[string]string{summaryHTTPStatus: "500"},
		},
		{
			2,
			"second",
			"",
			"GET /checkout",
			summaryService,
			"",
			testWorkspaceID,
			testEnvironmentID,
			testResourceID,
			time.Second,
			time.Second,
			map[string]string{"user.id": "7", "http.status_code": "200"},
		},
		{
			3,
			"third",
			"",
			"POST /cart",
			"resource-" + testResourceID,
			"",
			testWorkspaceID,
			testEnvironmentID,
			testResourceID,
			time.Second,
			time.Second,
			map[string]string{testSharedLabelKey: "solo"},
		},
		{
			1,
			"foreign-summary",
			"",
			"foreign workspace",
			"foreign-summary",
			"",
			testOtherWorkspaceID,
			testEnvironmentID,
			testResourceID,
			0,
			time.Hour,
			nil,
		},
		{
			1,
			"other-env",
			"",
			"separate environment",
			"other",
			"",
			testWorkspaceID,
			testOtherEnvironmentID,
			testResourceID,
			0,
			time.Hour,
			nil,
		},
	}
	for _, row := range rows {
		if err := batch.Append(
			start.Add(row.offset),
			fmt.Sprintf("%032x", row.trace),
			row.span,
			row.parent,
			row.name,
			row.service,
			uint64(row.duration),
			row.status,
			tenantAttributes(row.workspace, row.environment, row.resource),
			row.attributes,
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}
	base := &observabilityv1.ListTracesRequest{
		Scope:     &observabilityv1.TraceScope{WorkspaceId: testWorkspaceID},
		StartTime: timestamppb.New(start),
		EndTime:   timestamppb.New(start.Add(5 * time.Second)),
	}
	base.Scope.EnvironmentId = testEnvironmentID
	return f, base
}

func queryTraceSummaries(
	t *testing.T,
	f *queryFixture,
	req *observabilityv1.ListTracesRequest,
	filter TraceFilter,
	limit int32,
) *observabilityv1.ListTracesResponse {
	t.Helper()
	result, err := f.client.ListTraces(t.Context(), req, filter, limit)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
