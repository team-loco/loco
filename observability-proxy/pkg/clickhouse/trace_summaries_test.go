package clickhouse

import (
	"fmt"
	"slices"
	"testing"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"google.golang.org/protobuf/proto"
)

const summaryService = "summary-web"

func TestTraceSummaries(t *testing.T) {
	f, base := newTraceSummaryFixture(t)
	start := base.GetStartTime().AsTime()
	result := queryTraceSummaries(t, f, base, testFullPage)
	if result.GetTotalCount() != 3 || len(result.GetSummaries()) != 3 {
		t.Fatalf("summary result: %v", result)
	}
	root := result.GetSummaries()[2]
	if root.GetTraceId() != fmt.Sprintf("%032x", 1) || root.GetRootSpanName() != "GET /cart" ||
		root.GetServiceName() != summaryService || root.GetResourceId() != testResourceID ||
		!root.GetStartTime().AsTime().Equal(start) || root.GetDurationNanos() != uint64(4*time.Second) ||
		root.GetStatus() != observabilityv1.TraceStatus_TRACE_STATUS_ERROR || root.GetSpanCount() != 2 {
		t.Fatalf("incorrect summary: %v", root)
	}
	for _, tc := range []struct {
		name   string
		change func(*observabilityv1.QueryTracesRequest)
		want   []int
	}{
		{"minimum duration", func(r *observabilityv1.QueryTracesRequest) {
			r.Summary.MinDurationNanos = proto.Uint64(uint64(4 * time.Second))
		}, []int{1}},
		{"maximum duration", func(r *observabilityv1.QueryTracesRequest) {
			r.Summary.MaxDurationNanos = proto.Uint64(uint64(time.Second))
		}, []int{2, 3}},
		{"error in child", func(r *observabilityv1.QueryTracesRequest) {
			r.Summary.Status = observabilityv1.TraceStatus_TRACE_STATUS_ERROR
		}, []int{1}},
		{"ok", func(r *observabilityv1.QueryTracesRequest) {
			r.Summary.Status = observabilityv1.TraceStatus_TRACE_STATUS_OK
		}, []int{2, 3}},
		{"root name terms", func(r *observabilityv1.QueryTracesRequest) {
			r.Summary.RootNameTerms = []string{"get", "/CART"}
		}, []int{1}},
		{"child name excluded", func(r *observabilityv1.QueryTracesRequest) {
			r.Summary.RootNameTerms = []string{"child"}
		}, nil},
		{"duration descending", func(r *observabilityv1.QueryTracesRequest) {
			r.Summary.Sort = observabilityv1.TraceSummarySort_TRACE_SUMMARY_SORT_DURATION
		}, []int{1, 2, 3}},
		{"duration ascending", func(r *observabilityv1.QueryTracesRequest) {
			r.Summary.Sort = observabilityv1.TraceSummarySort_TRACE_SUMMARY_SORT_DURATION
			r.Summary.Ascending = true
		}, []int{2, 3, 1}},
		{"start ascending", func(r *observabilityv1.QueryTracesRequest) { r.Summary.Ascending = true }, []int{1, 2, 3}},
		{"trace id", func(r *observabilityv1.QueryTracesRequest) { r.TraceId = fmt.Sprintf("%032x", 1) }, []int{1}},
		{"empty workspace", func(r *observabilityv1.QueryTracesRequest) { r.WorkspaceId = testUnknownWorkspaceID }, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := proto.CloneOf(base)
			tc.change(req)
			got := queryTraceSummaries(t, f, req, testFullPage)
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

func TestTraceSummaryPages(t *testing.T) {
	f, base := newTraceSummaryFixture(t)
	req := proto.CloneOf(base)
	var ids []string
	for offset := range 4 {
		req.Summary.Offset = int32(offset)
		got := queryTraceSummaries(t, f, req, 1)
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
	req.ResourceIds = []string{testWorkerResourceID}
	got := queryTraceSummaries(t, f, req, testFullPage).GetSummaries()
	if len(got) != 1 || got[0].GetRootSpanName() != "child operation" || got[0].GetSpanCount() != 1 ||
		got[0].GetDurationNanos() != uint64(3*time.Second) {
		t.Fatalf("resource scope: %v", got)
	}
	req.ResourceIds = nil
	req.WorkspaceId = testOtherWorkspaceID
	got = queryTraceSummaries(t, f, req, testFullPage).GetSummaries()
	if len(got) != 1 || got[0].GetRootSpanName() != "foreign workspace" || got[0].GetSpanCount() != 1 {
		t.Fatalf("workspace scope: %v", got)
	}
	req.WorkspaceId = testWorkspaceID
	req.EnvironmentId = testOtherEnvironmentID
	got = queryTraceSummaries(t, f, req, testFullPage).GetSummaries()
	if len(got) != 1 || got[0].GetRootSpanName() != "separate environment" || got[0].GetSpanCount() != 1 {
		t.Fatalf("environment scope: %v", got)
	}
}

func newTraceSummaryFixture(t *testing.T) (*queryFixture, *observabilityv1.QueryTracesRequest) {
	t.Helper()
	f := newQueryFixture(t)
	start := f.start.Add(time.Minute)
	batch, err := f.conn.PrepareBatch(t.Context(), "INSERT INTO otel_traces "+
		"(Timestamp, TraceId, SpanId, ParentSpanId, SpanName, ServiceName, Duration, StatusCode, ResourceAttributes)")
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		trace                                                                 int
		span, parent, name, service, status, workspace, environment, resource string
		offset, duration                                                      time.Duration
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
		},
		{
			3,
			"third",
			"",
			"POST /cart",
			summaryService,
			"",
			testWorkspaceID,
			testEnvironmentID,
			testResourceID,
			time.Second,
			time.Second,
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
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}
	base := traceRequest(start, start.Add(5*time.Second))
	base.EnvironmentId = testEnvironmentID
	base.Summary = &observabilityv1.TraceSummaryQuery{}
	return f, base
}

func queryTraceSummaries(
	t *testing.T,
	f *queryFixture,
	req *observabilityv1.QueryTracesRequest,
	limit int32,
) *observabilityv1.QueryTracesResponse {
	t.Helper()
	result, err := f.client.QueryTraces(t.Context(), req, limit)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.GetSpans()) != 0 || result.GetNextCursor() != "" {
		t.Fatal("summary returned spans or span cursor")
	}
	return result
}
