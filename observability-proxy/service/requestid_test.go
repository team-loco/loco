package service

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
	"github.com/team-loco/loco/internal/rpc"
	"github.com/team-loco/loco/observability-proxy/pkg/auth"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
)

const forwardedRequestID = "forwarded-request-id"

type recordingPermissions struct {
	tokenv1connect.UnimplementedTokenServiceHandler
	requestIDs chan string
}

func (p *recordingPermissions) CheckPermission(
	ctx context.Context,
	_ *tokenv1.CheckPermissionRequest,
) (*tokenv1.CheckPermissionResponse, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	p.requestIDs <- info.RequestHeader().Get(rpc.RequestIDHeader)
	return nil, connect.NewError(connect.CodeUnavailable, "control plane down")
}

func TestRequestIDReachesResponseLogsAndControlPlane(t *testing.T) {
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(LogHandler{Handler: slog.NewJSONHandler(&logs, nil)}))
	t.Cleanup(func() { slog.SetDefault(previous) })

	permissions := &recordingPermissions{requestIDs: make(chan string, 1)}
	controlPlane := connect.NewServer()
	tokenv1connect.RegisterTokenServiceHandler(controlPlane, permissions)
	controlMux := http.NewServeMux()
	connecthttp.Mount(controlMux, controlPlane)
	controlHTTP := httptest.NewServer(controlMux)
	t.Cleanup(controlHTTP.Close)
	validator, err := auth.NewValidator(controlHTTP.URL, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validator.Close)

	cfg := &config.Config{DefaultLimit: 1, MaxLimit: 2, MaxTimeRange: time.Hour}
	proxyHTTP := httptest.NewServer(NewObservabilityService(nil, cfg, validator).Handler())
	t.Cleanup(proxyHTTP.Close)
	transport := connecthttp.NewTransport(proxyHTTP.Client(), proxyHTTP.URL)
	client := observabilityv1connect.NewObservabilityProxyServiceClient(connect.NewClient(transport))

	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("Authorization", "Bearer caller")
	info.RequestHeader().Set(rpc.RequestIDHeader, forwardedRequestID)
	if _, err := client.QueryMetrics(ctx, counterRequest(tailTestWorkspace, time.Now())); connect.CodeOf(
		err,
	) != connect.CodeUnavailable {
		t.Fatalf("error = %v, want unavailable", err)
	}
	if got := info.ResponseHeader().Get(rpc.RequestIDHeader); got != forwardedRequestID {
		t.Errorf("response request ID = %q", got)
	}
	if got := <-permissions.requestIDs; got != forwardedRequestID {
		t.Errorf("CheckPermission request ID = %q", got)
	}
	var record struct {
		Msg     string `json:"msg"`
		Request struct {
			RequestID string `json:"requestId"`
			Path      string `json:"path"`
		} `json:"request"`
	}
	if err := json.Unmarshal(logs.Bytes(), &record); err != nil {
		t.Fatalf("decode %q: %v", logs.String(), err)
	}
	if record.Request.RequestID != forwardedRequestID ||
		record.Request.Path != observabilityv1connect.ObservabilityProxyServiceQueryMetricsProcedure {
		t.Errorf("log record = %+v", record)
	}
}

func TestRequestIDMintedForDirectCaller(t *testing.T) {
	proxyHTTP := httptest.NewServer(NewObservabilityService(nil, &config.Config{}, nil).Handler())
	t.Cleanup(proxyHTTP.Close)
	transport := connecthttp.NewTransport(proxyHTTP.Client(), proxyHTTP.URL)
	client := observabilityv1connect.NewObservabilityProxyServiceClient(connect.NewClient(transport))

	ctx, info := connect.NewClientContext(t.Context())
	if _, err := client.QueryMetrics(ctx, counterRequest(tailTestWorkspace, time.Now())); connect.CodeOf(
		err,
	) != connect.CodeUnauthenticated {
		t.Fatalf("error = %v, want unauthenticated", err)
	}
	if info.ResponseHeader().Get(rpc.RequestIDHeader) == "" {
		t.Error("refused call has no request ID")
	}
}
