package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	tokenv1 "github.com/team-loco/loco/gen/go/loco/token/v1"
	"github.com/team-loco/loco/gen/go/loco/token/v1/tokenv1connect"
	"github.com/team-loco/loco/observability-proxy/pkg/cache"
)

const (
	testQueryCredential = "signed-provider-session"
	testWorkspaceID     = "00000000-0000-4000-8000-000000000000"
)

var errTestSubjectCredentialRejected = connect.NewError(connect.CodeUnauthenticated, "test credential rejected")

type subjectTokenServer struct {
	tokenv1connect.UnimplementedTokenServiceHandler
}

func (*subjectTokenServer) CheckPermission(
	ctx context.Context,
	req *tokenv1.CheckPermissionRequest,
) (*tokenv1.CheckPermissionResponse, error) {
	info, ok := connect.CallInfoForServerContext(ctx)
	if !ok {
		return nil, errTestSubjectCredentialRejected
	}
	credential := info.RequestHeader().Get("Authorization")
	if credential != "Bearer "+testQueryCredential {
		return nil, errTestSubjectCredentialRejected
	}
	allowed := credential == "Bearer "+testQueryCredential && req.GetScope() == tokenv1.Scope_SCOPE_READ
	return &tokenv1.CheckPermissionResponse{Allowed: allowed}, nil
}

func TestValidatorAuthenticatesPermissionChecksAsTheirSubject(t *testing.T) {
	service := &subjectTokenServer{}
	rpcServer := connect.NewServer()
	tokenv1connect.RegisterTokenServiceHandler(rpcServer, service)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpcServer)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	store, err := cache.NewMemory(time.Minute)
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			t.Errorf("close cache: %v", closeErr)
		}
	})
	validator := NewValidator(server.URL, store)
	if checkErr := validator.CheckPermission(
		t.Context(),
		testQueryCredential,
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		testWorkspaceID,
		tokenv1.Scope_SCOPE_READ,
	); checkErr != nil {
		t.Fatalf("subject read check: %v", checkErr)
	}
	if checkErr := validator.CheckPermission(
		t.Context(),
		testQueryCredential,
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		testWorkspaceID,
		tokenv1.Scope_SCOPE_WRITE,
	); connect.CodeOf(
		checkErr,
	) != connect.CodePermissionDenied {
		t.Fatalf("write without permission: %v", checkErr)
	}
	if checkErr := validator.CheckPermission(
		t.Context(),
		"invalid-caller",
		tokenv1.EntityType_ENTITY_TYPE_WORKSPACE,
		testWorkspaceID,
		tokenv1.Scope_SCOPE_READ,
	); connect.CodeOf(
		checkErr,
	) != connect.CodePermissionDenied {
		t.Fatalf("rejected caller: %v", checkErr)
	}
}
