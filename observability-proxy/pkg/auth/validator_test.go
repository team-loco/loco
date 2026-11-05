package auth

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connectinprocess"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
)

const upstreamPrivateMessage = "upstream private diagnostic"

type permissionServer struct {
	tokenv1connect.UnimplementedTokenServiceHandler
	failure error
	allowed bool
	calls   atomic.Int32
}

func (s *permissionServer) CheckPermission(
	context.Context,
	*tokenv1.CheckPermissionRequest,
) (*tokenv1.CheckPermissionResponse, error) {
	s.calls.Add(1)
	if s.failure != nil {
		return nil, s.failure
	}
	return &tokenv1.CheckPermissionResponse{Allowed: s.allowed}, nil
}

func newTestValidator(t *testing.T, upstream *permissionServer) *Validator {
	t.Helper()
	server := connect.NewServer()
	tokenv1connect.RegisterTokenServiceHandler(server, upstream)
	client := tokenv1connect.NewTokenServiceClient(connect.NewClient(connectinprocess.New(server)))
	validator, err := NewValidator("http://control-plane.invalid", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	validator.client = client
	t.Cleanup(validator.Close)
	return validator
}

func checkWorkspaceRead(t *testing.T, validator *Validator) error {
	t.Helper()
	return validator.CheckPermission(
		t.Context(),
		"caller",
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		"workspace",
		tokenv1.Scope_SCOPE_READ,
	)
}

func TestPermissionErrorsAreClassified(t *testing.T) {
	cases := []struct {
		name    string
		failure error
		allowed bool
		want    error
	}{
		{
			name:    "permission denied",
			failure: connect.NewError(connect.CodePermissionDenied, upstreamPrivateMessage),
			want:    ErrPermissionDenied,
		},
		{
			name:    "upstream unavailable",
			failure: connect.NewError(connect.CodeUnavailable, upstreamPrivateMessage),
			want:    ErrPermissionCheckUnavailable,
		},
		{
			name:    "caller token rejected",
			failure: connect.NewError(connect.CodeUnauthenticated, upstreamPrivateMessage),
			want:    ErrPermissionDenied,
		},
		{
			name:    "upstream plain error",
			failure: errors.New(upstreamPrivateMessage),
			want:    ErrPermissionCheckUnavailable,
		},
		{name: "false result", want: ErrPermissionDenied},
		{name: "allowed", allowed: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			validator := newTestValidator(t, &permissionServer{failure: tc.failure, allowed: tc.allowed})
			err := checkWorkspaceRead(t, validator)
			if !errors.Is(err, tc.want) {
				t.Fatalf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestPermissionResultIsCached(t *testing.T) {
	upstream := &permissionServer{allowed: true}
	validator := newTestValidator(t, upstream)
	if err := checkWorkspaceRead(t, validator); err != nil {
		t.Fatal(err)
	}
	validator.cache.Wait()
	if err := checkWorkspaceRead(t, validator); err != nil {
		t.Fatal(err)
	}
	if calls := upstream.calls.Load(); calls != 1 {
		t.Fatalf("control plane calls = %d, want 1", calls)
	}
}
