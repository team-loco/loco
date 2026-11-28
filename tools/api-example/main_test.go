package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	"github.com/team-loco/loco/gen/go/loco/environment/v1/environmentv1connect"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/internal/rpc"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
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

const sourceRegion = "us-ca-1"

type redeployFixture struct {
	deploymentv1connect.UnimplementedDeploymentServiceHandler
	resourcev1connect.UnimplementedResourceServiceHandler
	environmentv1connect.UnimplementedEnvironmentServiceHandler
	source  *deploymentv1.DeploymentSpec
	created *deploymentv1.CreateDeploymentRequest
}

func (*redeployFixture) GetResource(
	context.Context,
	*resourcev1.GetResourceRequest,
) (*resourcev1.GetResourceResponse, error) {
	return &resourcev1.GetResourceResponse{
		Resource: &resourcev1.Resource{
			Id: "resource",
			Spec: &resourcev1.ResourceSpec{
				Spec: &resourcev1.ResourceSpec_Service{
					Service: &resourcev1.ServiceSpec{Regions: map[string]*resourcev1.RegionTarget{
						sourceRegion: {Enabled: true}, "us-va-1": {Enabled: true},
					}},
				},
			},
		},
	}, nil
}

func (*redeployFixture) ListEnvironments(
	context.Context,
	*environmentv1.ListEnvironmentsRequest,
) (*environmentv1.ListEnvironmentsResponse, error) {
	return &environmentv1.ListEnvironmentsResponse{
		Environments: []*environmentv1.Environment{{Id: "environment", Slug: "production"}},
	}, nil
}

func (*redeployFixture) ListDeployments(
	context.Context,
	*deploymentv1.ListDeploymentsRequest,
) (*deploymentv1.ListDeploymentsResponse, error) {
	return &deploymentv1.ListDeploymentsResponse{Deployments: []*deploymentv1.Deployment{{Id: "source"}}}, nil
}

func (f *redeployFixture) GetDeployment(
	context.Context,
	*deploymentv1.GetDeploymentRequest,
) (*deploymentv1.GetDeploymentResponse, error) {
	return &deploymentv1.GetDeploymentResponse{Deployment: &deploymentv1.Deployment{Id: "source", Spec: f.source}}, nil
}

func (f *redeployFixture) CreateDeployment(
	_ context.Context,
	request *deploymentv1.CreateDeploymentRequest,
) (*deploymentv1.CreateDeploymentResponse, error) {
	f.created = request
	return nil, connect.NewError(connect.CodePermissionDenied, "synthetic create refusal")
}

func TestRunPreservesSourceDeploymentRegions(t *testing.T) {
	fixture := &redeployFixture{
		source: &deploymentv1.DeploymentSpec{
			Spec: &deploymentv1.DeploymentSpec_Service{Service: &deploymentv1.ServiceDeploymentSpec{
				Regions: map[string]*deploymentv1.RegionDeploymentTarget{
					sourceRegion: {
						Cpu:         new("100m"),
						Memory:      new("256Mi"),
						MinReplicas: new(int32(1)),
						MaxReplicas: new(int32(1)),
					},
				},
				Command: []string{"serve"},
				Env:     map[string]string{"MODE": "production"},
			}},
		},
	}
	server := connect.NewServer()
	deploymentv1connect.RegisterDeploymentServiceHandler(server, fixture)
	resourcev1connect.RegisterResourceServiceHandler(server, fixture)
	environmentv1connect.RegisterEnvironmentServiceHandler(server, fixture)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	host := httptest.NewServer(mux)
	defer host.Close()
	token := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(token, []byte("synthetic-example-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := run(host.URL, token, "workspace", "service", "production", time.Minute)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("run error = %v", err)
	}
	if fixture.created == nil {
		t.Fatal("deployment creation was not attempted")
	}
	t.Logf(
		"Synthetic typed Connect handlers: resource enables us-ca-1 and us-va-1; "+
			"source targets us-ca-1 only. Captured CreateDeployment:\n%s",
		protojson.Format(fixture.created),
	)
	if !slices.Equal(fixture.created.GetRegions(), []string{sourceRegion}) {
		t.Fatalf(
			"redeploy regions = %v; source targets only us-ca-1 despite two enabled resource regions",
			fixture.created.GetRegions(),
		)
	}
	if !proto.Equal(fixture.created.GetSpec(), fixture.source) {
		t.Fatalf("redeploy spec = %v, want source %v", fixture.created.GetSpec(), fixture.source)
	}
}
