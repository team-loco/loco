package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
	"github.com/team-loco/loco/observability-proxy/migrations"
	"github.com/team-loco/loco/observability-proxy/pkg/auth"
	"github.com/team-loco/loco/observability-proxy/pkg/cache"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	eventsTestToken          = "events-reader"
	eventsTestOtherWorkspace = "99999999-8888-7777-6666-555555555555"
	eventsTestLimit          = 100
	eventsTestMaxLimit       = 1000
	eventsTestCacheTTL       = time.Minute
	eventsTestMaxRange       = 24 * time.Hour
	eventsTestEnvironment    = "66666666-7777-8888-9999-000000000000"
	eventsTestOtherEnv       = "77777777-8888-9999-0000-111111111111"
	eventsTestResource       = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	eventsTestMaxConns       = 2
	eventsTestBody           = `{"type":"ADDED","object":{"metadata":{"uid":"%s"},"type":"Warning","reason":"%s",` +
		`"note":"Back-off restarting failed container","regarding":{"kind":"Pod","name":"resource-web-x2k4p"}}}`
)

var errEventsTestNoCallInfo = errors.New("no call info")

type workspaceTokenServer struct {
	tokenv1connect.UnimplementedTokenServiceHandler
	workspaceID string
}

func (s *workspaceTokenServer) CheckPermission(
	ctx context.Context,
	req *tokenv1.CheckPermissionRequest,
) (*tokenv1.CheckPermissionResponse, error) {
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok {
		return nil, errEventsTestNoCallInfo
	}
	credential := info.RequestHeader().Get("Authorization")
	allowed := credential == "Bearer "+eventsTestToken && req.GetEntityId() == s.workspaceID
	return &tokenv1.CheckPermissionResponse{Allowed: allowed}, nil
}

func eventsProxy(t *testing.T, ch *chClient.Client) observabilityv1connect.ObservabilityProxyServiceClient {
	t.Helper()
	tokens := &workspaceTokenServer{workspaceID: tailTestWorkspace}
	tokenServer := connect.NewServer()
	tokenv1connect.RegisterTokenServiceHandler(tokenServer, tokens)
	tokenMux := http.NewServeMux()
	connecthttp.Mount(tokenMux, tokenServer)
	controlPlane := httptest.NewServer(tokenMux)
	t.Cleanup(controlPlane.Close)

	permissions, err := cache.NewMemory(eventsTestCacheTTL)
	if err != nil {
		t.Fatalf("permission cache: %v", err)
	}
	t.Cleanup(func() {
		if err := permissions.Close(); err != nil {
			t.Errorf("close permission cache: %v", err)
		}
	})
	validator := auth.NewValidator(controlPlane.URL, "", permissions)
	cfg := &config.Config{
		DefaultLimit: eventsTestLimit,
		MaxLimit:     eventsTestMaxLimit,
		MaxTimeRange: eventsTestMaxRange,
		QueryTimeout: tailTestQueryTimeout,
	}
	svc := NewObservabilityService(ch, cfg, validator)
	rpcServer := connect.NewServer(auth.NewAuthInterceptor().Interceptor())
	observabilityv1connect.RegisterObservabilityProxyServiceHandler(rpcServer, svc)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpcServer)
	proxy := httptest.NewServer(mux)
	t.Cleanup(proxy.Close)
	transport := connecthttp.NewTransport(http.DefaultClient, proxy.URL)
	return observabilityv1connect.NewObservabilityProxyServiceClient(connect.NewClient(transport))
}

func TestQueryEventsChecksWorkspacePermissionBeforeQuerying(t *testing.T) {
	client := eventsProxy(t, nil)
	now := time.Now()
	hourAgo := now.Add(-time.Hour)
	end := timestamppb.New(now)
	start := timestamppb.New(hourAgo)
	cases := []struct {
		name        string
		token       string
		workspaceID string
		want        connect.Code
	}{
		{name: "no token", workspaceID: tailTestWorkspace, want: connect.CodeUnauthenticated},
		{
			name:        "other workspace",
			token:       eventsTestToken,
			workspaceID: eventsTestOtherWorkspace,
			want:        connect.CodePermissionDenied,
		},
		{
			name:        "foreign token",
			token:       "someone-else",
			workspaceID: tailTestWorkspace,
			want:        connect.CodePermissionDenied,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := &observabilityv1.QueryEventsRequest{
				WorkspaceId: tc.workspaceID,
				StartTime:   start,
				EndTime:     end,
			}
			ctx, info := connect.NewClientContext(t.Context())
			if tc.token != "" {
				info.RequestHeader().Set("Authorization", "Bearer "+tc.token)
			}
			_, err := client.QueryEvents(ctx, msg)
			if connect.CodeOf(err) != tc.want {
				t.Fatalf("QueryEvents error = %v, want %v", err, tc.want)
			}
		})
	}
}

func eventsDatabase(t *testing.T) *chClient.Client {
	t.Helper()
	dsn := os.Getenv("LOCO_TEST_CLICKHOUSE_URL")
	if dsn == "" {
		t.Skip("LOCO_TEST_CLICKHOUSE_URL not set")
	}
	database := "events_" + fmt.Sprintf("%x", uuid.New())
	migration := migrations.Config{
		DSN:        dsn,
		Database:   database,
		LogsTTL:    time.Hour,
		TracesTTL:  time.Hour,
		MetricsTTL: time.Hour,
	}
	if err := migrations.Up(t.Context(), migration); err != nil {
		t.Fatal(err)
	}
	client, err := chClient.NewClient(dsn, database, eventsTestMaxConns)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Conn().Exec(context.Background(), "DROP DATABASE "+database+" SYNC"); err != nil {
			t.Error(err)
		}
		client.Close()
	})
	return client
}

func TestQueryEventsReturnsTheRequestedEnvironment(t *testing.T) {
	ch := eventsDatabase(t)
	batch, err := ch.Conn().
		PrepareBatch(t.Context(), "INSERT INTO otel_k8s_events (Timestamp, Body, ResourceAttributes)")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	reasons := map[string]string{eventsTestEnvironment: "BackOff", eventsTestOtherEnv: "Unhealthy"}
	for environmentID, reason := range reasons {
		body := fmt.Sprintf(eventsTestBody, environmentID, reason)
		attrs := map[string]string{
			"loco.workspace.id":   tailTestWorkspace,
			"loco.environment.id": environmentID,
			"loco.resource.id":    eventsTestResource,
		}
		if appendErr := batch.Append(now.Add(-time.Minute), body, attrs); appendErr != nil {
			t.Fatal(appendErr)
		}
	}
	if sendErr := batch.Send(); sendErr != nil {
		t.Fatal(sendErr)
	}

	client := eventsProxy(t, ch)
	end := timestamppb.New(now)
	hourAgo := now.Add(-time.Hour)
	start := timestamppb.New(hourAgo)
	msg := &observabilityv1.QueryEventsRequest{
		WorkspaceId:   tailTestWorkspace,
		EnvironmentId: eventsTestEnvironment,
		ResourceIds:   []string{eventsTestResource},
		StartTime:     start,
		EndTime:       end,
	}
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("Authorization", "Bearer "+eventsTestToken)
	resp, err := client.QueryEvents(ctx, msg)
	if err != nil {
		t.Fatalf("QueryEvents: %v", err)
	}
	events := resp.GetEvents()
	got := make([]string, 0, len(events))
	for _, event := range events {
		got = append(got, event.GetReason()+" "+event.GetResourceId())
	}
	want := []string{reasons[eventsTestEnvironment] + " " + eventsTestResource}
	if !slices.Equal(got, want) {
		t.Errorf("events = %v, want %v", got, want)
	}
}
