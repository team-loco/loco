package cmdutil

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/internal/rpc"
)

const (
	testTargetWorkspaceID = "00000000-0000-7000-8000-000000000010"
	testTargetServiceID   = "00000000-0000-7000-8000-000000000011"
	testTargetService     = "web"
)

type namedResources struct {
	resourcev1connect.UnimplementedResourceServiceHandler
}

func (namedResources) GetResource(
	_ context.Context,
	req *resourcev1.GetResourceRequest,
) (*resourcev1.GetResourceResponse, error) {
	key := req.GetNameKey()
	if key.GetWorkspaceId() != testTargetWorkspaceID || key.GetName() != testTargetService {
		return nil, connect.NewError(connect.CodeNotFound, "resource not found")
	}
	resource := &resourcev1.Resource{Id: testTargetServiceID, Name: testTargetService}
	return &resourcev1.GetResourceResponse{Resource: resource}, nil
}

func newServiceTarget(t *testing.T) Target {
	t.Helper()
	server := connect.NewServer()
	resourcev1connect.RegisterResourceServiceHandler(server, namedResources{})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	clients := rpc.NewHTTPClients(srv.Client(), srv.URL, "")
	var target Target
	target.Clients = clients
	target.WorkspaceID = testTargetWorkspaceID
	return target
}

func TestTargetServiceLooksUpTheNameInTheWorkspace(t *testing.T) {
	target := newServiceTarget(t)
	service, err := target.Service(context.Background(), testTargetService)
	if err != nil {
		t.Fatalf("Service(%q): %v", testTargetService, err)
	}
	if service.GetId() != testTargetServiceID {
		t.Fatalf("Service(%q) id = %q, want %q", testTargetService, service.GetId(), testTargetServiceID)
	}
}

func TestTargetServiceReportsAMissingService(t *testing.T) {
	target := newServiceTarget(t)
	_, err := target.Service(context.Background(), "api")
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("Service(%q) error = %v, want a not found code", "api", err)
	}
	if !strings.Contains(err.Error(), `"api"`) {
		t.Fatalf("Service(%q) error = %q, want it to name the service", "api", err)
	}
}
