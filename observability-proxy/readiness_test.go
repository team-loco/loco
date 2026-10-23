package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/team-loco/loco/observability-proxy/pkg/config"
)

func TestReadyzDoesNotDependOnTheControlPlane(t *testing.T) {
	_, dsn, database := testClickHouse(t)
	cfg := &config.Config{
		QueryEnabled:           true,
		ControlPlaneURL:        unreachableControlPlane,
		ClickHouseURL:          dsn,
		ClickHouseMigratorURL:  dsn,
		ClickHouseDB:           database,
		LogsTTL:                testTTL,
		TracesTTL:              testTTL,
		MetricsTTL:             testTTL,
		MigrationRetryBudget:   sharedRetryBudget,
		MigrationRetryInterval: testRetryInterval,
		MaxConcurrent:          testMaxConcurrent,
		TokenCacheTTL:          testTokenTTL,
	}
	p, err := newProxy(cfg, unlocked{}, testVersion)
	if err != nil {
		t.Fatalf("newProxy: %v", err)
	}
	t.Cleanup(p.close)
	if err := p.migrate(t.Context(), cfg); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	server := httptest.NewServer(p.server.Handler)
	t.Cleanup(server.Close)
	if status := getStatus(t, server.URL+"/readyz"); status != http.StatusOK {
		t.Fatalf("readiness with an unreachable control plane = %d, want %d", status, http.StatusOK)
	}
}
