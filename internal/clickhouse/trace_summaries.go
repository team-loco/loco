package clickhouse

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	traceSortTimestamp = "Int64"
	traceSortUnsigned  = "UInt64"
	traceSortText      = "String" //nolint:usestdlibvars // ClickHouse type name, not a Go constant kind.
)

// traceRequest holds the scope and range ListTracesRequest and QueryTraceBucketsRequest share.
type traceRequest interface {
	GetScope() *observabilityv1.TraceScope
	GetStartTime() *timestamppb.Timestamp
	GetEndTime() *timestamppb.Timestamp
}

// buildTraceSummaryQuery summarizes the traces of the request's spans that match filter, binding
// every value through b.
func buildTraceSummaryQuery(req traceRequest, filter TraceFilter, b *positionalBinder) string {
	spans := func() string {
		return strings.Join(append(traceScope(req.GetScope(), b),
			"Timestamp >= fromUnixTimestamp64Nano("+b.num(req.GetStartTime().AsTime().UnixNano())+")",
			"Timestamp <= fromUnixTimestamp64Nano("+b.num(req.GetEndTime().AsTime().UnixNano())+")",
		), " AND ")
	}
	query := "SELECT TraceId, argMin(tuple(SpanName, ServiceName, ResourceId, " +
		"toUInt32OrZero(coalesce(nullIf(SpanAttributes[" + b.str("http.response.status_code") + "], " +
		b.str("") + "), SpanAttributes[" + b.str("http.status_code") + "]))), " +
		"tuple(ParentSpanId != '', Timestamp, SpanId)) AS root, min(Timestamp) AS start_time, " +
		"toUInt64(max(toInt128(toUnixTimestamp64Nano(Timestamp)) + Duration) - " +
		"toInt128(toUnixTimestamp64Nano(start_time))) AS duration_nanos, " +
		"if(countIf(StatusCode = " + b.str(ptrace.StatusCodeError.String()) + ") > 0, toInt32(" +
		b.num(int64(observabilityv1.TraceStatus_TRACE_STATUS_ERROR)) + "), toInt32(" +
		b.num(int64(observabilityv1.TraceStatus_TRACE_STATUS_OK)) + ")) AS status, " +
		"uniqExact(SpanId) AS span_count FROM otel_traces WHERE " + spans() + " GROUP BY TraceId"
	if filter.Query != nil {
		query += " HAVING " + traceFilterSQL(filter.Query, filter.Resources, spans, b)
	}
	return query
}

// ErrInvalidTracePageToken rejects a ListTraces page_token no response returned.
var ErrInvalidTracePageToken = errors.New("invalid trace page token")

func (c *Client) ListTraces(
	ctx context.Context,
	req *observabilityv1.ListTracesRequest,
	filter TraceFilter,
	limit int32,
) (*observabilityv1.ListTracesResponse, error) {
	b := &positionalBinder{}
	query := buildTraceSummaryQuery(req, filter, b)
	settings := fmt.Sprintf(" SETTINGS max_execution_time = %d", int(c.queryTimeout.Seconds()))
	summaryQuery, summaryArgs := query, slices.Clone(b.args)
	sort, sortType := "toUnixTimestamp64Nano(start_time)", traceSortTimestamp
	switch req.GetOrderBy() {
	case observabilityv1.TraceOrder_TRACE_ORDER_DURATION:
		sort, sortType = "duration_nanos", traceSortUnsigned
	case observabilityv1.TraceOrder_TRACE_ORDER_OPERATION:
		sort, sortType = "root.1", traceSortText
	case observabilityv1.TraceOrder_TRACE_ORDER_RESOURCE:
		sort, sortType = "root.2", traceSortText
		if len(filter.Resources) > 0 {
			choices := make([]string, 0, 2*len(filter.Resources)+1)
			for _, resource := range filter.Resources {
				choices = append(choices, "root.3 = "+b.str(resource.ID)+
					" AND root.2 = concat('resource-', root.3)", b.str(resource.Name))
			}
			sort = "multiIf(" + strings.Join(append(choices, sort), ", ") + ")"
		}
	case observabilityv1.TraceOrder_TRACE_ORDER_STATUS:
		sort, sortType = "root.4", traceSortUnsigned
	case observabilityv1.TraceOrder_TRACE_ORDER_SPAN_COUNT:
		sort, sortType = "span_count", traceSortUnsigned
	}
	cursor, err := decodeTraceCursor(req, sortType)
	if err != nil {
		return nil, err
	}
	direction := " ASC"
	if req.GetDescending() || req.GetOrderBy() == observabilityv1.TraceOrder_TRACE_ORDER_UNSPECIFIED {
		direction = " DESC"
	}
	columns := "TraceId, root.1, root.2, root.3, start_time, duration_nanos, status, span_count, root.4"
	query = "WITH trace_summaries AS (" + query + ") SELECT *, " + sort + " AS sort_value FROM trace_summaries"
	if req.GetIncludeTotalCount() {
		// Count the whole matching range before seeking past the previous page.
		query = "SELECT *, count() OVER () AS total_count FROM (" + query + ")"
		columns += ", total_count"
	}
	query = "SELECT " + columns + ", toString(sort_value) FROM (" + query + ")"
	if cursor != nil {
		comparison := ">"
		if direction == " DESC" {
			comparison = "<"
		}
		query += " WHERE sort_value " + comparison + " CAST(" + b.str(cursor.value) + " AS " + sortType + ")" +
			" OR (sort_value = CAST(" + b.str(cursor.value) + " AS " + sortType + ") AND TraceId > " + b.str(cursor.trace) + ")"
	}
	query += " ORDER BY sort_value" + direction + ", TraceId ASC LIMIT ?" + settings
	b.args = append(b.args, limit+pageLookahead)
	rows, err := c.query(ctx, "ListTraces", query, b.args...)
	if err != nil {
		return nil, fmt.Errorf("query trace summaries: %w", err)
	}
	defer rows.Close()
	summaries := make([]*observabilityv1.TraceSummary, 0)
	var lastValue string
	var total uint64
	for rows.Next() {
		var id, name, service, resource string
		var status int32
		var httpStatus uint32
		var start time.Time
		var duration, count uint64
		var value string
		dest := []any{&id, &name, &service, &resource, &start, &duration, &status, &count, &httpStatus}
		if req.GetIncludeTotalCount() {
			dest = append(dest, &total)
		}
		dest = append(dest, &value)
		if err := rows.Scan(dest...); err != nil {
			return nil, fmt.Errorf("scan trace summary: %w", err)
		}
		summaries = append(summaries, &observabilityv1.TraceSummary{
			TraceId:        id,
			RootSpanName:   name,
			ServiceName:    service,
			ResourceId:     resource,
			StartTime:      timestamppb.New(start),
			DurationNanos:  duration,
			Status:         observabilityv1.TraceStatus(status),
			SpanCount:      count,
			HttpStatusCode: httpStatus,
		})
		if int32(len(summaries)) == limit {
			lastValue = value
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("trace summary rows: %w", err)
	}
	response := &observabilityv1.ListTracesResponse{Summaries: summaries}
	if int32(len(summaries)) > limit {
		response.Summaries = summaries[:limit]
		response.NextPageToken = encodeTraceCursor(req, summaries[limit-1].GetTraceId(), lastValue)
	}
	// A page past the end has no rows to carry the total.
	pastEnd := len(summaries) == 0 && cursor != nil
	if req.GetIncludeTotalCount() && pastEnd {
		err := c.scanRow(ctx, "CountTraces", "SELECT count() FROM ("+summaryQuery+")"+settings, summaryArgs, &total)
		if err != nil {
			return nil, fmt.Errorf("count trace summaries: %w", err)
		}
	}
	response.TotalCount = total
	return response, nil
}

type traceCursor struct {
	trace string
	value string
}

func encodeTraceCursor(req *observabilityv1.ListTracesRequest, trace, value string) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(fmt.Sprintf("%d:%t:%s:%s", req.GetOrderBy(), req.GetDescending(), trace, value)),
	)
}

func decodeTraceCursor(req *observabilityv1.ListTracesRequest, sortType string) (*traceCursor, error) {
	if req.GetPageToken() == "" {
		return nil, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(req.GetPageToken())
	if err != nil {
		return nil, ErrInvalidTracePageToken
	}
	parts := strings.SplitN(string(decoded), ":", 4)
	if len(parts) != 4 {
		return nil, ErrInvalidTracePageToken
	}
	if parts[0] != strconv.Itoa(int(req.GetOrderBy())) || parts[1] != strconv.FormatBool(req.GetDescending()) {
		return nil, ErrInvalidTracePageToken
	}
	trace, err := hex.DecodeString(parts[2])
	if err != nil || len(trace) != 16 {
		return nil, ErrInvalidTracePageToken
	}
	switch sortType {
	case traceSortTimestamp:
		_, err = strconv.ParseInt(parts[3], 10, 64)
	case traceSortUnsigned:
		_, err = strconv.ParseUint(parts[3], 10, 64)
	}
	if err != nil {
		return nil, ErrInvalidTracePageToken
	}
	return &traceCursor{trace: parts[2], value: parts[3]}, nil
}
