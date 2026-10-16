package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/types/known/timestamppb"

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
)

const traceHTTPQueryTimeout = 10

const traceTestReader = "workspace-reader"

const traceTestOtherWorkspace = "99999999-8888-7777-6666-555555555555"

type tracePermissions struct {
	tokenv1connect.UnimplementedTokenServiceHandler
	t *testing.T
}

func (p *tracePermissions) CheckPermission(
	ctx context.Context,
	req *tokenv1.CheckPermissionRequest,
) (*tokenv1.CheckPermissionResponse, error) {
	if req.GetEntityType() != tokenv1.EntityType_ENTITY_TYPE_WORKSPACE || req.GetScope() != tokenv1.Scope_SCOPE_READ {
		p.t.Errorf("wrong permission request: %v", req)
	}
	info, _ := connect.CallInfoForServerContext(ctx)
	allowed := info.RequestHeader().Get("Authorization") == "Bearer workspace-reader" &&
		req.GetEntityId() == tailTestWorkspace
	allowed = allowed ||
		(info.RequestHeader().Get("Authorization") == "Bearer other-reader" && req.GetEntityId() == traceTestOtherWorkspace)
	return &tokenv1.CheckPermissionResponse{Allowed: allowed}, nil
}

func traceTestClient(
	t *testing.T,
	ch *chClient.Client,
	queryTimeout ...int,
) observabilityv1connect.ObservabilityProxyServiceClient {
	t.Helper()
	controlPlane := connect.NewServer()
	tokenv1connect.RegisterTokenServiceHandler(controlPlane, &tracePermissions{t: t})
	controlMux := http.NewServeMux()
	connecthttp.Mount(controlMux, controlPlane)
	controlHTTP := httptest.NewServer(controlMux)
	t.Cleanup(controlHTTP.Close)
	memory, err := cache.NewMemory(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := memory.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	validator := auth.NewValidator(controlHTTP.URL, memory)
	if ch == nil {
		var clientErr error
		ch, clientErr = chClient.NewClient("clickhouse://127.0.0.1:1", "default", 1)
		if clientErr != nil {
			t.Fatal(clientErr)
		}
		t.Cleanup(ch.Close)
	}
	timeout := traceHTTPQueryTimeout
	if len(queryTimeout) > 0 {
		timeout = queryTimeout[0]
	}
	cfg := &config.Config{DefaultLimit: 1, MaxLimit: 2, MaxTimeRange: time.Hour, QueryTimeout: timeout}
	service := NewObservabilityService(ch, cfg, validator)
	server := connect.NewServer(auth.NewAuthInterceptor().Interceptor())
	observabilityv1connect.RegisterObservabilityProxyServiceHandler(server, service)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	proxyHTTP := httptest.NewServer(mux)
	t.Cleanup(proxyHTTP.Close)
	transport := connecthttp.NewTransport(proxyHTTP.Client(), proxyHTTP.URL)
	return observabilityv1connect.NewObservabilityProxyServiceClient(connect.NewClient(transport))
}

func TestQueryTracesHTTPAuthValidationAndErrors(t *testing.T) {
	client := traceTestClient(t, nil)
	cases := []struct {
		name, token, workspace, cursor string
		missingTime                    bool
		code                           connect.Code
	}{
		{name: "missing token", workspace: tailTestWorkspace, code: connect.CodeUnauthenticated},
		{name: "unknown token", token: "unknown", workspace: tailTestWorkspace, code: connect.CodePermissionDenied},
		{
			name:      "other workspace",
			token:     traceTestReader,
			workspace: traceTestOtherWorkspace,
			code:      connect.CodePermissionDenied,
		},
		{name: "empty workspace", token: traceTestReader, code: connect.CodeInvalidArgument},
		{
			name:        "missing time",
			token:       "workspace-reader",
			workspace:   tailTestWorkspace,
			missingTime: true,
			code:        connect.CodeInvalidArgument,
		},
		{
			name:      "invalid cursor",
			token:     traceTestReader,
			workspace: tailTestWorkspace,
			cursor:    "invalid!",
			code:      connect.CodeInvalidArgument,
		},
		{
			name:      "authorized storage failure",
			token:     traceTestReader,
			workspace: tailTestWorkspace,
			code:      connect.CodeInternal,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Now()
			req := &observabilityv1.QueryTracesRequest{
				WorkspaceId: tc.workspace,
				StartTime:   timestamppb.New(now.Add(-time.Minute)),
				EndTime:     timestamppb.New(now),
				Cursor:      tc.cursor,
			}
			if tc.missingTime {
				req.StartTime = nil
			}
			ctx, info := connect.NewClientContext(t.Context())
			if tc.token != "" {
				info.RequestHeader().Set("Authorization", "Bearer "+tc.token)
			}
			_, err := client.QueryTraces(ctx, req)
			if connect.CodeOf(err) != tc.code {
				t.Fatalf("error = %v, want %v", err, tc.code)
			}
			if tc.code == connect.CodeInternal && err.Error() != "internal: query failed" {
				t.Errorf("private diagnostic exposed: %v", err)
			}
		})
	}
}

func TestQueryTracesHTTPReadsIsolatedSpans(t *testing.T) {
	dsn := os.Getenv("LOCO_TEST_CLICKHOUSE_URL")
	if dsn == "" {
		t.Skip("LOCO_TEST_CLICKHOUSE_URL not set")
	}
	id := uuid.New()
	database := "trace_http_" + fmt.Sprintf("%x", id)
	cfg := migrations.Config{
		DSN:        dsn,
		Database:   database,
		LogsTTL:    time.Hour,
		TracesTTL:  time.Hour,
		MetricsTTL: time.Hour,
		EventsTTL:  time.Hour,
	}
	if err := migrations.Up(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	ch, err := chClient.NewClient(dsn, database, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if dropErr := ch.Conn().Exec(context.Background(), "DROP DATABASE "+database+" SYNC"); dropErr != nil {
			t.Error(dropErr)
		}
		ch.Close()
	})
	timestamp := time.Now().UTC().Add(-time.Minute)
	const traceID = "0123456789abcdef0123456789abcdef"
	const resourceID = "ffffffff-eeee-dddd-cccc-bbbbbbbbbbbb"
	batch, prepareErr := ch.Conn().
		PrepareBatch(t.Context(), "INSERT INTO otel_traces "+
			"(Timestamp, TraceId, SpanId, ParentSpanId, ServiceName, ResourceAttributes)")
	if prepareErr != nil {
		t.Fatal(prepareErr)
	}
	for _, row := range []struct{ workspace, span, parent, service string }{
		{tailTestWorkspace, "1111111111111111", "", "frontend"},
		{tailTestWorkspace, "2222222222222222", "1111111111111111", "checkout"},
		{tailTestWorkspace, "4444444444444444", "2222222222222222", "payment"},
		{tailTestWorkspace, "5555555555555555", "2222222222222222", "shipping"},
		{traceTestOtherWorkspace, "3333333333333333", "", "foreign"},
	} {
		attrs := map[string]string{"loco.workspace.id": row.workspace, "loco.resource.id": resourceID}
		if appendErr := batch.Append(timestamp, traceID, row.span, row.parent, row.service, attrs); appendErr != nil {
			t.Fatal(appendErr)
		}
	}
	if sendErr := batch.Send(); sendErr != nil {
		t.Fatal(sendErr)
	}
	client := traceTestClient(t, ch)
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("Authorization", "Bearer "+traceTestReader)
	clamped := &observabilityv1.QueryTracesRequest{
		WorkspaceId: tailTestWorkspace,
		StartTime:   timestamppb.New(timestamp),
		EndTime:     timestamppb.New(timestamp),
		TraceId:     traceID,
		Limit:       100,
	}
	page, queryErr := client.QueryTraces(ctx, clamped)
	if queryErr != nil {
		t.Fatal(queryErr)
	}
	if len(page.GetSpans()) != 2 || page.GetNextCursor() == "" {
		t.Fatalf("configured maximum ignored: %v", page)
	}
	clamped.Limit = 0
	page, queryErr = client.QueryTraces(ctx, clamped)
	if queryErr != nil {
		t.Fatal(queryErr)
	}
	if len(page.GetSpans()) != 1 || page.GetNextCursor() == "" {
		t.Fatalf("configured default ignored: %v", page)
	}
	for _, tc := range []struct {
		workspace, token string
		want             []string
	}{
		{tailTestWorkspace, traceTestReader, []string{
			"1111111111111111", "2222222222222222", "4444444444444444", "5555555555555555",
		}},
		{traceTestOtherWorkspace, "other-reader", []string{"3333333333333333"}},
	} {
		t.Run(tc.workspace, func(t *testing.T) {
			req := &observabilityv1.QueryTracesRequest{
				WorkspaceId: tc.workspace,
				StartTime:   timestamppb.New(timestamp),
				EndTime:     timestamppb.New(timestamp),
				TraceId:     traceID,
				Limit:       1,
			}
			var got []string
			for range len(tc.want) + 1 {
				ctx, info := connect.NewClientContext(t.Context())
				info.RequestHeader().Set("Authorization", "Bearer "+tc.token)
				response, queryErr := client.QueryTraces(ctx, req)
				if queryErr != nil {
					t.Fatal(queryErr)
				}
				if len(response.GetSpans()) > 1 {
					t.Fatal("limit ignored")
				}
				for _, span := range response.GetSpans() {
					got = append(got, span.GetSpanId())
					if span.GetWorkspaceId() != tc.workspace || span.GetResourceId() != resourceID {
						t.Errorf("wrong attribution: %v", span)
					}
					wantParents := map[string]string{
						"2222222222222222": "1111111111111111",
						"4444444444444444": "2222222222222222",
						"5555555555555555": "2222222222222222",
					}
					if parent, ok := wantParents[span.GetSpanId()]; ok && span.GetParentSpanId() != parent {
						t.Errorf("lost parent: %v", span)
					}
				}
				if response.GetNextCursor() == "" {
					break
				}
				req.Cursor = response.GetNextCursor()
			}
			slices.Sort(got)
			if !slices.Equal(got, tc.want) {
				t.Errorf("spans = %v, want %v", got, tc.want)
			}
		})
	}
}
