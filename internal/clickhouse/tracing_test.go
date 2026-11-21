package clickhouse

import (
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

func recordSpans(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder)))
	t.Cleanup(func() { otel.SetTracerProvider(previous) })
	return recorder
}

func endedSpan(t *testing.T, recorder *tracetest.SpanRecorder, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, span := range recorder.Ended() {
		if span.Name() == name {
			return span
		}
	}
	t.Fatalf("no ended span %q", name)
	return nil
}

func TestQueriesRecordClientSpansUnderTheCaller(t *testing.T) {
	fixture := newQueryFixture(t)
	recorder := recordSpans(t)
	ctx, parent := otel.Tracer("test").Start(t.Context(), "request")
	_, err := fixture.client.QueryLogs(ctx, LogQuery{
		WorkspaceID: testWorkspaceID,
		Start:       fixture.start,
		End:         fixture.end,
		Limit:       testPageSize,
	})
	parent.End()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"QueryLogs", "CountLogs"} {
		span := endedSpan(t, recorder, name)
		if span.Parent().SpanID() != parent.SpanContext().SpanID() {
			t.Errorf("%s parent = %s, want %s", name, span.Parent().SpanID(), parent.SpanContext().SpanID())
		}
		if span.SpanKind() != trace.SpanKindClient {
			t.Errorf("%s kind = %s, want client", name, span.SpanKind())
		}
		attributes := map[string]string{}
		for _, attribute := range span.Attributes() {
			attributes[string(attribute.Key)] = attribute.Value.String()
		}
		if attributes[string(semconv.DBSystemNameKey)] != "clickhouse" {
			t.Errorf("%s attributes = %v, want db.system.name clickhouse", name, attributes)
		}
	}
}

func TestQuerySpanLastsUntilRowsCloseAndRecordsFailures(t *testing.T) {
	fixture := newQueryFixture(t)
	recorder := recordSpans(t)
	rows, err := fixture.client.query(t.Context(), "Sleep", "SELECT sleep(0.2)")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
	}
	if len(recorder.Ended()) != 0 {
		t.Fatal("span ended before the rows closed")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if span := endedSpan(t, recorder, "Sleep"); span.EndTime().Sub(span.StartTime()) < 200*time.Millisecond {
		t.Errorf("span lasted %s, want at least the query's 200ms", span.EndTime().Sub(span.StartTime()))
	}

	var value uint8
	if err := fixture.client.scanRow(t.Context(), "Broken", "SELECT missing_column", nil, &value); err == nil {
		t.Fatal("broken query succeeded")
	}
	if span := endedSpan(t, recorder, "Broken"); span.Status().Code != codes.Error {
		t.Errorf("broken query span status = %v, want error", span.Status())
	}
}
