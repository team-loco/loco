package db

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	statementTimeoutParam = "statement_timeout"
	maxConnLifetime       = time.Hour
	maxConnLifetimeJitter = 5 * time.Minute
	maxConnIdleTime       = 2 * time.Minute
	connectTimeout        = 5 * time.Second
)

type Config struct {
	URL      string
	MaxConns int32
	MinConns int32
	// StatementTimeout applies unless URL sets statement_timeout itself.
	StatementTimeout time.Duration
}

func NewPool(ctx context.Context, config Config) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(config.URL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database config: %w", err)
	}

	cfg.MaxConns = config.MaxConns
	cfg.MinConns = config.MinConns
	cfg.MaxConnLifetime = maxConnLifetime
	cfg.MaxConnLifetimeJitter = maxConnLifetimeJitter
	cfg.MaxConnIdleTime = maxConnIdleTime
	cfg.ConnConfig.ConnectTimeout = connectTimeout
	if _, ok := cfg.ConnConfig.RuntimeParams[statementTimeoutParam]; !ok {
		milliseconds := strconv.FormatInt(config.StatementTimeout.Milliseconds(), 10)
		cfg.ConnConfig.RuntimeParams[statementTimeoutParam] = milliseconds
	}

	cfg.ConnConfig.Tracer = otelpgx.NewTracer()

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	slog.Info("Database connection established")

	return pool, nil
}
