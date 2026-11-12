package ingest

import (
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/ptrace"
	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
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
			req := &observabilityv1.QueryTracesRequest{
				WorkspaceId: testWorkspaceID, StartTime: timestamppb.New(now.Add(-testWindow)),
				EndTime: timestamppb.New(now.Add(testWindow)),
			}
			spans, err := reader.QueryTraces(t.Context(), req, testQueryLimit)
			if err != nil {
				t.Fatal(err)
			}
			if len(spans.GetSpans()) != 1 || spans.GetSpans()[0].GetStatusCode() != tc.stored.String() {
				t.Fatalf("stored status: %v", spans)
			}
			req.Summary = &observabilityv1.TraceSummaryQuery{Status: tc.summary}
			summaries, err := reader.QueryTraces(t.Context(), req, testQueryLimit)
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
			req.Summary.Status = other
			summaries, err = reader.QueryTraces(t.Context(), req, testQueryLimit)
			if err != nil {
				t.Fatal(err)
			}
			if summaries.GetTotalCount() != 0 || len(summaries.GetSummaries()) != 0 {
				t.Fatalf("status filter: %v", summaries)
			}
		})
	}
}
