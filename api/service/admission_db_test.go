package service

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"
	"github.com/team-loco/loco/api/authz"
	"github.com/team-loco/loco/api/authz/actions"
	db "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/interceptor"
	"github.com/team-loco/loco/api/pkg/admission"
	"github.com/team-loco/loco/api/pkg/cache"
	"github.com/team-loco/loco/api/reqctx"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	"github.com/team-loco/loco/gen/go/loco/build/v1/buildv1connect"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
)

func TestBuildAdmissionAcrossServers(t *testing.T) {
	f := newBuildFixture(t)
	config := admission.Config{Window: time.Hour, GlobalBuilds: 3, WorkspaceBuilds: 2}
	clients := make([]buildv1connect.BuildServiceClient, 2)
	principal, _ := reqctx.PrincipalFrom(f.ctx)
	authenticate := func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			return next(reqctx.WithPrincipal(ctx, principal), spec, stream)
		}
	}
	for i := range clients {
		limiter := admission.New(f.pool, nil, config)
		rpc := connect.NewServer(interceptor.NewErrorInterceptor(), authenticate, limiter.Interceptor())
		buildv1connect.RegisterBuildServiceHandler(rpc, f.server)
		mux := http.NewServeMux()
		connecthttp.Mount(mux, rpc)
		server := httptest.NewServer(mux)
		t.Cleanup(server.Close)
		clients[i] = buildv1connect.NewBuildServiceClient(
			connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL)),
		)
	}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			ctx, info := connect.NewClientContext(t.Context())
			_, err := clients[i%2].CreateBuild(ctx, &buildv1.CreateBuildRequest{
				ResourceId: f.resourceID.String(), DockerfilePath: testDockerfile, SourceSize: 10,
			})
			if err == nil {
				accepted.Add(1)
				return
			}
			if connect.CodeOf(err) != connect.CodeResourceExhausted || !strings.Contains(err.Error(), "retry after") {
				t.Errorf("rejection: %v", err)
			}
			if info.ResponseHeader().Get("Retry-After") == "" {
				t.Error("missing retry header")
			}
		})
	}
	wg.Wait()
	if accepted.Load() != 2 {
		t.Fatalf("accepted %d builds", accepted.Load())
	}
	var count int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM builds").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 || len(f.bucket.presigned) != 2 {
		t.Fatalf("builds=%d uploads=%d", count, len(f.bucket.presigned))
	}
}

func TestDeploymentAdmissionBeforeImageResolution(t *testing.T) {
	f := newBuildFixture(t)
	seedDeployment(t, f.deployFixture, sameRegionSpec)
	resolver := &fakeResolver{digest: testDigest}
	server := NewDeploymentServer(
		f.pool,
		f.queries,
		resolver,
		testRegistryHost,
		testServiceDefaults(),
		testClusterSilentAfter,
		testMaxPlacementBytes,
	)
	limiter := admission.New(f.pool, nil, admission.Config{Window: time.Hour, GlobalDeploys: 1})
	ctx := limiter.Context(f.ctx)
	if err := admission.AdmitWork(ctx, f.workspaceID(t), admission.Deploy); err != nil {
		t.Fatal(err)
	}
	_, err := server.CreateDeployment(
		limiter.Context(f.ctx),
		&deploymentv1.CreateDeploymentRequest{
			ResourceId:    f.resourceID.String(),
			EnvironmentId: f.envID.String(),
			Region:        testRegion,
			Spec: &deploymentv1.DeploymentSpec{
				Spec: &deploymentv1.DeploymentSpec_Service{
					Service: &deploymentv1.ServiceDeploymentSpec{
						Build: &deploymentv1.BuildSource{Type: buildSourceTypeImage, Image: testPublicImage},
					},
				},
			},
		},
	)
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("want admission refusal before spec validation: %v", err)
	}
	var count int
	if err := f.pool.QueryRow(t.Context(), "SELECT count(*) FROM deployments").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("created %d deployments", count)
	}
	if len(resolver.calls) != 0 {
		t.Fatalf("resolved images: %v", resolver.calls)
	}
}

func TestUnauthorizedCallerCannotConsumeWorkAdmission(t *testing.T) {
	f := newBuildFixture(t)
	limiter := admission.New(f.pool, nil, admission.Config{Window: time.Hour, GlobalBuilds: 1})
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{})
	_, err := f.server.CreateBuild(
		limiter.Context(ctx),
		&buildv1.CreateBuildRequest{ResourceId: f.resourceID.String()},
	)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("authorization changed: %v", err)
	}
	_, err = f.server.CreateBuild(
		limiter.Context(f.ctx),
		&buildv1.CreateBuildRequest{ResourceId: f.resourceID.String(), SourceSize: 10},
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestApplyAdmissionBeforePlanSideEffects(t *testing.T) {
	f := newDeployFixture(t)
	server := newPlanServer(f)
	limiter := admission.New(f.pool, nil, admission.Config{Window: time.Hour, WorkspaceDeploys: 1})
	env, err := f.queries.GetEnvironmentByID(t.Context(), f.envID)
	if err != nil {
		t.Fatal(err)
	}
	scope := actions.New(actions.Apply, env.WorkspaceID)
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: []db.EntityScope{scope}})
	if admitErr := admission.AdmitWork(limiter.Context(ctx), env.WorkspaceID, admission.Deploy); admitErr != nil {
		t.Fatal(admitErr)
	}
	_, err = server.Apply(
		limiter.Context(ctx),
		&planv1.ApplyRequest{File: []byte(planFileHeader + applyFileWorker), EnvironmentId: f.envID.String()},
	)
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("apply: %v", err)
	}
	var resources, deployments int
	if err := f.pool.QueryRow(ctx, "SELECT (SELECT count(*) FROM resources), (SELECT count(*) FROM deployments)").
		Scan(&resources, &deployments); err != nil {
		t.Fatal(err)
	}
	if resources != 1 || deployments != 0 {
		t.Fatalf("resources=%d deployments=%d", resources, deployments)
	}
}

func TestAdmissionPreservesMissingResourceResponse(t *testing.T) {
	f := newBuildFixture(t)
	limiter := admission.New(f.pool, nil, admission.Config{Window: time.Hour, GlobalBuilds: 1})
	resourceID := uuid.New()
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: []db.EntityScope{
		actions.New(actions.CreateBuild, resourceID),
	}})
	_, err := f.server.CreateBuild(
		limiter.Context(ctx),
		&buildv1.CreateBuildRequest{ResourceId: resourceID.String(), SourceSize: 10},
	)
	if !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("missing resource: %v", err)
	}
	_, err = f.server.CreateBuild(
		limiter.Context(f.ctx),
		&buildv1.CreateBuildRequest{ResourceId: f.resourceID.String(), SourceSize: 10},
	)
	if err != nil {
		t.Fatalf("missing resource consumed the work budget: %v", err)
	}
}

type repeatedAuthorizationServer struct {
	resourcev1connect.UnimplementedResourceServiceHandler
	fixture *deployFixture
}

func (s *repeatedAuthorizationServer) ListWorkspaceResources(
	ctx context.Context,
	req *resourcev1.ListWorkspaceResourcesRequest,
) (*resourcev1.ListWorkspaceResourcesResponse, error) {
	authorizer := authz.New(s.fixture.pool, s.fixture.queries)
	for range 2 {
		if err := authorize(ctx, authorizer, actions.ListResources, uuid.MustParse(req.GetWorkspaceId())); err != nil {
			return nil, err
		}
		if err := authorizeVisible(
			ctx,
			authorizer,
			actions.New(actions.GetResource, s.fixture.resourceID),
			ErrResourceNotFound,
		); err != nil {
			return nil, err
		}
	}
	return &resourcev1.ListWorkspaceResourcesResponse{}, nil
}

func TestRequestAdmissionChargesOnceAcrossAuthorizationDecisions(t *testing.T) {
	f := newDeployFixture(t)
	backend, err := cache.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	limiter := admission.New(
		f.pool,
		backend,
		admission.Config{Window: time.Hour, GlobalRequests: 2, WorkspaceRequests: 1},
	)
	p := reqctx.Principal{Scopes: f.workspaceReadScopes(t)}
	authenticate := func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			return next(reqctx.WithPrincipal(ctx, p), spec, stream)
		}
	}
	rpc := connect.NewServer(authenticate, limiter.Interceptor())
	resourcev1connect.RegisterResourceServiceHandler(rpc, &repeatedAuthorizationServer{fixture: f})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpc)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := resourcev1connect.NewResourceServiceClient(
		connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL)),
	)
	request := &resourcev1.ListWorkspaceResourcesRequest{WorkspaceId: f.workspaceID(t).String()}
	if _, err := client.ListWorkspaceResources(t.Context(), request); err != nil {
		t.Fatalf("repeated authorization spent request capacity: %v", err)
	}
	if _, err := client.ListWorkspaceResources(
		t.Context(),
		request,
	); connect.CodeOf(
		err,
	) != connect.CodeResourceExhausted {
		t.Fatalf("second RPC: %v", err)
	}
	// Authorization decisions outside an RPC must not consume admission capacity either.
	ctx := limiter.Context(reqctx.WithPrincipal(t.Context(), p))
	for range 3 {
		if err := authorize(ctx, authz.New(f.pool, f.queries), actions.ListResources, f.workspaceID(t)); err != nil {
			t.Fatalf("pure authorization: %v", err)
		}
	}
}

func TestRestartResourceConsumesDeployAdmission(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := createDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	f.finishRollouts(t)
	limiter := admission.New(f.pool, nil, admission.Config{Window: time.Hour, GlobalDeploys: 1})
	ctx := limiter.Context(reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: []db.EntityScope{
		actions.New(actions.RestartResource, f.resourceID),
	}}))
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter)
	req := &resourcev1.RestartResourceRequest{ResourceId: f.resourceID.String(), EnvironmentId: f.envID.String()}
	if _, err := server.RestartResource(ctx, req); err != nil {
		t.Fatal(err)
	}
	f.finishRollouts(t)
	key := db.GetPlacementForResourceClusterParams{ResourceID: f.resourceID, ClusterID: f.clusterID}
	before, err := f.queries.GetPlacementForResourceCluster(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if _, restartErr := server.RestartResource(ctx, req); connect.CodeOf(restartErr) != connect.CodeResourceExhausted {
		t.Fatalf("second restart: %v", restartErr)
	}
	after, err := f.queries.GetPlacementForResourceCluster(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if after.DesiredRevision != before.DesiredRevision {
		t.Fatal("rejected restart changed placement revision")
	}
	for _, deployment := range f.activeDeployments(t, f.envID) {
		if deployment.Status != db.DeploymentStatusRunning {
			t.Fatalf("rejected restart changed deployment status: %s", deployment.Status)
		}
	}
	if events := f.count(
		t,
		"SELECT count(*) FROM events WHERE subject_id = $1 AND type = 'resource.restarted'",
	); events != 1 {
		t.Fatalf("restart events: %d", events)
	}
}

func TestResolveImageUsesOnlyRequestAdmission(t *testing.T) {
	f := newDeployFixture(t)
	backend, err := cache.NewMemory()
	if err != nil {
		t.Fatal(err)
	}
	defer backend.Close()
	limiter := admission.New(
		f.pool,
		backend,
		admission.Config{Window: time.Hour, WorkspaceRequests: 1, GlobalDeploys: 1},
	)
	if err := admission.AdmitWork(limiter.Context(t.Context()), f.workspaceID(t), admission.Deploy); err != nil {
		t.Fatal(err)
	}
	p := reqctx.Principal{Scopes: []db.EntityScope{actions.New(actions.CreateResource, f.workspaceID(t))}}
	authenticate := func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			return next(reqctx.WithPrincipal(ctx, p), spec, stream)
		}
	}
	resolver := &fakeResolver{digest: testDigest}
	service := NewDeploymentServer(
		f.pool,
		f.queries,
		resolver,
		testRegistryHost,
		testServiceDefaults(),
		testClusterSilentAfter,
		testMaxPlacementBytes,
	)
	rpc := connect.NewServer(authenticate, limiter.Interceptor())
	deploymentv1connect.RegisterDeploymentServiceHandler(rpc, service)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, rpc)
	server := httptest.NewServer(mux)
	defer server.Close()
	client := deploymentv1connect.NewDeploymentServiceClient(
		connect.NewClient(connecthttp.NewTransport(server.Client(), server.URL)),
	)
	req := &deploymentv1.ResolveImageRequest{WorkspaceId: f.workspaceID(t).String(), Image: testPublicImage}
	if _, err := client.ResolveImage(t.Context(), req); err != nil {
		t.Fatalf("preview consumed deploy admission: %v", err)
	}
	if _, err := client.ResolveImage(t.Context(), req); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("preview escaped request admission: %v", err)
	}
	if len(resolver.calls) != 1 {
		t.Fatalf("resolved images: %v", resolver.calls)
	}
}
