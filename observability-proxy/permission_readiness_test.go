package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
	"github.com/team-loco/loco/observability-proxy/pkg/migrationlock"
)

const (
	testQueryToken          = "query"
	testPermissionCalls     = 4
	testProxyCredential     = "test-proxy-credential"
	testQueryTimeoutSeconds = 1
)

var errTestCredentialRejected = errors.New("test credential rejected")

type readinessTokenServer struct {
	tokenv1connect.UnimplementedTokenServiceHandler
	rejected atomic.Bool
	calls    atomic.Int32
}

func (s *readinessTokenServer) CheckPermission(
	ctx context.Context,
	req *tokenv1.CheckPermissionRequest,
) (*tokenv1.CheckPermissionResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	s.calls.Add(1)
	if s.rejected.Load() || info.RequestHeader().Get("Authorization") != "Bearer "+testProxyCredential {
		return nil, connect.NewError(connect.CodeUnauthenticated, errTestCredentialRejected.Error()).
			WithCause(errTestCredentialRejected)
	}
	allowed := req.GetToken() == testQueryToken
	response := &tokenv1.CheckPermissionResponse{Allowed: allowed}
	return response, nil
}

func readinessControlPlane(t *testing.T) (*readinessTokenServer, string) {
	t.Helper()
	service := &readinessTokenServer{}
	rpcServer := connect.NewServer()
	tokenv1connect.RegisterTokenServiceHandler(rpcServer, service)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpcServer)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return service, server.URL
}

func TestReadyzChecksTheControlPlaneAndRecovers(t *testing.T) {
	_, dsn, database := testClickHouse(t)
	controlPlane, controlPlaneURL := readinessControlPlane(t)
	cfg := &config.Config{
		ControlPlaneURL:        controlPlaneURL,
		ProxyAuthToken:         testProxyCredential,
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
		QueryTimeout:           testQueryTimeoutSeconds,
		PermissionCheckTimeout: healthzDeadline,
		DefaultLimit:           testMaxConcurrent,
		MaxLimit:               testMaxConcurrent,
		MaxTimeRange:           testTTL,
	}
	p, err := newProxy(cfg, migrationlock.None{})
	if err != nil {
		t.Fatalf("newProxy: %v", err)
	}
	t.Cleanup(p.close)
	if err := p.migrate(t.Context(), cfg); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	server := httptest.NewServer(p.server.Handler)
	t.Cleanup(server.Close)
	controlPlane.rejected.Store(true)
	if status := getStatus(t, server.URL+"/readyz"); status != http.StatusServiceUnavailable {
		t.Fatalf("readiness with rejected credential = %d, want %d", status, http.StatusServiceUnavailable)
	}
	if status := getStatus(t, server.URL+"/healthz"); status != http.StatusOK {
		t.Fatalf("liveness while control plane is unavailable = %d", status)
	}
	controlPlane.rejected.Store(false)
	if status := getStatus(t, server.URL+"/readyz"); status != http.StatusOK {
		t.Fatalf("readiness after recovery = %d, want %d", status, http.StatusOK)
	}
	httpClient := &http.Client{Timeout: healthzDeadline}
	client := observabilityv1connect.NewObservabilityProxyServiceClient(
		connect.NewClient(connecthttp.NewTransport(httpClient, server.URL)),
	)
	now := time.Now()
	start := timestamppb.New(now.Add(-time.Minute))
	end := timestamppb.New(now)
	message := &observabilityv1.QueryLogsRequest{
		WorkspaceId: "00000000-0000-4000-8000-000000000000",
		StartTime:   start,
		EndTime:     end,
	}
	req := message
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("Authorization", "Bearer "+testQueryToken)
	if _, err := client.QueryLogs(ctx, req); err != nil {
		t.Fatalf("authorized query through the proxy: %v", err)
	}
	controlPlane.rejected.Store(true)
	if status := getStatus(t, server.URL+"/readyz"); status != http.StatusServiceUnavailable {
		t.Fatalf("readiness after credential rejection = %d, want %d", status, http.StatusServiceUnavailable)
	}
	if controlPlane.calls.Load() != testPermissionCalls {
		t.Fatalf("permission endpoint calls = %d, want %d", controlPlane.calls.Load(), testPermissionCalls)
	}
}
