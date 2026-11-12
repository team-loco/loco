package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	observabilityv1 "github.com/team-loco/loco/gen/go/loco/observability/v1"
	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	"github.com/team-loco/loco/internal/rpc"
)

const gatewayWorkspace = "11111111-2222-3333-4444-555555555555"
const gatewayCancelTimeout = 3 * time.Second

type gatewayTelemetry struct {
	observabilityv1connect.UnimplementedObservabilityProxyServiceHandler
	canceled chan struct{}
}

func gatewayPermission(ctx context.Context, workspace string) error {
	info, _ := connect.CallInfoForServerContext(ctx)
	if info.RequestHeader().Get("Authorization") == "" {
		return connect.NewError(connect.CodeUnauthenticated, "missing token")
	}
	if info.RequestHeader().Get("Authorization") != "Bearer reader" || workspace != gatewayWorkspace {
		return connect.NewError(connect.CodePermissionDenied, "permission denied")
	}
	return nil
}

func (*gatewayTelemetry) QueryLogs(
	ctx context.Context,
	req *observabilityv1.QueryLogsRequest,
) (*observabilityv1.QueryLogsResponse, error) {
	if err := gatewayPermission(ctx, req.GetWorkspaceId()); err != nil {
		return nil, err
	}
	timestamp := timestamppb.Now()
	return &observabilityv1.QueryLogsResponse{
		Entries: []*observabilityv1.LogEntry{
			{
				Body:               "regional history",
				Timestamp:          timestamp,
				ResourceAttributes: map[string]string{"loco.region": "us-east-1"},
			},
		},
		NextCursor: "opaque-next-page",
	}, nil
}

func (s *gatewayTelemetry) TailLogs(
	ctx context.Context,
	req *observabilityv1.TailLogsRequest,
	stream observabilityv1connect.ObservabilityProxyServiceTailLogsServerStream,
) error {
	if err := gatewayPermission(ctx, req.GetWorkspaceId()); err != nil {
		return err
	}
	entry := &observabilityv1.TailLogsResponse{
		Event: &observabilityv1.TailLogsResponse_Entry{
			Entry: &observabilityv1.LogEntry{Body: "streamed before upstream closes"},
		},
	}
	if err := stream.Send(entry); err != nil {
		return err
	}
	<-ctx.Done()
	close(s.canceled)
	return nil
}

func TestRegionalGatewayAuthenticatedQueryAndTailCancellation(t *testing.T) {
	service := &gatewayTelemetry{canceled: make(chan struct{})}
	rpcServer := connect.NewServer()
	observabilityv1connect.RegisterObservabilityProxyServiceHandler(rpcServer, service)
	upstreamMux := http.NewServeMux()
	connecthttp.Mount(upstreamMux, rpcServer)
	upstreamHandler := withCORS([]string{configuredOrigin}, false)(upstreamMux)
	upstream := httptest.NewServer(upstreamHandler)
	t.Cleanup(upstream.Close)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	target := parseObservabilityProxyURL(upstream.URL)
	mountObservabilityProxy(mux, target)
	handler := withCORS([]string{configuredOrigin}, false)(mux)
	gateway := httptest.NewServer(handler)
	t.Cleanup(gateway.Close)
	for _, tc := range []struct {
		name, token, workspace string
		code                   connect.Code
	}{
		{name: "authorized", token: "reader", workspace: gatewayWorkspace},
		{name: "unauthenticated", workspace: gatewayWorkspace, code: connect.CodeUnauthenticated},
		{name: "wrong workspace", token: "reader", workspace: "other", code: connect.CodePermissionDenied},
		{name: "wrong token", token: "other", workspace: gatewayWorkspace, code: connect.CodePermissionDenied},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clients := rpc.NewHTTPClients(gateway.Client(), gateway.URL, tc.token)
			ctx, info := connect.NewClientContext(t.Context())
			info.RequestHeader().Set("Origin", configuredOrigin)
			req := &observabilityv1.QueryLogsRequest{WorkspaceId: tc.workspace}
			response, err := clients.ObservabilityProxy.QueryLogs(ctx, req)
			if tc.code != 0 {
				if connect.CodeOf(err) != tc.code {
					t.Fatalf("error = %v, want %v", err, tc.code)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(response.GetEntries()) != 1 || response.GetNextCursor() != "opaque-next-page" {
				t.Fatalf("response = %v", response)
			}
			if origins := info.ResponseHeader().
				Values("Access-Control-Allow-Origin"); len(origins) != 1 ||
				origins[0] != configuredOrigin {
				t.Fatalf("CORS origins = %v", origins)
			}
		})
	}
	clients := rpc.NewHTTPClients(gateway.Client(), gateway.URL, "reader")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stream, err := clients.ObservabilityProxy.TailLogs(
		ctx,
		&observabilityv1.TailLogsRequest{WorkspaceId: gatewayWorkspace},
	)
	if err != nil {
		t.Fatal(err)
	}
	message, err := stream.Receive()
	if err != nil || message.GetEntry().GetBody() != "streamed before upstream closes" {
		t.Fatalf("stream entry = %v, %v", message, err)
	}
	cancel()
	if closeErr := stream.Close(); closeErr != nil && connect.CodeOf(closeErr) != connect.CodeCanceled {
		t.Fatal(closeErr)
	}
	select {
	case <-service.canceled:
	case <-time.After(gatewayCancelTimeout):
		t.Fatal("gateway did not cancel upstream tail")
	}
}

func TestRegionalGatewayUnavailableWithoutBackend(t *testing.T) {
	mux := http.NewServeMux()
	mountObservabilityProxy(mux, nil)
	gateway := httptest.NewServer(mux)
	defer gateway.Close()
	clients := rpc.NewHTTPClients(gateway.Client(), gateway.URL, "reader")
	_, err := clients.ObservabilityProxy.QueryLogs(
		t.Context(),
		&observabilityv1.QueryLogsRequest{WorkspaceId: gatewayWorkspace},
	)
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("error = %v", err)
	}
}

func TestRegionalGatewayConfiguration(t *testing.T) {
	if parseObservabilityProxyURL("") != nil {
		t.Fatal("empty URL must disable forwarding")
	}
	for _, raw := range []string{"http://127.0.0.1:8090", "https://proxy.internal/"} {
		if parseObservabilityProxyURL(raw) == nil {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{
		"clickhouse://db:9000", "http://user:secret@proxy", "http://proxy/path",
		"http://proxy?secret=token", "http://proxy#fragment", "http://",
	} {
		t.Run(raw, func(t *testing.T) {
			defer func() {
				value := recover()
				if value == nil {
					t.Fatalf("panic = %v", value)
				}
				err, ok := value.(error)
				if !ok || !strings.Contains(err.Error(), "OBSERVABILITY_PROXY_URL") {
					t.Fatalf("panic = %v", value)
				}
			}()
			parseObservabilityProxyURL(raw)
		})
	}
}

func (*gatewayTelemetry) ListTraces(
	ctx context.Context,
	req *observabilityv1.ListTracesRequest,
) (*observabilityv1.ListTracesResponse, error) {
	if err := gatewayPermission(ctx, req.GetScope().GetWorkspaceId()); err != nil {
		return nil, err
	}
	return &observabilityv1.ListTracesResponse{
		TotalCount: 42,
		Summaries: []*observabilityv1.TraceSummary{{
			TraceId: req.GetTraceId(), RootSpanName: strings.Join(req.GetRootNameTerms(), " "),
			ServiceName: req.GetScope().GetEnvironmentId(), ResourceId: req.GetScope().GetResourceIds()[0],
			StartTime: req.GetStartTime(), DurationNanos: req.GetMinDurationNanos(),
			Status: req.GetStatus(), SpanCount: uint64(req.GetOffset()),
		}},
	}, nil
}

func TestTraceSummaryPassthrough(t *testing.T) {
	rpcServer := connect.NewServer()
	observabilityv1connect.RegisterObservabilityProxyServiceHandler(rpcServer, &gatewayTelemetry{})
	upstreamMux := http.NewServeMux()
	connecthttp.Mount(upstreamMux, rpcServer)
	upstream := httptest.NewServer(upstreamMux)
	t.Cleanup(upstream.Close)
	mux := http.NewServeMux()
	mountObservabilityProxy(mux, parseObservabilityProxyURL(upstream.URL))
	gateway := httptest.NewServer(mux)
	t.Cleanup(gateway.Close)
	ctx := t.Context()
	req := &observabilityv1.ListTracesRequest{
		Scope: &observabilityv1.TraceScope{
			WorkspaceId:   gatewayWorkspace,
			ResourceIds:   []string{"resource"},
			EnvironmentId: "environment",
		},
		TraceId:   "0123456789abcdef0123456789abcdef",
		StartTime: timestamppb.Now(),
		EndTime:   timestamppb.Now(),
		Limit:     1,
		MinDurationNanos: proto.Uint64(
			9007199254740993,
		),
		Status:        observabilityv1.TraceStatus_TRACE_STATUS_ERROR,
		RootNameTerms: []string{"GET", "/cart"},
		Offset:        7,
	}
	clients := rpc.NewHTTPClients(gateway.Client(), gateway.URL, "reader")
	response, err := clients.ObservabilityProxy.ListTraces(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if response.GetTotalCount() != 42 || len(response.GetSummaries()) != 1 {
		t.Fatalf("response: %v", response)
	}
	summary := response.GetSummaries()[0]
	if summary.GetTraceId() != req.GetTraceId() || summary.GetRootSpanName() != "GET /cart" ||
		summary.GetServiceName() != req.GetScope().
			GetEnvironmentId() ||
		summary.GetResourceId() != req.GetScope().GetResourceIds()[0] ||
		!proto.Equal(
			summary.GetStartTime(),
			req.GetStartTime(),
		) || summary.GetDurationNanos() != req.GetMinDurationNanos() ||
		summary.GetStatus() != observabilityv1.TraceStatus_TRACE_STATUS_ERROR || summary.GetSpanCount() != 7 {
		t.Fatalf("lost summary fields: %v", summary)
	}
	traceReq := &observabilityv1.GetTraceRequest{Scope: req.GetScope(), TraceId: req.GetTraceId()}
	trace, err := clients.ObservabilityProxy.GetTrace(ctx, traceReq)
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.GetSpans()) != 1 || trace.GetSpans()[0].GetTraceId() != req.GetTraceId() ||
		trace.GetSpans()[0].GetWorkspaceId() != gatewayWorkspace {
		t.Fatalf("trace: %v", trace)
	}
	for _, token := range []string{"", "wrong-reader"} {
		_, err := rpc.NewHTTPClients(gateway.Client(), gateway.URL, token).ObservabilityProxy.ListTraces(ctx, req)
		want := connect.CodePermissionDenied
		if token == "" {
			want = connect.CodeUnauthenticated
		}
		if connect.CodeOf(err) != want {
			t.Fatalf("token %q: %v", token, err)
		}
		_, err = rpc.NewHTTPClients(gateway.Client(), gateway.URL, token).ObservabilityProxy.GetTrace(ctx, traceReq)
		if connect.CodeOf(err) != want {
			t.Fatalf("get trace token %q: %v", token, err)
		}
	}
}

func (*gatewayTelemetry) GetTrace(
	ctx context.Context,
	req *observabilityv1.GetTraceRequest,
) (*observabilityv1.GetTraceResponse, error) {
	if err := gatewayPermission(ctx, req.GetScope().GetWorkspaceId()); err != nil {
		return nil, err
	}
	return &observabilityv1.GetTraceResponse{
		Spans: []*observabilityv1.TraceSpan{{TraceId: req.GetTraceId(), WorkspaceId: req.GetScope().GetWorkspaceId()}},
	}, nil
}
