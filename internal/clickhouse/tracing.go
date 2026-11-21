package clickhouse

import (
	"context"
	"errors"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/team-loco/loco/internal/clickhouse"

// query runs a SELECT inside a client span named summary, which ends when the rows close.
func (c *Client) query(ctx context.Context, summary, query string, args ...any) (driver.Rows, error) {
	ctx, span := startQuery(ctx, summary, query)
	rows, err := c.conn.Query(ctx, query, args...)
	if err != nil {
		endQuery(span, err)
		return nil, err
	}
	return &tracedRows{Rows: rows, span: span}, nil
}

// scanRow runs a single-row SELECT inside a client span named summary and scans it into dest.
func (c *Client) scanRow(ctx context.Context, summary, query string, args []any, dest ...any) error {
	ctx, span := startQuery(ctx, summary, query)
	err := c.conn.QueryRow(ctx, query, args...).Scan(dest...)
	endQuery(span, err)
	return err
}

// startQuery opens the client span and hands its context to ClickHouse, whose server spans then join the trace.
func startQuery(ctx context.Context, summary, query string) (context.Context, trace.Span) {
	ctx, span := otel.Tracer(tracerName).Start(ctx, summary, //nolint:spancheck
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			semconv.DBSystemNameClickHouse,
			semconv.DBQuerySummary(summary),
			semconv.DBQueryText(query),
		),
	)
	return clickhouse.Context(ctx, clickhouse.WithSpan(span.SpanContext())), span //nolint:spancheck
}

func endQuery(span trace.Span, err error) {
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
	span.End()
}

type tracedRows struct {
	driver.Rows
	span trace.Span
}

func (r *tracedRows) Close() error {
	err := r.Rows.Close()
	if r.span != nil {
		endQuery(r.span, errors.Join(r.Err(), err))
		r.span = nil
	}
	return err
}
