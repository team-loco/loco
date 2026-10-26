package clickhouse

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
)

type Client struct {
	conn driver.Conn
	db   string
}

func NewClient(url string, database string, maxConns int) (*Client, error) {
	opts, err := clickhouse.ParseDSN(url)
	if err != nil {
		return nil, fmt.Errorf("parse clickhouse DSN: %w", err)
	}
	opts.Auth.Database = database
	opts.MaxOpenConns = maxConns
	opts.MaxIdleConns = maxConns/2 + 1

	conn, err := clickhouse.Open(opts)
	if err != nil {
		return nil, fmt.Errorf("open clickhouse connection: %w", err)
	}

	return &Client{conn: conn, db: database}, nil
}

func (c *Client) Conn() driver.Conn {
	return c.conn
}

func (c *Client) Ping(ctx context.Context) error {
	return c.conn.Ping(ctx)
}

func (c *Client) Close() {
	if err := c.conn.Close(); err != nil {
		slog.Error("failed to close clickhouse connection", "error", err)
	}
}

func (c *Client) QueryLogs(
	ctx context.Context, workspaceID string, resourceIDs []string,
	startTime, endTime time.Time, search string, levels []string, labels map[string]string,
	limit int32, cursor string, order observabilityv1.LogOrder, queryTimeout int, scope LogScope,
) ([]*observabilityv1.LogEntry, string, error) {
	return QueryLogs(
		ctx, c.conn, workspaceID, resourceIDs, startTime, endTime, search, levels, labels,
		limit, cursor, order, queryTimeout, scope,
	)
}

func (c *Client) QueryTailLogPage(
	ctx context.Context, request *observabilityv1.TailLogsRequest, since time.Time,
	limit int32, queryTimeout int, cursor TailLogCursor,
) (TailLogPage, error) {
	return QueryTailLogPage(ctx, c.conn, request, since, limit, queryTimeout, cursor)
}

func (c *Client) QueryMetrics(
	ctx context.Context, workspaceID, environmentID, region string, resourceIDs []string,
	startTime, endTime time.Time, metricName string, intervalSeconds int32, aggregation string,
	queryTimeout int,
) ([]*observabilityv1.MetricSeries, error) {
	return QueryMetrics(
		ctx, c.conn, workspaceID, environmentID, region, resourceIDs, startTime, endTime,
		metricName, intervalSeconds, aggregation, queryTimeout,
	)
}

func (c *Client) QueryTraces(
	ctx context.Context, req *observabilityv1.QueryTracesRequest, limit int32, queryTimeout int,
) (*observabilityv1.QueryTracesResponse, error) {
	return QueryTraces(ctx, c.conn, req, limit, queryTimeout)
}
