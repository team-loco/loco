package migrations

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path"
	"regexp"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/pressly/goose/v3"

	"github.com/team-loco/loco/observability-proxy/pkg/clickhouseerr"
)

const VersionTable = "loco_schema_migrations"

//go:embed *.sql
var files embed.FS

var (
	ErrInvalidDatabase    = errors.New("database name is not a ClickHouse identifier")
	ErrInvalidTTL         = errors.New("ttl must be a positive whole number of seconds")
	ErrUnknownPlaceholder = errors.New("migration uses an unknown placeholder")

	identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type Config struct {
	DSN        string
	Database   string
	LogsTTL    time.Duration
	TracesTTL  time.Duration
	MetricsTTL time.Duration
}

func IsIdentifier(name string) bool {
	return identifierPattern.MatchString(name)
}

func validate(cfg Config) error {
	if !IsIdentifier(cfg.Database) {
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
	if err := validate(cfg); err != nil {
		return err
	}
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

func UpWithRetry(ctx context.Context, cfg Config, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for attempt := 1; ; attempt++ {
		err := Up(ctx, cfg)
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrInvalidDatabase) || errors.Is(err, ErrInvalidTTL) {
			return err
		}
		if clickhouseerr.CredentialsRejected(err) {
			return fmt.Errorf("clickhouse rejected the migration connection: %w", err)
		}
		slog.WarnContext(ctx, "clickhouse migrations failed", "attempt", attempt, "error", err)
		select {
		case <-ctx.Done():
			cause := errors.Join(ctx.Err(), err)
			return fmt.Errorf("clickhouse migrations did not succeed (%d attempts): %w", attempt, cause)
		case <-ticker.C:
		}
	}
}

func up(ctx context.Context, cfg Config) ([]*goose.MigrationResult, error) {
	rendered, err := render(cfg)
	if err != nil {
		return nil, err
	}
	return apply(ctx, cfg, rendered)
}

func apply(ctx context.Context, cfg Config, migrations fs.FS) ([]*goose.MigrationResult, error) {
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
		migrations,
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
