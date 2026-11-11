package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/team-loco/loco/api/authz/actions"
	db "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/interceptor"
	"github.com/team-loco/loco/api/pkg/admission"
	"github.com/team-loco/loco/api/reqctx"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	"github.com/team-loco/loco/gen/go/loco/build/v1/buildv1connect"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	"github.com/team-loco/loco/gen/go/loco/plan/v1/planv1connect"
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
		limiter := admission.New(f.pool, config)
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
	limiter := admission.New(f.pool, admission.Config{Window: time.Hour, GlobalDeploys: 1})
	scope := actions.New(actions.CreateDeployment, f.resourceID)
	ctx := limiter.Context(f.ctx, deploymentv1connect.DeploymentServiceCreateDeploymentProcedure)
	if err := admission.Authorize(ctx, scope); err != nil {
		t.Fatal(err)
	}
	_, err := server.CreateDeployment(
		limiter.Context(f.ctx, deploymentv1connect.DeploymentServiceCreateDeploymentProcedure),
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
	limiter := admission.New(f.pool, admission.Config{Window: time.Hour, GlobalBuilds: 1})
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{})
	_, err := f.server.CreateBuild(
		limiter.Context(ctx, buildv1connect.BuildServiceCreateBuildProcedure),
		&buildv1.CreateBuildRequest{ResourceId: f.resourceID.String()},
	)
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("authorization changed: %v", err)
	}
	_, err = f.server.CreateBuild(
		limiter.Context(f.ctx, buildv1connect.BuildServiceCreateBuildProcedure),
		&buildv1.CreateBuildRequest{ResourceId: f.resourceID.String(), SourceSize: 10},
	)
	if err != nil {
		t.Fatal(err)
	}
}

func TestApplyAdmissionBeforePlanSideEffects(t *testing.T) {
	f := newDeployFixture(t)
	server := newPlanServer(f)
	limiter := admission.New(f.pool, admission.Config{Window: time.Hour, WorkspaceDeploys: 1})
	env, err := f.queries.GetEnvironmentByID(t.Context(), f.envID)
	if err != nil {
		t.Fatal(err)
	}
	scope := actions.New(actions.Apply, env.WorkspaceID)
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: []db.EntityScope{scope}})
	if admitErr := admission.Authorize(
		limiter.Context(ctx, planv1connect.PlanServiceApplyProcedure),
		scope,
	); admitErr != nil {
		t.Fatal(admitErr)
	}
	_, err = server.Apply(
		limiter.Context(ctx, planv1connect.PlanServiceApplyProcedure),
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
