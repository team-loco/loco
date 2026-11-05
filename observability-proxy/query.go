package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/team-loco/loco/observability-proxy/migrations"
	"github.com/team-loco/loco/observability-proxy/pkg/auth"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
	"github.com/team-loco/loco/observability-proxy/pkg/migrationlock"
	"github.com/team-loco/loco/observability-proxy/service"
)

var errMigrationsPending = errors.New("clickhouse migrations pending")

type queryRole struct {
	ch        *chClient.Client
	validator *auth.Validator
	handler   http.Handler
	lock      *migrationlock.Lock
	migrated  atomic.Bool
}

func newQueryRole(cfg *config.Config, lock *migrationlock.Lock) (*queryRole, error) {
	ch, err := chClient.NewClient(cfg.ClickHouseURL, cfg.ClickHouseDB, cfg.MaxConcurrent, cfg.QueryTimeout)
	if err != nil {
		return nil, fmt.Errorf("connect to clickhouse: %w", err)
	}
	validator, err := auth.NewValidator(cfg.ControlPlaneURL, cfg.TokenCacheTTL)
	if err != nil {
		ch.Close()
		return nil, err
	}
	svc := service.NewObservabilityService(ch, cfg, validator)
	handler := withCORS(cfg.CORSAllowedOrigins, svc.Handler())
	return &queryRole{ch: ch, validator: validator, handler: handler, lock: lock}, nil
}

func (r *queryRole) migrate(ctx context.Context, cfg *config.Config) error {
	schema := migrations.Config{
		DSN:        cfg.ClickHouseMigratorURL,
		Database:   cfg.ClickHouseDB,
		LogsTTL:    cfg.LogsTTL,
		TracesTTL:  cfg.TracesTTL,
		MetricsTTL: cfg.MetricsTTL,
	}
	budgetCtx, cancel := context.WithTimeout(ctx, cfg.MigrationRetryBudget)
	defer cancel()
	err := r.lock.WithLock(budgetCtx, func(lockCtx context.Context) error {
		return migrations.UpWithRetry(lockCtx, schema, cfg.MigrationRetryInterval)
	})
	if err != nil {
		return fmt.Errorf("migrate clickhouse schema: %w", err)
	}
	r.migrated.Store(true)
	return nil
}

func (r *queryRole) ready(ctx context.Context) error {
	if !r.migrated.Load() {
		return errMigrationsPending
	}
	if err := r.ch.Ping(ctx); err != nil {
		return fmt.Errorf("clickhouse: %w", err)
	}
	return nil
}

func (r *queryRole) close() {
	r.ch.Close()
	r.validator.Close()
}
