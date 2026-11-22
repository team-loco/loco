package clickhouse

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/team-loco/loco/internal/clickhouse"

const (
	readRowsKey  = attribute.Key("clickhouse.read_rows")
	readBytesKey = attribute.Key("clickhouse.read_bytes")
	elapsedKey   = attribute.Key("clickhouse.elapsed_ms")
)

// query runs a SELECT inside a client span named summary, which ends when the rows close.
func (c *Client) query(ctx context.Context, summary, query string, args ...any) (driver.Rows, error) {
	ctx, span := startQuery(ctx, summary, query)
	rows, err := c.conn.Query(ctx, query, args...)
	if err != nil {
		span.end(err)
		return nil, err
	}
	return &tracedRows{Rows: rows, span: span}, nil
}

// scanRow runs a single-row SELECT inside a client span named summary and scans it into dest.
func (c *Client) scanRow(ctx context.Context, summary, query string, args []any, dest ...any) error {
	ctx, span := startQuery(ctx, summary, query)
	err := c.conn.QueryRow(ctx, query, args...).Scan(dest...)
	span.end(err)
	return err
}

// querySpan is a ClickHouse client span and the statistics the server reports while the query runs.
type querySpan struct {
	span         trace.Span
	mu           sync.Mutex
	progress     clickhouse.Progress
	returnedRows uint64
}

// startQuery opens the client span and hands its context to ClickHouse, whose server spans then join the trace.
func startQuery(ctx context.Context, summary, query string) (context.Context, *querySpan) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, summary, //nolint:spancheck
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			semconv.DBSystemNameClickHouse,
			semconv.DBQuerySummary(summary),
			semconv.DBQueryText(query),
		),
	)
	q := &querySpan{span: span}
	return clickhouse.Context(ctx, //nolint:spancheck
		clickhouse.WithSpan(span.SpanContext()),
		clickhouse.WithProgress(q.addProgress),
		clickhouse.WithProfileInfo(q.setProfileInfo),
	), q
}

// addProgress sums the progress packets, each of which carries the increment since the previous one.
func (q *querySpan) addProgress(p *clickhouse.Progress) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.progress.Rows += p.Rows
	q.progress.Bytes += p.Bytes
	q.progress.Elapsed += p.Elapsed
}

func (q *querySpan) setProfileInfo(p *clickhouse.ProfileInfo) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.returnedRows = p.Rows
}

func (q *querySpan) end(err error) {
	q.mu.Lock()
	q.span.SetAttributes(
		readRowsKey.Int64(int64(q.progress.Rows)),
		readBytesKey.Int64(int64(q.progress.Bytes)),
		elapsedKey.Float64(float64(q.progress.Elapsed)/float64(time.Millisecond)),
		semconv.DBResponseReturnedRows(int(q.returnedRows)),
	)
	q.mu.Unlock()
	if err != nil {
		q.span.RecordError(err)
		q.span.SetStatus(codes.Error, err.Error())
	}
	q.span.End()
}

type tracedRows struct {
	driver.Rows
	span *querySpan
}

func (r *tracedRows) Close() error {
	err := r.Rows.Close()
	if r.span != nil {
		r.span.end(errors.Join(r.Err(), err))
		r.span = nil
	}
	return err
}
