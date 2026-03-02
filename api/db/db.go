package db

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DB wraps the connection pool
type DB struct {
	pool *pgxpool.Pool
}

// NewDB creates a new database connection pool
func NewDB(ctx context.Context, databaseURL string) (*DB, error) {
	if databaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL not set")
	}

	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database config: %w", err)
	}

	// todo: are these the best settings
	// todo: ensure we use pool all the time
	// todo: ensure we use pg tx where necessary.
	cfg.MaxConns = 25
	cfg.MinConns = 5
	cfg.MaxConnLifetime = 5 * time.Minute
	cfg.MaxConnIdleTime = 2 * time.Minute
	cfg.ConnConfig.ConnectTimeout = 5 * time.Second
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		for _, typeName := range []string{"entity_type", "entity_scope"} {
			pgType, pgErr := conn.LoadType(ctx, typeName)
			if pgErr != nil {
				return fmt.Errorf("load pg type %q: %w", typeName, pgErr)
			}
			conn.TypeMap().RegisterType(pgType)
		}
		return nil
	}

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Verify connection
	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	slog.Info("Database connection established")

	return &DB{pool: pool}, nil
}

// Close closes the connection pool
func (d *DB) Close() {
	if d.pool != nil {
		d.pool.Close()
	}
}

// Pool returns the underlying pgxpool.Pool
func (d *DB) Pool() *pgxpool.Pool {
	return d.pool
}

// RunMigrations runs SQL migrations from a file or string
func (d *DB) RunMigrations(ctx context.Context, migrationSQL string) error {
	conn, err := d.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("failed to acquire connection: %w", err)
	}
	defer conn.Release()

	_, err = conn.Exec(ctx, migrationSQL)
	if err != nil {
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	slog.Info("Migrations completed successfully")
	return nil
}
