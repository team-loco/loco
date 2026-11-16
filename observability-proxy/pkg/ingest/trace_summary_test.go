package ingest

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace"
	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/internal/clickhouse"
	"github.com/team-loco/loco/internal/logquery"
)

func TestIngestedTraceSummaryStatus(t *testing.T) {
	for _, tc := range []struct {
		stored  ptrace.StatusCode
		summary observabilityv1.TraceStatus
	}{
		{ptrace.StatusCodeUnset, observabilityv1.TraceStatus_TRACE_STATUS_OK},
		{ptrace.StatusCodeOk, observabilityv1.TraceStatus_TRACE_STATUS_OK},
		{ptrace.StatusCodeError, observabilityv1.TraceStatus_TRACE_STATUS_ERROR},
	} {
		t.Run(tc.stored.String(), func(t *testing.T) {
			f := newIngestFixture(t)
			writer := f.startedWriter(t)
			now := time.Now().UTC().Truncate(time.Second)
			traces := testTraces(now)
			span := traces.ResourceSpans().At(0).ScopeSpans().At(0).Spans().At(0)
			span.Status().SetCode(tc.stored)
			if err := writer.Consumers().Traces.ConsumeTraces(t.Context(), traces); err != nil {
				t.Fatal(err)
			}
			reader := f.reader(t)
			req := &observabilityv1.ListTracesRequest{
				Scope: &observabilityv1.TraceScope{
					WorkspaceId: testWorkspaceID,
				},
				StartTime: timestamppb.New(now.Add(-testWindow)),
				EndTime:   timestamppb.New(now.Add(testWindow)),
			}
			spans, err := reader.GetTrace(
				t.Context(),
				&observabilityv1.GetTraceRequest{Scope: req.GetScope(), TraceId: testTraceID.String()},
			)
			if err != nil {
				t.Fatal(err)
			}
			if len(spans.GetSpans()) != 1 || spans.GetSpans()[0].GetStatusCode() != tc.stored.String() {
				t.Fatalf("stored status: %v", spans)
			}
			summaries, err := reader.ListTraces(t.Context(), req, statusFilter(tc.summary), testQueryLimit)
			if err != nil {
				t.Fatal(err)
			}
			if summaries.GetTotalCount() != 1 || len(summaries.GetSummaries()) != 1 ||
				summaries.GetSummaries()[0].GetStatus() != tc.summary {
				t.Fatalf("summary status: %v", summaries)
			}
			other := observabilityv1.TraceStatus_TRACE_STATUS_ERROR
			if tc.summary == other {
				other = observabilityv1.TraceStatus_TRACE_STATUS_OK
			}
			summaries, err = reader.ListTraces(t.Context(), req, statusFilter(other), testQueryLimit)
			if err != nil {
				t.Fatal(err)
			}
			if summaries.GetTotalCount() != 0 || len(summaries.GetSummaries()) != 0 {
				t.Fatalf("status filter: %v", summaries)
			}
		})
	}
}

// statusFilter is the trace query @status:<name> for an ok or error summary status.
func statusFilter(status observabilityv1.TraceStatus) clickhouse.TraceFilter {
	name := "ok"
	if status == observabilityv1.TraceStatus_TRACE_STATUS_ERROR {
		name = "error"
	}
	return clickhouse.TraceFilter{Query: logquery.Field{Key: clickhouse.StatusField, Value: name}}
}
