package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/validate"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/interceptor"
	"github.com/team-loco/loco/api/pkg/guardrails"
	"github.com/team-loco/loco/api/reqctx"
	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	"github.com/team-loco/loco/internal/bearer"
	chClient "github.com/team-loco/loco/internal/clickhouse"
)

const (
	observabilityTestWorkspace = "01a11886-e710-7fc9-a6e9-5e0c367f2cd0"
	observabilityTestTraceID   = "0123456789abcdef0123456789abcdef"
	unreachableClickHouseURL   = "clickhouse://127.0.0.1:1"
	unreachableClickHouseDB    = "loco_obs"
	observabilityTestConns     = 2
	observabilityTestTimeout   = 10 * time.Second
	observabilityTestCaller    = "caller"
)

var observabilityTestConfig = ObservabilityConfig{
	Limits:          guardrails.Limits{DefaultLimit: 1, MaxLimit: 2, MaxTimeRange: time.Hour},
	MaxTailDuration: time.Minute,
}

type observabilityClient = observabilityv1connect.ObservabilityProxyServiceClient

// observabilityTestClient serves server behind the authenticated server's error and validation
// interceptors. A bearer token names its caller's scopes in callers; an unknown token is a caller
// with no scopes and a missing one has no principal.
func observabilityTestClient(
	t *testing.T,
	server *ObservabilityServer,
	callers map[string][]genDb.EntityScope,
) observabilityClient {
	t.Helper()
	principals := func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			info, _ := connect.CallInfoForServerContext(ctx)
			if token, ok := bearer.FromHeader(info.RequestHeader()); ok {
				ctx = reqctx.WithPrincipal(ctx, reqctx.Principal{Scopes: callers[token]})
			}
			return next(ctx, spec, stream)
		}
	}
	rpcServer := connect.NewServer(interceptor.NewErrorInterceptor(), principals, validate.NewServerInterceptor())
	observabilityv1connect.RegisterObservabilityProxyServiceHandler(rpcServer, server)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpcServer)
	httpServer := httptest.NewServer(mux)
	t.Cleanup(httpServer.Close)
	transport := connecthttp.NewTransport(httpServer.Client(), httpServer.URL)
	return observabilityv1connect.NewObservabilityProxyServiceClient(connect.NewClient(transport))
}

func callerContext(t *testing.T, token string) context.Context {
	t.Helper()
	ctx, info := connect.NewClientContext(t.Context())
	if token != "" {
		info.RequestHeader().Set("Authorization", bearer.Header(token))
	}
	return ctx
}

// unreachableClickHouse fails every query, so a request that passes validation and authorization
// ends as Internal instead of the code its own checks would return.
func unreachableClickHouse(t *testing.T) *chClient.Client {
	t.Helper()
	client, err := chClient.NewClient(
		unreachableClickHouseURL,
		unreachableClickHouseDB,
		observabilityTestConns,
		observabilityTestTimeout,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	return client
}

// unreachableAuthorizer fails every lookup it needs the database for, so a request that reaches
// it ends as Internal.
func unreachableAuthorizer(t *testing.T) *authz.Authorizer {
	t.Helper()
	pool := dbtest.ClosedPool(t)
	return authz.New(pool, genDb.New(pool))
}

func TestObservabilityIsUnavailableWithoutClickHouse(t *testing.T) {
	client := observabilityTestClient(
		t,
		NewObservabilityServer(nil, unreachableAuthorizer(t), observabilityTestConfig),
		nil,
	)
	now := time.Now()
	_, err := client.QueryLogs(callerContext(t, observabilityTestCaller), &observabilityv1.QueryLogsRequest{
		WorkspaceId: observabilityTestWorkspace,
		StartTime:   timestamppb.New(now.Add(-time.Minute)),
		EndTime:     timestamppb.New(now),
	})
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("error = %v, want unavailable", err)
	}
}

func TestQueriesWithMalformedIDsFailBeforeThePermissionCheck(t *testing.T) {
	server := NewObservabilityServer(unreachableClickHouse(t), unreachableAuthorizer(t), observabilityTestConfig)
	client := observabilityTestClient(t, server, nil)
	now := time.Now()
	query := func(workspace string, resources []string) error {
		_, err := client.QueryLogs(callerContext(t, observabilityTestCaller), &observabilityv1.QueryLogsRequest{
			WorkspaceId: workspace,
			ResourceIds: resources,
			StartTime:   timestamppb.New(now.Add(-time.Minute)),
			EndTime:     timestamppb.New(now),
		})
		return err
	}
	for name, tc := range map[string]struct {
		workspace string
		resources []string
	}{
		"non-UUID workspace":  {workspace: "not-a-uuid"},
		"uppercase workspace": {workspace: strings.ToUpper(observabilityTestWorkspace)},
		"braced workspace":    {workspace: "{" + observabilityTestWorkspace + "}"},
		"unhyphenated":        {workspace: strings.ReplaceAll(observabilityTestWorkspace, "-", "")},
		"non-UUID resource":   {workspace: observabilityTestWorkspace, resources: []string{"resource"}},
		"uppercase resource ID": {
			workspace: observabilityTestWorkspace,
			resources: []string{strings.ToUpper(observabilityTestWorkspace)},
		},
	} {
		if err := query(tc.workspace, tc.resources); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: error = %v, want invalid_argument", name, err)
		}
	}
	// A well-formed request reaches the permission check, whose database is unreachable.
	if err := query(observabilityTestWorkspace, nil); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("canonical workspace: error = %v, want internal", err)
	}
}

func TestTraceSummaryValidation(t *testing.T) {
	server := NewObservabilityServer(unreachableClickHouse(t), unreachableAuthorizer(t), observabilityTestConfig)
	client := observabilityTestClient(t, server, nil)
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
			req := &observabilityv1.ListTracesRequest{
				Scope:     &observabilityv1.TraceScope{WorkspaceId: observabilityTestWorkspace},
				StartTime: timestamppb.New(time.Now().Add(-time.Minute)),
				EndTime:   timestamppb.Now(),
			}
			tc.change(req)
			_, err := client.ListTraces(callerContext(t, observabilityTestCaller), req)
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("error: %v", err)
			}
		})
	}
}

func TestGetTraceRequiresTraceID(t *testing.T) {
	server := NewObservabilityServer(unreachableClickHouse(t), unreachableAuthorizer(t), observabilityTestConfig)
	client := observabilityTestClient(t, server, nil)
	malformed := []string{"", "abcd", "00000000000000000000000000000000", strings.ToUpper(observabilityTestTraceID)}
	for _, id := range malformed {
		_, err := client.GetTrace(callerContext(t, observabilityTestCaller), &observabilityv1.GetTraceRequest{
			Scope:   &observabilityv1.TraceScope{WorkspaceId: observabilityTestWorkspace},
			TraceId: id,
		})
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("trace %q: %v", id, err)
		}
	}
}

func TestTailLoopEndsTheStreamWhenAPollFails(t *testing.T) {
	server := NewObservabilityServer(unreachableClickHouse(t), nil, ObservabilityConfig{})
	start := time.Now()
	err := server.tailLoop(
		t.Context(),
		tailLogQuery(&observabilityv1.TailLogsRequest{}, time.Now()),
		func(*observabilityv1.TailLogsResponse) error { return nil },
	)
	if err == nil {
		t.Fatal("failed poll ended the tail without an error")
	}
	if elapsed := time.Since(start); elapsed > tailLoopReturnLimit {
		t.Fatalf("tail returned after %v, want under %v", elapsed, tailLoopReturnLimit)
	}
}
