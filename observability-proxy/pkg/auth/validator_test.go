package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	configv1 "github.com/team-loco/loco/gen/go/loco/config/v1"
	"github.com/team-loco/loco/gen/go/loco/config/v1/configv1connect"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
	"github.com/team-loco/loco/observability-proxy/pkg/cache"
)

const upstreamPrivateMessage = "upstream private diagnostic"

type permissionServer struct {
	tokenv1connect.UnimplementedTokenServiceHandler
	failure error
	allowed bool
}

func (s *permissionServer) CheckPermission(
	context.Context,
	*tokenv1.CheckPermissionRequest,
) (*tokenv1.CheckPermissionResponse, error) {
	if s.failure != nil {
		return nil, s.failure
	}
	return &tokenv1.CheckPermissionResponse{Allowed: s.allowed}, nil
}

type permissionRelay struct {
	configv1connect.UnimplementedConfigServiceHandler
	validator *Validator
}

func (r *permissionRelay) GetConfig(
	ctx context.Context,
	_ *configv1.GetConfigRequest,
) (*configv1.GetConfigResponse, error) {
	err := r.validator.CheckPermission(
		ctx,
		"caller",
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		"workspace",
		tokenv1.Scope_SCOPE_READ,
	)
	if err != nil {
		return nil, err
	}
	return &configv1.GetConfigResponse{}, nil
}

func TestPermissionErrorsHaveExplicitLocalCodes(t *testing.T) {
	cases := []struct {
		name    string
		failure error
		allowed bool
		code    connect.Code
		message string
	}{
		{
			name:    "permission denied",
			failure: connect.NewError(connect.CodePermissionDenied, upstreamPrivateMessage),
			code:    connect.CodePermissionDenied,
			message: errPermissionDenied.Message(),
		},
		{
			name:    "upstream unavailable",
			failure: connect.NewError(connect.CodeUnavailable, upstreamPrivateMessage),
			code:    connect.CodeUnavailable,
			message: errPermissionCheckUnavailable.Message(),
		},
		{
			name:    "caller token rejected",
			failure: connect.NewError(connect.CodeUnauthenticated, upstreamPrivateMessage),
			code:    connect.CodePermissionDenied,
			message: errPermissionDenied.Message(),
		},
		{
			name:    "upstream plain error",
			failure: errors.New(upstreamPrivateMessage),
			code:    connect.CodeUnavailable,
			message: errPermissionCheckUnavailable.Message(),
		},
		{name: "false result", code: connect.CodePermissionDenied, message: errPermissionDenied.Message()},
		{name: "allowed", allowed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := connect.NewServer()
			tokenv1connect.RegisterTokenServiceHandler(
				server,
				&permissionServer{failure: tc.failure, allowed: tc.allowed},
			)
			transport := connectinprocess.New(server)
			client := tokenv1connect.NewTokenServiceClient(connect.NewClient(transport))
			memory, err := cache.NewMemory(time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if closeErr := memory.Close(); closeErr != nil {
					t.Error(closeErr)
				}
			})
			validator := &Validator{client: client, cache: memory}
			err = validator.CheckPermission(
				t.Context(),
				"caller",
				tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
				"workspace",
				tokenv1.Scope_SCOPE_READ,
			)
			if tc.allowed {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			failure, ok := errors.AsType[*connect.Error](err)
			if !ok {
				t.Fatalf("error = %v", err)
			}
			if failure.Code() != tc.code || failure.Message() != tc.message {
				t.Fatalf("error = %v, want %s: %s", failure, tc.code, tc.message)
			}
			relayServer := connect.NewServer()
			configv1connect.RegisterConfigServiceHandler(relayServer, &permissionRelay{validator: validator})
			relayTransport := connectinprocess.New(relayServer)
			relayClient := configv1connect.NewConfigServiceClient(connect.NewClient(relayTransport))
			_, wireErr := relayClient.GetConfig(t.Context(), &configv1.GetConfigRequest{})
			publicError, isConnect := errors.AsType[*connect.Error](wireErr)
			if !isConnect || publicError.Code() != tc.code || publicError.Message() != tc.message {
				t.Fatalf("relayed wire error = %v", wireErr)
			}
		})
	}
}
