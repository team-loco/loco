package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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
	"github.com/team-loco/loco/observability-proxy/pkg/auth"
	"github.com/team-loco/loco/observability-proxy/pkg/config"
)

const canonicalWorkspaceID = "01a11886-e710-7fc9-a6e9-5e0c367f2cd0"

type countingPermissions struct {
	tokenv1connect.UnimplementedTokenServiceHandler
	calls atomic.Int32
}

func (p *countingPermissions) CheckPermission(
	context.Context,
	*tokenv1.CheckPermissionRequest,
) (*tokenv1.CheckPermissionResponse, error) {
	p.calls.Add(1)
	return &tokenv1.CheckPermissionResponse{Allowed: false}, nil
}

func TestQueriesWithMalformedIDsFailBeforeThePermissionCheck(t *testing.T) {
	permissions := &countingPermissions{}
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

	now := time.Now()
	start := timestamppb.New(now.Add(-time.Minute))
	end := timestamppb.New(now)
	query := func(workspace string, resources []string) error {
		ctx, info := connect.NewClientContext(t.Context())
		info.RequestHeader().Set("Authorization", "Bearer caller")
		_, err := client.QueryLogs(ctx, &observabilityv1.QueryLogsRequest{
			WorkspaceId: workspace, ResourceIds: resources, StartTime: start, EndTime: end,
		})
		return err
	}
	for name, tc := range map[string]struct {
		workspace string
		resources []string
	}{
		"non-UUID workspace":  {workspace: "workspace"},
		"uppercase workspace": {workspace: strings.ToUpper(canonicalWorkspaceID)},
		"braced workspace":    {workspace: "{" + canonicalWorkspaceID + "}"},
		"unhyphenated":        {workspace: strings.ReplaceAll(canonicalWorkspaceID, "-", "")},
		"non-UUID resource":   {workspace: canonicalWorkspaceID, resources: []string{"resource"}},
		"uppercase resource ID": {
			workspace: canonicalWorkspaceID,
			resources: []string{strings.ToUpper(canonicalWorkspaceID)},
		},
	} {
		if err := query(tc.workspace, tc.resources); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: error = %v, want invalid_argument", name, err)
		}
	}
	if calls := permissions.calls.Load(); calls != 0 {
		t.Errorf("CheckPermission called %d times for malformed requests", calls)
	}
	if err := query(canonicalWorkspaceID, nil); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("canonical workspace: error = %v, want permission_denied", err)
	}
}
