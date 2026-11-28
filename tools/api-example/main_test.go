package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	"github.com/team-loco/loco/internal/rpc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type watchFixture struct {
	deploymentv1connect.UnimplementedDeploymentServiceHandler
	mu       sync.Mutex
	requests []int64
	reject   bool
}

func (f *watchFixture) WatchDeployment(
	ctx context.Context,
	req *deploymentv1.WatchDeploymentRequest,
	stream deploymentv1connect.DeploymentServiceWatchDeploymentServerStream,
) error {
	info, _ := connect.CallInfoForServerContext(ctx)
	if info.RequestHeader().Get("Authorization") != "Bearer synthetic-example-token" {
		return connect.NewError(connect.CodeUnauthenticated, "missing example token")
	}
	f.mu.Lock()
	f.requests = append(f.requests, req.GetAfterSeq())
	first := len(f.requests) == 1
	f.mu.Unlock()
	if f.reject {
		return connect.NewError(connect.CodePermissionDenied, "read grant revoked")
	}
	if first {
		if err := stream.Send(&deploymentv1.WatchDeploymentResponse{
			Deployment: &deploymentv1.Deployment{Status: deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED},
			Activity:   []*deploymentv1.Activity{{Seq: 1, Message: "waiting for recovery"}},
		}); err != nil {
			return err
		}
		panic(http.ErrAbortHandler)
	}
	if req.GetAfterSeq() != 1 {
		return connect.NewError(connect.CodeInvalidArgument, "resume lost the acknowledged activity")
	}
	return stream.Send(&deploymentv1.WatchDeploymentResponse{
		Deployment: &deploymentv1.Deployment{
			Status:    deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_RUNNING,
			StartedAt: timestamppb.Now(),
		},
		Activity: []*deploymentv1.Activity{{Seq: 2, Message: "ready after recovery"}},
	})
}

func exampleClient(t *testing.T, fixture *watchFixture) deploymentv1connect.DeploymentServiceClient {
	t.Helper()
	server := connect.NewServer()
	deploymentv1connect.RegisterDeploymentServiceHandler(server, fixture)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	host := httptest.NewServer(mux)
	t.Cleanup(host.Close)
	return rpc.NewHTTPClients(host.Client(), host.URL, "synthetic-example-token").Deployment
}

func TestFollowResumesActivityAfterConnectionLoss(t *testing.T) {
	fixture := &watchFixture{}
	var output bytes.Buffer
	if err := follow(t.Context(), exampleClient(t, fixture), "deployment", &output); err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if !slices.Equal(fixture.requests, []int64{0, 1}) {
		t.Fatalf("watch positions = %v", fixture.requests)
	}
	for _, message := range []string{"waiting for recovery", "ready after recovery"} {
		if strings.Count(output.String(), message) != 1 {
			t.Fatalf("activity must print once: %s", output.String())
		}
	}
	if !strings.Contains(output.String(), "RUNNING") {
		t.Fatalf("recovered deployment was not followed to readiness: %s", output.String())
	}
	t.Log(output.String())
}

func TestFollowStopsWhenAccessIsRevoked(t *testing.T) {
	fixture := &watchFixture{reject: true}
	var output bytes.Buffer
	err := follow(t.Context(), exampleClient(t, fixture), "deployment", &output)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("error = %v", err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.requests) != 1 {
		t.Fatalf("permanent refusal retried %d times", len(fixture.requests))
	}
}
