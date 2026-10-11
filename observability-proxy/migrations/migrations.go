package migrations

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"regexp"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/pressly/goose/v3"
)

const VersionTable = "loco_schema_migrations"

//go:embed *.sql
var files embed.FS

var (
	ErrInvalidDatabase      = errors.New("database name is not a ClickHouse identifier")
	ErrInvalidTTL           = errors.New("ttl must be a positive whole number of seconds")
	ErrUnknownPlaceholder   = errors.New("migration uses an unknown placeholder")
	ErrRetryBudgetExhausted = errors.New("clickhouse migrations did not succeed within the retry budget")

	identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type Config struct {
	DSN        string
	Database   string
	LogsTTL    time.Duration
	TracesTTL  time.Duration
	MetricsTTL time.Duration
}

type Retry struct {
	Budget   time.Duration
	Interval time.Duration
}

func validate(cfg Config) error {
	if !identifierPattern.MatchString(cfg.Database) {
		return fmt.Errorf("%w: %q", ErrInvalidDatabase, cfg.Database)
	}
	for _, ttl := range []time.Duration{cfg.LogsTTL, cfg.TracesTTL, cfg.MetricsTTL} {
		if ttl <= 0 || ttl%time.Second != 0 {
			return fmt.Errorf("%w: %s", ErrInvalidTTL, ttl)
		}
	}
	return nil
}

func Up(ctx context.Context, cfg Config) error {
	results, err := up(ctx, cfg)
	if err != nil {
		return err
	}
	for _, result := range results {
		slog.InfoContext(
			ctx,
			"applied clickhouse migration",
			"version", result.Source.Version,
			"file", path.Base(result.Source.Path),
			"duration", result.Duration,
		)
	}
	return nil
}

func UpWithRetry(ctx context.Context, cfg Config, retry Retry) error {
	if err := validate(cfg); err != nil {
		return err
	}
	budgetCtx, cancel := context.WithTimeout(ctx, retry.Budget)
	defer cancel()
	ticker := time.NewTicker(retry.Interval)
	defer ticker.Stop()
	for attempt := 1; ; attempt++ {
		err := Up(budgetCtx, cfg)
		if err == nil {
			return nil
		}
		slog.WarnContext(ctx, "clickhouse migrations failed", "attempt", attempt, "error", err)
		select {
		case <-budgetCtx.Done():
			if ctxErr := ctx.Err(); ctxErr != nil {
				return fmt.Errorf("%w: %w", ctxErr, err)
			}
			return fmt.Errorf("%w (%s, %d attempts): %w", ErrRetryBudgetExhausted, retry.Budget, attempt, err)
		case <-ticker.C:
		}
	}
}

func up(ctx context.Context, cfg Config) ([]*goose.MigrationResult, error) {
	if err := validate(cfg); err != nil {
		return nil, err
	}
	rendered, err := render(cfg)
	if err != nil {
		return nil, err
	}
	opts, err := clickhouse.ParseDSN(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse migrator DSN: %w", err)
	}
	if createErr := createDatabase(ctx, opts, cfg.Database); createErr != nil {
		return nil, createErr
	}
	opts.Auth.Database = cfg.Database
	db := clickhouse.OpenDB(opts)
	defer func() {
		if closeErr := db.Close(); closeErr != nil {
			slog.WarnContext(ctx, "close clickhouse migration connection", "error", closeErr)
		}
	}()
	provider, err := goose.NewProvider(
		goose.DialectClickHouse,
		db,
		rendered,
		goose.WithTableName(VersionTable),
		goose.WithDisableGlobalRegistry(true),
		goose.WithIsolateDDL(true),
		goose.WithSlog(slog.Default()),
	)
	if err != nil {
		return nil, fmt.Errorf("load clickhouse migrations: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return nil, fmt.Errorf("apply clickhouse migrations: %w", err)
	}
	return results, nil
}

func createDatabase(ctx context.Context, opts *clickhouse.Options, database string) error {
	bootstrap := *opts
	bootstrap.Auth.Database = ""
	conn, err := clickhouse.Open(&bootstrap)
	if err != nil {
		return fmt.Errorf("open clickhouse migration connection: %w", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			slog.WarnContext(ctx, "close clickhouse bootstrap connection", "error", closeErr)
		}
	}()
	if err := conn.Exec(ctx, "CREATE DATABASE IF NOT EXISTS "+database); err != nil {
		return fmt.Errorf("create database %s: %w", database, err)
	}
	return nil
}
