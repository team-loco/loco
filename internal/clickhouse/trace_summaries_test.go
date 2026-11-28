package clickhouse

import (
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const summaryService = "summary-web"
const summaryHTTPStatus = "http.response.status_code"
const summaryRootSpan = "root"

func TestTraceSummaryPageIncludesLateRootAndChildren(t *testing.T) {
	f := newQueryFixture(t)
	req := &observabilityv1.ListTracesRequest{
		Scope:     &observabilityv1.TraceScope{WorkspaceId: testWorkspaceID},
		StartTime: timestamppb.New(f.start), EndTime: timestamppb.New(f.end), IncludeTotalCount: true,
	}
	insert := func(trace int, span, parent, name string, offset time.Duration) {
		t.Helper()
		if err := f.conn.Exec(t.Context(), "INSERT INTO otel_traces "+
			"(Timestamp, TraceId, SpanId, ParentSpanId, SpanName, Duration, ResourceAttributes) VALUES (?, ?, ?, ?, ?, ?, ?)",
			f.start.Add(offset), fmt.Sprintf("%032x", trace), span, parent, name, uint64(time.Second),
			tenantAttributes(testWorkspaceID, testEnvironmentID, testResourceID)); err != nil {
			t.Fatal(err)
		}
	}
	insert(1, "child", summaryRootSpan, "child", time.Second)
	insert(2, "other", "", "other trace", 2*time.Second)
	first := queryTraceSummaries(t, f, req, TraceFilter{}, 1)
	if first.GetSummaries()[0].GetTraceId() != fmt.Sprintf("%032x", 2) {
		t.Fatalf("first page: %v", first)
	}
	// The unseen trace receives a root before the page boundary and a child after it.
	// Seeking over raw spans would silently remove that child from its summary.
	insert(1, summaryRootSpan, "", "GET /late-root", 0)
	insert(1, "later-child", summaryRootSpan, "later child", 3*time.Second)
	req.PageToken = first.GetNextPageToken()
	second := queryTraceSummaries(t, f, req, TraceFilter{}, 1)
	if len(second.GetSummaries()) != 1 {
		t.Fatalf("second page: %v", second)
	}
	got := second.GetSummaries()[0]
	complete := got.GetRootSpanName() == "GET /late-root" && got.GetSpanCount() == 3 &&
		got.GetDurationNanos() == uint64(4*time.Second)
	if !complete {
		t.Fatalf("late spans missing from summary: %v", got)
	}
}

func TestTraceSummaryPagesAfterLateArrival(t *testing.T) {
	for _, order := range []observabilityv1.TraceOrder{
		observabilityv1.TraceOrder_TRACE_ORDER_START_TIME,
		observabilityv1.TraceOrder_TRACE_ORDER_DURATION,
		observabilityv1.TraceOrder_TRACE_ORDER_OPERATION,
		observabilityv1.TraceOrder_TRACE_ORDER_RESOURCE,
		observabilityv1.TraceOrder_TRACE_ORDER_STATUS,
		observabilityv1.TraceOrder_TRACE_ORDER_SPAN_COUNT,
	} {
		for _, descending := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", order, descending), func(t *testing.T) {
				f := newQueryFixture(t)
				req := &observabilityv1.ListTracesRequest{
					Scope:     &observabilityv1.TraceScope{WorkspaceId: testWorkspaceID},
					StartTime: timestamppb.New(f.start), EndTime: timestamppb.New(f.end), IncludeTotalCount: true,
				}
				insert := func(id int) {
					t.Helper()
					if err := f.conn.Exec(
						t.Context(),
						"INSERT INTO otel_traces "+
							"(Timestamp, TraceId, SpanId, SpanName, ServiceName, ResourceAttributes) VALUES (?, ?, ?, ?, ?, ?)",
						f.start.Add(
							time.Minute,
						),
						fmt.Sprintf("%032x", id),
						summaryRootSpan,
						"GET /cart",
						summaryService,
						tenantAttributes(testWorkspaceID, testEnvironmentID, testResourceID),
					); err != nil {
						t.Fatal(err)
					}
				}
				for _, id := range []int{32, 48, 64} {
					insert(id)
				}
				req.OrderBy, req.Descending = order, descending
				filter := TraceFilter{Resources: []ResourceName{{ID: testResourceID, Name: "aaa"}}}
				original := queryTraceSummaries(t, f, req, filter, testFullPage).GetSummaries()
				first := queryTraceSummaries(t, f, req, filter, 1)
				boundary := first.GetSummaries()[0]
				// This new trace ties the boundary's primary sort value but its lower ID puts it
				// before the first page, in either direction. It must not shift the next page.
				insert(16)
				req.PageToken = first.GetNextPageToken()
				second := queryTraceSummaries(t, f, req, filter, testFullPage)
				got := make([]string, 0, len(second.GetSummaries()))
				want := make([]string, 0, len(original)-1)
				for _, summary := range second.GetSummaries() {
					got = append(got, summary.GetTraceId())
				}
				for _, summary := range original[1:] {
					want = append(want, summary.GetTraceId())
				}
				if !slices.Equal(got, want) || second.GetTotalCount() != 4 {
					t.Fatalf("after late arrival: got %v count %d, want %v count 4", got, second.GetTotalCount(), want)
				}
				t.Logf("order=%s descending=%t page1=%s page2=%v total=%d", order, descending,
					boundary.GetTraceId(), got, second.GetTotalCount())
			})
		}
	}
}

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
			r.OrderBy, r.Descending = observabilityv1.TraceOrder_TRACE_ORDER_DURATION, true
		}, want: []int{1, 2, 3}},
		{name: "duration ascending", change: func(r *observabilityv1.ListTracesRequest) {
			r.OrderBy = observabilityv1.TraceOrder_TRACE_ORDER_DURATION
		}, want: []int{2, 3, 1}},
		{name: "start ascending", change: func(r *observabilityv1.ListTracesRequest) {
			r.OrderBy = observabilityv1.TraceOrder_TRACE_ORDER_START_TIME
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
		order                 observabilityv1.TraceOrder
		ascending, descending []int
	}{
		{observabilityv1.TraceOrder_TRACE_ORDER_START_TIME, []int{1, 2, 3}, []int{2, 3, 1}},
		{observabilityv1.TraceOrder_TRACE_ORDER_DURATION, []int{2, 3, 1}, []int{1, 2, 3}},
		{observabilityv1.TraceOrder_TRACE_ORDER_OPERATION, []int{1, 2, 3}, []int{3, 2, 1}},
		{observabilityv1.TraceOrder_TRACE_ORDER_RESOURCE, []int{3, 1, 2}, []int{1, 2, 3}},
		{observabilityv1.TraceOrder_TRACE_ORDER_STATUS, []int{3, 2, 1}, []int{1, 2, 3}},
		{observabilityv1.TraceOrder_TRACE_ORDER_SPAN_COUNT, []int{2, 3, 1}, []int{1, 2, 3}},
	} {
		for _, descending := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", tc.order, descending), func(t *testing.T) {
				req := proto.CloneOf(base)
				req.OrderBy, req.Descending = tc.order, descending
				want := tc.ascending
				if descending {
					want = tc.descending
				}
				for page, id := range want {
					filter := TraceFilter{Resources: []ResourceName{{ID: testResourceID, Name: "aaa"}}}
					result := queryTraceSummaries(t, f, req, filter, 1)
					req.PageToken = result.GetNextPageToken()
					if len(result.GetSummaries()) != 1 ||
						result.GetSummaries()[0].GetTraceId() != fmt.Sprintf("%032x", id) {
						t.Fatalf("page %d: %v, want trace %d", page, result, id)
					}
					if id == 2 && result.GetSummaries()[0].GetHttpStatusCode() != 200 {
						t.Fatalf("HTTP code: %v", result.GetSummaries()[0])
					}
					if id == 3 && result.GetSummaries()[0].GetHttpStatusCode() != 0 {
						t.Fatalf("missing HTTP code: %v", result.GetSummaries()[0])
					}
				}
			})
		}
	}
}

func TestTraceSummaryLegacyHTTPCode(t *testing.T) {
	f, req := newTraceSummaryFixture(t)
	id := fmt.Sprintf("%032x", 4)
	err := f.conn.Exec(t.Context(), "INSERT INTO otel_traces "+
		"(Timestamp, TraceId, SpanId, ResourceAttributes, SpanAttributes) VALUES (?, ?, ?, ?, ?)",
		req.GetStartTime().AsTime(), id, "legacy", tenantAttributes(testWorkspaceID, testEnvironmentID, testResourceID),
		map[string]string{"http.status_code": "201"})
	if err != nil {
		t.Fatal(err)
	}
	for _, summary := range queryTraceSummaries(t, f, req, TraceFilter{}, testFullPage).GetSummaries() {
		if summary.GetTraceId() == id {
			if summary.GetHttpStatusCode() != 201 {
				t.Fatalf("legacy HTTP code: %v", summary)
			}
			return
		}
	}
	t.Fatal("legacy trace missing")
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
	want := []string{fmt.Sprintf("%032x", 2), fmt.Sprintf("%032x", 3), fmt.Sprintf("%032x", 1)}
	for _, counted := range []bool{false, true} {
		t.Run(fmt.Sprintf("counted=%t", counted), func(t *testing.T) {
			req := proto.CloneOf(base)
			req.IncludeTotalCount = counted
			var wantTotal uint64
			if counted {
				wantTotal = 3
			}
			var ids []string
			for page := 0; ; page++ {
				got := queryTraceSummaries(t, f, req, TraceFilter{}, 1)
				if got.GetTotalCount() != wantTotal {
					t.Fatalf("page %d count: %v", page, got)
				}
				if len(got.GetSummaries()) != 1 {
					t.Fatalf("page %d: %v", page, got)
				}
				ids = append(ids, got.GetSummaries()[0].GetTraceId())
				if page == 2 && got.GetSummaries()[0].GetSpanCount() != 2 {
					t.Fatal("split trace")
				}
				if got.GetNextPageToken() == "" {
					break
				}
				req.PageToken = got.GetNextPageToken()
			}
			if !slices.Equal(ids, want) {
				t.Fatalf("trace pages = %v, want %v", ids, want)
			}
		})
	}
	req := proto.CloneOf(base)
	last := queryTraceSummaries(t, f, req, TraceFilter{}, testFullPage).GetSummaries()[2]
	req.PageToken = encodeTraceCursor(
		req,
		last.GetTraceId(),
		strconv.FormatInt(last.GetStartTime().AsTime().UnixNano(), 10),
	)
	got := queryTraceSummaries(t, f, req, TraceFilter{}, 1)
	if len(got.GetSummaries()) != 0 || got.GetTotalCount() != 3 {
		t.Fatalf("page past the end: %v", got)
	}
	durationOrder := proto.CloneOf(base)
	durationOrder.OrderBy = observabilityv1.TraceOrder_TRACE_ORDER_DURATION
	reversed := proto.CloneOf(base)
	reversed.Descending = true
	for _, tc := range []struct {
		name, token string
		order       observabilityv1.TraceOrder
	}{
		{name: "bad base64", token: "not base64!"},
		{name: "missing boundary", token: "LTE"},
		{name: "invalid trace ID", token: base64.RawURLEncoding.EncodeToString([]byte("0:false:bad:3"))},
		{name: "invalid time", token: encodeTraceCursor(base, last.GetTraceId(), "not a time")},
		{name: "different sort", token: encodeTraceCursor(
			durationOrder, last.GetTraceId(), strconv.FormatUint(last.GetDurationNanos(), 10),
		)},
		{name: "different direction", token: encodeTraceCursor(
			reversed, last.GetTraceId(), strconv.FormatInt(last.GetStartTime().AsTime().UnixNano(), 10),
		)},
		{name: "negative unsigned value", token: encodeTraceCursor(durationOrder, last.GetTraceId(), "-1"),
			order: durationOrder.GetOrderBy()},
		{name: "overflowing unsigned value",
			token: encodeTraceCursor(durationOrder, last.GetTraceId(), "18446744073709551616"),
			order: durationOrder.GetOrderBy()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := proto.CloneOf(base)
			req.OrderBy, req.PageToken = tc.order, tc.token
			_, err := f.client.ListTraces(t.Context(), req, TraceFilter{}, 1)
			if !errors.Is(err, ErrInvalidTracePageToken) {
				t.Errorf("token %q: err = %v, want ErrInvalidTracePageToken", tc.token, err)
			}
		})
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
			summaryRootSpan,
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
			summaryRootSpan,
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
			map[string]string{"user.id": "7", summaryHTTPStatus: "200"},
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

		IncludeTotalCount: true,
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
