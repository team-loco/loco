package service

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/team-loco/loco/gen/go/loco/observability/v1/observabilityv1connect"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
	"github.com/team-loco/loco/observability-proxy/pkg/auth"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
)

type failingPermissions struct {
	tokenv1connect.UnimplementedTokenServiceHandler
	failure error
}

func (p *failingPermissions) CheckPermission(
	context.Context,
	*tokenv1.CheckPermissionRequest,
) (*tokenv1.CheckPermissionResponse, error) {
	return nil, p.failure
}

func TestAuthorizeReadLogsErrorOnlyWhenTheCheckFails(t *testing.T) {
	cases := map[string]struct {
		failure error
		code    connect.Code
		logged  bool
	}{
		"permission denied": {
			failure: connect.NewError(connect.CodePermissionDenied, "private"),
			code:    connect.CodePermissionDenied,
		},
		"unauthenticated": {
			failure: connect.NewError(connect.CodeUnauthenticated, "private"),
			code:    connect.CodePermissionDenied,
		},
		"unavailable": {
			failure: connect.NewError(connect.CodeUnavailable, "private"),
			code:    connect.CodeUnavailable,
			logged:  true,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var logs bytes.Buffer
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
			t.Cleanup(func() { slog.SetDefault(previous) })

			controlPlane := connect.NewServer()
			tokenv1connect.RegisterTokenServiceHandler(controlPlane, &failingPermissions{failure: tc.failure})
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
			_, err = client.QueryMetrics(ctx, counterRequest(tailTestWorkspace, time.Now()))
			if connect.CodeOf(err) != tc.code {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
			if got := strings.Contains(logs.String(), "level=ERROR"); got != tc.logged {
				t.Errorf("ERROR logged = %t, want %t; log: %s", got, tc.logged, logs.String())
			}
		})
	}
}
