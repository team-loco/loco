package migrationlock

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/uuid"

	"github.com/team-loco/loco/internal/clickhouse/clickhouseerr"
)

const (
	Table   = "loco_migration_lock"
	lockKey = "schema"

	minRenewalsPerTTL = 2
)

var (
	ErrNotPositive = errors.New("lock timings must be positive")
	ErrRetryPeriod = errors.New("retry period must be at most half the lock ttl")
	ErrNotAcquired = errors.New("migration lock was not acquired")
	ErrLockLost    = errors.New("migration lock was lost before the migration finished")

	strictMode = clickhouse.WithSettings(clickhouse.Settings{"keeper_map_strict_mode": 1})
)

type Config struct {
	DSN         string
	Database    string
	TTL         time.Duration
	RetryPeriod time.Duration
}

func (c Config) Validate() error {
	if c.TTL <= 0 || c.RetryPeriod <= 0 {
		return fmt.Errorf("%w: ttl %s, retry period %s", ErrNotPositive, c.TTL, c.RetryPeriod)
	}
	if minRenewalsPerTTL*c.RetryPeriod > c.TTL {
		return fmt.Errorf("%w: retry period %s, ttl %s", ErrRetryPeriod, c.RetryPeriod, c.TTL)
	}
	return nil
}

type Lock struct {
	cfg      Config
	identity string
	table    string
}

func New(cfg Config) *Lock {
	return &Lock{cfg: cfg, identity: uuid.NewString(), table: cfg.Database + "." + Table}
}

func (l *Lock) logAttrs() []any {
	return []any{"table", l.table, "holder", l.identity}
}

func (l *Lock) WithLock(ctx context.Context, fn func(context.Context) error) error {
	opts, err := clickhouse.ParseDSN(l.cfg.DSN)
	if err != nil {
		return fmt.Errorf("parse migration lock DSN: %w", err)
	}
	opts.Auth.Database = ""
	// The driver bounds a new connection's dial and handshake by DialTimeout alone, not by the context.
	if opts.DialTimeout == 0 || opts.DialTimeout > l.cfg.RetryPeriod {
		opts.DialTimeout = l.cfg.RetryPeriod
	}
	conn, err := clickhouse.Open(opts)
	if err != nil {
		return fmt.Errorf("open migration lock connection: %w", err)
	}
	defer func() {
		if closeErr := conn.Close(); closeErr != nil {
			slog.WarnContext(ctx, "close migration lock connection", "error", closeErr)
		}
	}()

	leaseEnd, err := l.acquire(ctx, conn)
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithCancelCause(ctx)
	renewed := make(chan struct{})
	go func() {
		defer close(renewed)
		l.renew(runCtx, conn, leaseEnd, cancel)
	}()
	result := fn(runCtx)
	cancel(nil)
	<-renewed
	l.release(ctx, conn)
	if errors.Is(context.Cause(runCtx), ErrLockLost) {
		return errors.Join(ErrLockLost, result)
	}
	return result
}

func (l *Lock) acquire(ctx context.Context, conn driver.Conn) (time.Time, error) {
	slog.InfoContext(ctx, "acquiring migration lock", l.logAttrs()...)
	ticker := time.NewTicker(l.cfg.RetryPeriod)
	defer ticker.Stop()
	for {
		holder, leaseEnd, err := l.tryAcquire(ctx, conn)
		switch {
		case err != nil && clickhouseerr.CredentialsRejected(err):
			return time.Time{}, fmt.Errorf("acquire %s: %w", l.table, err)
		case err != nil:
			slog.WarnContext(ctx, "migration lock attempt failed", append(l.logAttrs(), "error", err)...)
		case holder == l.identity:
			slog.InfoContext(ctx, "acquired migration lock", l.logAttrs()...)
			return leaseEnd, nil
		default:
			slog.InfoContext(ctx, "waiting for migration lock", append(l.logAttrs(), "current_holder", holder)...)
		}
		select {
		case <-ctx.Done():
			return time.Time{}, fmt.Errorf("%w: %s: %w", ErrNotAcquired, l.table, context.Cause(ctx))
		case <-ticker.C:
		}
	}
}

func (l *Lock) tryAcquire(ctx context.Context, conn driver.Conn) (string, time.Time, error) {
	if err := l.createTable(ctx, conn); err != nil {
		return "", time.Time{}, err
	}
	strictCtx := clickhouse.Context(ctx, strictMode)
	insert := "INSERT INTO " + l.table + " SELECT ?, ?, now64(3) + toIntervalMillisecond(?)"
	insertErr := conn.Exec(strictCtx, insert, lockKey, l.identity, l.cfg.TTL.Milliseconds())
	if insertErr == nil {
		return l.holder(ctx, conn)
	}
	steal := "ALTER TABLE " + l.table +
		" UPDATE holder = ?, expires_at = now64(3) + toIntervalMillisecond(?) WHERE key = ? AND expires_at < now64(3)"
	stealErr := conn.Exec(strictCtx, steal, l.identity, l.cfg.TTL.Milliseconds(), lockKey)
	holder, leaseEnd, err := l.holder(ctx, conn)
	if err != nil {
		return "", time.Time{}, errors.Join(insertErr, stealErr, err)
	}
	return holder, leaseEnd, nil
}

func (l *Lock) createTable(ctx context.Context, conn driver.Conn) error {
	if err := conn.Exec(ctx, "CREATE DATABASE IF NOT EXISTS "+l.cfg.Database); err != nil {
		return fmt.Errorf("create database %s: %w", l.cfg.Database, err)
	}
	create := "CREATE TABLE IF NOT EXISTS " + l.table +
		" (key String, holder String, expires_at DateTime64(3))" +
		" ENGINE = KeeperMap('/" + l.cfg.Database + "/" + Table + "') PRIMARY KEY key"
	if err := conn.Exec(ctx, create); err != nil {
		return fmt.Errorf("create %s: %w", l.table, err)
	}
	return nil
}

// holder returns the current holder and the local time by which its lease has ended on the server.
func (l *Lock) holder(ctx context.Context, conn driver.Conn) (string, time.Time, error) {
	sentAt := time.Now()
	rows, err := conn.Query(ctx,
		"SELECT holder, dateDiff('millisecond', now64(3), expires_at) FROM "+l.table+" WHERE key = ?", lockKey)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("read %s: %w", l.table, err)
	}
	defer rows.Close()
	var holder string
	var remainingMs int64
	if rows.Next() {
		if err := rows.Scan(&holder, &remainingMs); err != nil {
			return "", time.Time{}, fmt.Errorf("scan %s: %w", l.table, err)
		}
	}
	if err := rows.Err(); err != nil {
		return "", time.Time{}, fmt.Errorf("read %s: %w", l.table, err)
	}
	return holder, sentAt.Add(time.Duration(remainingMs) * time.Millisecond), nil
}

func (l *Lock) renew(ctx context.Context, conn driver.Conn, leaseEnd time.Time, lose context.CancelCauseFunc) {
	ticker := time.NewTicker(l.cfg.RetryPeriod)
	defer ticker.Stop()
	expiry := time.AfterFunc(time.Until(leaseEnd), func() {
		slog.WarnContext(ctx, "migration lock expired", l.logAttrs()...)
		lose(ErrLockLost)
	})
	defer expiry.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		holder, leaseEnd, err := l.extend(ctx, conn)
		switch {
		case ctx.Err() != nil:
			return
		case err == nil && holder == l.identity:
			expiry.Reset(time.Until(leaseEnd))
		case err == nil:
			slog.WarnContext(ctx, "migration lock taken over", append(l.logAttrs(), "current_holder", holder)...)
			lose(ErrLockLost)
			return
		default:
			slog.WarnContext(ctx, "migration lock renewal failed", append(l.logAttrs(), "error", err)...)
		}
	}
}

func (l *Lock) extend(ctx context.Context, conn driver.Conn) (string, time.Time, error) {
	strictCtx := clickhouse.Context(ctx, strictMode)
	update := "ALTER TABLE " + l.table +
		" UPDATE expires_at = now64(3) + toIntervalMillisecond(?) WHERE key = ? AND holder = ?"
	if err := conn.Exec(strictCtx, update, l.cfg.TTL.Milliseconds(), lockKey, l.identity); err != nil {
		return "", time.Time{}, fmt.Errorf("renew %s: %w", l.table, err)
	}
	return l.holder(ctx, conn)
}

func (l *Lock) release(ctx context.Context, conn driver.Conn) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), l.cfg.TTL)
	defer cancel()
	strictCtx := clickhouse.Context(releaseCtx, strictMode)
	err := conn.Exec(strictCtx, "DELETE FROM "+l.table+" WHERE key = ? AND holder = ?", lockKey, l.identity)
	if err != nil {
		slog.WarnContext(
			ctx,
			"migration lock not released; it expires on its own",
			append(l.logAttrs(), "expires_after", l.cfg.TTL, "error", err)...)
		return
	}
	slog.InfoContext(ctx, "released migration lock", l.logAttrs()...)
}
