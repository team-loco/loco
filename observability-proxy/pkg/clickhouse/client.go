package clickhouse

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// Client wraps a ClickHouse connection pool.
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
