package interceptor

import (
	"context"
	"testing"

	"net/http"
	"net/http/httptest"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/otelconnect"
	"github.com/team-loco/loco/api/pkg/tracingtest"
	"github.com/team-loco/loco/api/reqctx"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	"github.com/team-loco/loco/internal/rpc"
)

const callerRequestID = "caller-request-id"

type contextTestServer struct {
	configv1connect.UnimplementedConfigServiceHandler
}

func (*contextTestServer) GetConfig(
	ctx context.Context,
	_ *configv1.GetConfigRequest,
) (*configv1.GetConfigResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	request, _ := reqctx.RequestFrom(ctx)
	if info.ResponseHeader().Get(rpc.RequestIDHeader) != request.ID {
		return nil, connect.NewError(connect.CodeInternal, "request ID was not set before the handler")
	}
	return &configv1.GetConfigResponse{}, nil
}

func TestContextInterceptorUnaryResponseHeaders(t *testing.T) {
	t.Parallel()
	server := connect.NewServer(NewContextInterceptor().Interceptor())
	configv1connect.RegisterConfigServiceHandler(server, &contextTestServer{})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	transport := connecthttp.NewTransport(srv.Client(), srv.URL)
	client := configv1connect.NewConfigServiceClient(connect.NewClient(transport))
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set(rpc.RequestIDHeader, callerRequestID)
	_, err := client.GetConfig(ctx, &configv1.GetConfigRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got := info.ResponseHeader().Get(rpc.RequestIDHeader); got != callerRequestID {
		t.Errorf("request ID = %q", got)
	}
}

func TestContextInterceptorRecordsRequestIDOnFailedRPCSpan(t *testing.T) {
	recorder := tracingtest.Record(t)
	tracing, err := otelconnect.NewServerInterceptor(otelconnect.WithoutMetrics())
	if err != nil {
		t.Fatal(err)
	}
	server := connect.NewServer(tracing, NewContextInterceptor().Interceptor())
	configv1connect.RegisterConfigServiceHandler(server, configv1connect.UnimplementedConfigServiceHandler{})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	transport := connecthttp.NewTransport(srv.Client(), srv.URL)
	client := configv1connect.NewConfigServiceClient(connect.NewClient(transport))
	ctx, info := connect.NewClientContext(t.Context())
	if _, err := client.GetConfig(ctx, &configv1.GetConfigRequest{}); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("error = %v, want unimplemented", err)
	}
	want := info.ResponseHeader().Get(rpc.RequestIDHeader)
	if want == "" {
		t.Fatal("response carried no request ID")
	}
	spans := recorder.Ended()
	if len(spans) != 1 {
		t.Fatalf("recorded %d spans, want the RPC span", len(spans))
	}
	for _, attr := range spans[0].Attributes() {
		if attr.Key == requestIDAttr {
			if got := attr.Value.AsString(); got != want {
				t.Fatalf("span request ID = %q, want %q", got, want)
			}
			return
		}
	}
	t.Fatalf("RPC span has no %s attribute", requestIDAttr)
}
