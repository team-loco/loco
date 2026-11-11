package service

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/validate"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/team-loco/loco/api/auth"
	"github.com/team-loco/loco/api/auth/authtest"
	"github.com/team-loco/loco/api/authz"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/interceptor"
	"github.com/team-loco/loco/api/tvm"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	"github.com/team-loco/loco/gen/go/loco/plan/v1/planv1connect"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	"github.com/team-loco/loco/gen/go/loco/resource/v1/resourcev1connect"
	"github.com/team-loco/loco/internal/rpc"
)

func TestDeploymentAttributionFromCredentials(t *testing.T) {
	for _, method := range []genDb.DeploymentMethod{
		genDb.DeploymentMethodCli, genDb.DeploymentMethodWeb, genDb.DeploymentMethodApiToken,
	} {
		t.Run(string(method), func(t *testing.T) {
			f := newDeployFixture(t)
			seedDeployment(t, f, sameRegionSpec)
			f.prepareApply(t)
			clients, actorID := deploymentActorClients(t, f, method)
			commit := &deploymentv1.SourceCommit{Sha: strings.Repeat("a", 40), Dirty: true}
			created, err := clients.Deployment.CreateDeployment(t.Context(), &deploymentv1.CreateDeploymentRequest{
				ResourceId:    f.resourceID.String(),
				EnvironmentId: f.envID.String(),
				Region:        testRegion,
				Spec: &deploymentv1.DeploymentSpec{
					Spec: &deploymentv1.DeploymentSpec_Service{Service: imageService()},
				},
				Commit: commit,
			})
			if err != nil {
				t.Fatal(err)
			}
			assertDeploymentActor(t, clients, f.resourceID.String(), created.GetDeploymentId(), method, actorID, commit)

			f.finishRollouts(t)
			_, err = clients.Resource.ScaleResource(t.Context(), &resourcev1.ScaleResourceRequest{
				ResourceId: f.resourceID.String(), EnvironmentId: f.envID.String(), Replicas: new(int32(2)),
			})
			if err != nil {
				t.Fatal(err)
			}
			active, err := f.queries.GetActiveDeploymentForResourceAndRegion(
				t.Context(),
				genDb.GetActiveDeploymentForResourceAndRegionParams{
					ResourceID: f.resourceID, EnvironmentID: f.envID, Region: testRegion,
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			assertDeploymentActor(t, clients, f.resourceID.String(), active.ID.String(), method, actorID, commit)

			file := []byte(planFileHeader + applyFileWorker)
			planned, err := clients.Plan.Plan(
				t.Context(),
				&planv1.PlanRequest{File: file, EnvironmentId: f.envID.String()},
			)
			if err != nil {
				t.Fatal(err)
			}
			applied, err := clients.Plan.Apply(t.Context(), &planv1.ApplyRequest{
				File:          file,
				EnvironmentId: f.envID.String(),
				Revision:      planned.GetRevision(),
				Images:        planned.GetImages(),
				Commit:        commit,
			})
			if err != nil {
				t.Fatal(err)
			}
			if len(applied.GetDeployments()) != 1 {
				t.Fatalf("deployments = %v", applied.GetDeployments())
			}
			worker, ok := f.resourceByName(t, applyWorker)
			if !ok {
				t.Fatal("worker not created")
			}
			assertDeploymentActor(
				t,
				clients,
				worker.ID.String(),
				applied.GetDeployments()[0].GetDeploymentId(),
				method,
				actorID,
				commit,
			)

			name := "renamed-user"
			avatar := "https://example.test/avatar.png"
			query := `UPDATE users SET name = $2, avatar_url = $3 WHERE id = $1`
			args := []any{actorID, name, avatar}
			if method == genDb.DeploymentMethodApiToken {
				name, avatar = "renamed-token", ""
				query, args = `UPDATE api_tokens SET name = $2 WHERE id = $1`, []any{actorID, name}
			}
			if _, updateErr := f.pool.Exec(t.Context(), query, args...); updateErr != nil {
				t.Fatal(updateErr)
			}
			listed, err := clients.Deployment.ListDeployments(
				t.Context(),
				&deploymentv1.ListDeploymentsRequest{ResourceId: worker.ID.String()},
			)
			if err != nil {
				t.Fatal(err)
			}
			if got := listed.GetDeployments()[0]; got.GetActorName() != name || got.GetActorAvatarUrl() != avatar {
				t.Fatalf("actor after rename = %q, %q", got.GetActorName(), got.GetActorAvatarUrl())
			}
		})
	}
}

func deploymentActorClients(t *testing.T, f *deployFixture, method genDb.DeploymentMethod) (*rpc.Clients, uuid.UUID) {
	t.Helper()
	issuer := authtest.NewIssuer(t)
	policy, err := auth.ParseSignupPolicy("open", "")
	if err != nil {
		t.Fatal(err)
	}
	resolver := auth.NewResolver(f.pool, policy)
	user, _, err := resolver.Resolve(t.Context(), auth.Identity{
		Issuer: issuer.URL(), Subject: "deployer", Email: "deployer@example.test", EmailVerified: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range f.adminScopes(t) {
		if scopeErr := f.queries.AddUserScope(t.Context(), genDb.AddUserScopeParams{
			UserID: user.ID, EntityType: scope.EntityType, EntityID: scope.EntityID, Scope: scope.Scope,
		}); scopeErr != nil {
			t.Fatal(scopeErr)
		}
	}
	machine := tvm.NewVendingMachine(f.pool, f.queries, tvm.Config{
		MaxAPITokenDuration: time.Hour, SessionAccessTokenDuration: time.Hour,
		SessionRefreshTokenDuration: time.Hour, LastUsedUpdateInterval: time.Minute,
	})
	verifier := auth.NewVerifier(http.DefaultClient, []auth.IssuerConfig{{
		Issuer: issuer.URL(), JWKSURL: issuer.URL() + "/.well-known/jwks.json", Audience: "authenticated",
		Claims: auth.ClaimPaths{Subject: "sub", Email: "email", EmailVerified: "email_verified"},
	}})
	server := connect.NewServer(interceptor.NewErrorInterceptor(),
		interceptor.NewAuthInterceptor(machine, verifier, resolver, authz.New(f.pool, f.queries)).Interceptor(),
		validate.NewServerInterceptor())
	deploymentv1connect.RegisterDeploymentServiceHandler(server, NewDeploymentServer(
		f.pool,
		f.queries,
		&fakeResolver{
			digest: testDigest,
		},
		testRegistryHost,
		testServiceDefaults(),
		testClusterSilentAfter,
		testMaxPlacementBytes,
	))
	planv1connect.RegisterPlanServiceHandler(server, newPlanServer(f))
	resourcev1connect.RegisterResourceServiceHandler(
		server,
		NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter),
	)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	endpoint := httptest.NewServer(mux)
	t.Cleanup(endpoint.Close)

	actorID := user.ID
	token := issuer.Sign("k1", issuer.Claims("deployer", "deployer@example.test", true))
	switch method {
	case genDb.DeploymentMethodCli:
		token, _, err = machine.IssueSession(t.Context(), user.ID, nil, netip.Addr{}, "")
	case genDb.DeploymentMethodApiToken:
		var metadata genDb.CreateAPITokenRow
		token, metadata, err = machine.Issue(t.Context(), "deploy-token", user.ID,
			genDb.Entity{Type: genDb.EntityTypeWorkspace, ID: f.workspaceID(t)}, f.adminScopes(t), time.Hour)
		actorID = metadata.ID
	}
	if err != nil {
		t.Fatal(err)
	}
	return rpc.ForHost(endpoint.URL, token), actorID
}

func assertDeploymentActor(
	t *testing.T,
	clients *rpc.Clients,
	resource, id string,
	method genDb.DeploymentMethod,
	actorID uuid.UUID,
	commit *deploymentv1.SourceCommit,
) {
	t.Helper()
	listed, err := clients.Deployment.ListDeployments(
		t.Context(),
		&deploymentv1.ListDeploymentsRequest{ResourceId: resource},
	)
	if err != nil {
		t.Fatal(err)
	}
	got := listed.GetDeployments()[0]
	actorType := string(genDb.EntityTypeUser)
	if method == genDb.DeploymentMethodApiToken {
		actorType = "api_token"
	}
	if got.GetId() != id || got.GetActorId() != actorID.String() || got.GetActorType() != actorType ||
		got.GetMethod() != string(method) ||
		!proto.Equal(got.GetCommit(), commit) {
		t.Fatalf("deployment = %v, want actor %s/%s method %s commit %v", got, actorType, actorID, method, commit)
	}
	detail, err := clients.Deployment.GetDeployment(t.Context(), &deploymentv1.GetDeploymentRequest{DeploymentId: id})
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(detail.GetDeployment(), got) {
		t.Fatalf("get and list differ: %v / %v", detail, got)
	}
}
