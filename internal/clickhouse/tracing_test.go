package clickhouse

import (
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
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
		if got := recordedAttributes(span)[semconv.DBSystemNameKey].AsString(); got != "clickhouse" {
			t.Errorf("%s db.system.name = %q, want clickhouse", name, got)
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
	span := endedSpan(t, recorder, "Sleep")
	if span.EndTime().Sub(span.StartTime()) < 200*time.Millisecond {
		t.Errorf("span lasted %s, want at least the query's 200ms", span.EndTime().Sub(span.StartTime()))
	}
	attributes := recordedAttributes(span)
	if got := attributes[elapsedKey].AsFloat64(); got < 200 {
		t.Errorf("%s = %v, want at least the query's 200ms", elapsedKey, got)
	}
	if got := attributes[readRowsKey].AsInt64(); got != 1 {
		t.Errorf("%s = %d, want the 1 row of system.one", readRowsKey, got)
	}
	if got := attributes[semconv.DBResponseReturnedRowsKey].AsInt64(); got != 1 {
		t.Errorf("%s = %d, want 1", semconv.DBResponseReturnedRowsKey, got)
	}

	var value uint8
	if err := fixture.client.scanRow(t.Context(), "Broken", "SELECT missing_column", nil, &value); err == nil {
		t.Fatal("broken query succeeded")
	}
	if span := endedSpan(t, recorder, "Broken"); span.Status().Code != codes.Error {
		t.Errorf("broken query span status = %v, want error", span.Status())
	}
}

func recordedAttributes(span sdktrace.ReadOnlySpan) map[attribute.Key]attribute.Value {
	attributes := map[attribute.Key]attribute.Value{}
	for _, kv := range span.Attributes() {
		attributes[kv.Key] = kv.Value
	}
	return attributes
}

func TestQuerySpanRecordsWhatClickHouseReports(t *testing.T) {
	recorder := recordSpans(t)
	_, span := startQuery(t.Context(), "Stats", "SELECT 1")
	span.addProgress(&clickhouse.Progress{Rows: 100, Bytes: 4000, Elapsed: 3 * time.Millisecond})
	span.addProgress(&clickhouse.Progress{Rows: 20, Bytes: 500, Elapsed: 2 * time.Millisecond})
	span.setProfileInfo(&clickhouse.ProfileInfo{Rows: 7})
	span.end(nil)

	attributes := recordedAttributes(endedSpan(t, recorder, "Stats"))
	if got := attributes[readRowsKey].AsInt64(); got != 120 {
		t.Errorf("%s = %d, want 120", readRowsKey, got)
	}
	if got := attributes[readBytesKey].AsInt64(); got != 4500 {
		t.Errorf("%s = %d, want 4500", readBytesKey, got)
	}
	if got := attributes[elapsedKey].AsFloat64(); got != 5 {
		t.Errorf("%s = %v, want 5", elapsedKey, got)
	}
	if got := attributes[semconv.DBResponseReturnedRowsKey].AsInt64(); got != 7 {
		t.Errorf("%s = %d, want 7", semconv.DBResponseReturnedRowsKey, got)
	}
}
