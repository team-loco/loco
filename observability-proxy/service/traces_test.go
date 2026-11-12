package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
	"github.com/team-loco/loco/observability-proxy/pkg/auth"
	chClient "github.com/team-loco/loco/observability-proxy/pkg/clickhouse"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
)

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
	if req.GetScope() != tokenv1.Scope_SCOPE_READ {
		p.t.Errorf("wrong permission request: %v", req)
	}
	info, _ := connect.CallInfoForServerContext(ctx)
	authorization := info.RequestHeader().Get("Authorization")
	var allowed bool
	switch req.GetEntityType() {
	case tokenv1.EntityType_ENTITY_TYPE_WORKSPACE:
		allowed = (authorization == "Bearer workspace-reader" && req.GetEntityId() == tailTestWorkspace) ||
			(authorization == "Bearer other-reader" && req.GetEntityId() == traceTestOtherWorkspace)
	case tokenv1.EntityType_ENTITY_TYPE_RESOURCE:
		allowed = authorization == "Bearer "+resourceTestReader && req.GetEntityId() == counterHTTPResource
	default:
		p.t.Errorf("wrong permission request: %v", req)
	}
	return &tokenv1.CheckPermissionResponse{Allowed: allowed}, nil
}

func traceTestClient(
	t *testing.T,
	clickhouse *chClient.Client,
) observabilityv1connect.ObservabilityProxyServiceClient {
	t.Helper()
	controlPlane := connect.NewServer()
	tokenv1connect.RegisterTokenServiceHandler(controlPlane, &tracePermissions{t: t})
	controlMux := http.NewServeMux()
	connecthttp.Mount(controlMux, controlPlane)
	controlHTTP := httptest.NewServer(controlMux)
	t.Cleanup(controlHTTP.Close)
	validator, err := auth.NewValidator(controlHTTP.URL, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validator.Close)
	cfg := &config.Config{DefaultLimit: 1, MaxLimit: 2, MaxTimeRange: time.Hour, MaxTailDuration: time.Minute}
	service := NewObservabilityService(clickhouse, cfg, validator)
	proxyHTTP := httptest.NewServer(service.Handler())
	t.Cleanup(proxyHTTP.Close)
	transport := connecthttp.NewTransport(proxyHTTP.Client(), proxyHTTP.URL)
	return observabilityv1connect.NewObservabilityProxyServiceClient(connect.NewClient(transport))
}

func TestTraceSummaryValidation(t *testing.T) {
	client := traceTestClient(t, nil)
	for _, tc := range []struct {
		name   string
		change func(*observabilityv1.ListTracesRequest)
	}{
		{"negative offset", func(r *observabilityv1.ListTracesRequest) { r.Offset = -1 }},
		{"invalid sort", func(r *observabilityv1.ListTracesRequest) {
			r.Sort = observabilityv1.TraceSummarySort(99)
		}},
		{"invalid status", func(r *observabilityv1.ListTracesRequest) {
			r.Status = observabilityv1.TraceStatus(99)
		}},
		{"inverted duration", func(r *observabilityv1.ListTracesRequest) {
			r.MinDurationNanos = proto.Uint64(2)
			r.MaxDurationNanos = proto.Uint64(1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, info := connect.NewClientContext(t.Context())
			info.RequestHeader().Set("Authorization", "Bearer "+traceTestReader)
			req := &observabilityv1.ListTracesRequest{
				Scope:     &observabilityv1.TraceScope{WorkspaceId: tailTestWorkspace},
				StartTime: timestamppb.New(time.Now().Add(-time.Minute)),
				EndTime:   timestamppb.Now(),
			}
			tc.change(req)
			_, err := client.ListTraces(ctx, req)
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("error: %v", err)
			}
		})
	}
}

func TestTraceRPCAuthenticationAndErrors(t *testing.T) {
	client := traceTestClient(t, unavailableClickHouse(t))
	for _, tc := range []struct {
		name, token, workspace string
		code                   connect.Code
	}{
		{"missing token", "", tailTestWorkspace, connect.CodeUnauthenticated},
		{"wrong token", "wrong", tailTestWorkspace, connect.CodePermissionDenied},
		{"wrong workspace", traceTestReader, traceTestOtherWorkspace, connect.CodePermissionDenied},
		{"empty workspace", traceTestReader, "", connect.CodeInvalidArgument},
		{"storage failure", traceTestReader, tailTestWorkspace, connect.CodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, info := connect.NewClientContext(t.Context())
			if tc.token != "" {
				info.RequestHeader().Set("Authorization", "Bearer "+tc.token)
			}
			scope := &observabilityv1.TraceScope{WorkspaceId: tc.workspace}
			now := time.Now()
			_, err := client.ListTraces(
				ctx,
				&observabilityv1.ListTracesRequest{
					Scope:     scope,
					StartTime: timestamppb.New(now.Add(-time.Minute)),
					EndTime:   timestamppb.New(now),
				},
			)
			if connect.CodeOf(err) != tc.code {
				t.Fatalf("ListTraces: %v", err)
			}
			_, err = client.GetTrace(
				ctx,
				&observabilityv1.GetTraceRequest{Scope: scope, TraceId: "0123456789abcdef0123456789abcdef"},
			)
			if connect.CodeOf(err) != tc.code {
				t.Fatalf("GetTrace: %v", err)
			}
			if tc.code == connect.CodeInternal && err.Error() != "internal: query failed" {
				t.Fatalf("private diagnostic: %v", err)
			}
		})
	}
}

func TestGetTraceRequiresTraceID(t *testing.T) {
	client := traceTestClient(t, nil)
	for _, id := range []string{"", "abcd", "00000000000000000000000000000000", "0123456789ABCDEF0123456789ABCDEF"} {
		ctx, info := connect.NewClientContext(t.Context())
		info.RequestHeader().Set("Authorization", "Bearer "+traceTestReader)
		_, err := client.GetTrace(
			ctx,
			&observabilityv1.GetTraceRequest{
				Scope:   &observabilityv1.TraceScope{WorkspaceId: tailTestWorkspace},
				TraceId: id,
			},
		)
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("trace %q: %v", id, err)
		}
	}
}

func TestTraceHTTPReadsWholeTrace(t *testing.T) {
	ch := newTestClickHouse(t)
	const id = "0123456789abcdef0123456789abcdef"
	now := time.Now().Add(-time.Minute)
	batch, prepareErr := ch.Conn().
		PrepareBatch(t.Context(), "INSERT INTO otel_traces (Timestamp,TraceId,SpanId,ResourceAttributes)")
	if prepareErr != nil {
		t.Fatal(prepareErr)
	}
	for _, workspace := range []string{tailTestWorkspace, traceTestOtherWorkspace} {
		for i := range 4 {
			if err := batch.Append(
				now,
				id,
				fmt.Sprintf("%016x", i+1),
				map[string]string{"loco.workspace.id": workspace},
			); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := batch.Send(); err != nil {
		t.Fatal(err)
	}
	client := traceTestClient(t, ch)
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("Authorization", "Bearer "+traceTestReader)
	scope := &observabilityv1.TraceScope{WorkspaceId: tailTestWorkspace}
	trace, err := client.GetTrace(ctx, &observabilityv1.GetTraceRequest{Scope: scope, TraceId: id})
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.GetSpans()) != 4 {
		t.Fatalf("GetTrace truncated to list limit: %v", trace)
	}
	list, err := client.ListTraces(
		ctx,
		&observabilityv1.ListTracesRequest{
			Scope:     scope,
			StartTime: timestamppb.New(now),
			EndTime:   timestamppb.New(now),
			Limit:     1,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if list.GetTotalCount() != 1 || len(list.GetSummaries()) != 1 || list.GetSummaries()[0].GetSpanCount() != 4 {
		t.Fatalf("ListTraces: %v", list)
	}
}
