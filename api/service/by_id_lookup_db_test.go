package service

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	"github.com/team-loco/loco/gen/go/loco/deployment/v1/deploymentv1connect"
	domainv1 "github.com/team-loco/loco/gen/go/loco/domain/v1"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

type byIDLookup struct {
	name     string
	existing string
	call     func(ctx context.Context, id string) error
}

func TestByIDLookupsAnswerACallerWithoutAccessTheSameForExistingAndMissingIDs(t *testing.T) {
	f := newDeployFixture(t)
	created, err := createDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatal(err)
	}
	f.addDomain(t, testPrimaryDomain, true)
	domains := newDomainClient(t, f)
	domainID := domains.domainID(t, testPrimaryDomain)
	deploymentID := created.GetDeploymentId()
	deployments, _ := f.deploymentWriter(t)
	environments := NewEnvironmentServer(f.pool, f.queries)
	resources := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter)
	plans := newPlanServer(f)
	var stream deploymentv1connect.DeploymentServiceWatchDeploymentServerStream
	file := []byte(planFileHeader + planFileSvc)
	spec := &deploymentv1.DeploymentSpec{Spec: &deploymentv1.DeploymentSpec_Service{
		Service: &deploymentv1.ServiceDeploymentSpec{Port: 8080},
	}}

	lookups := []byIDLookup{
		{name: "CreateDeployment", existing: f.resourceID.String(), call: func(ctx context.Context, id string) error {
			_, callErr := deployments.CreateDeployment(ctx, &deploymentv1.CreateDeploymentRequest{
				ResourceId: id, EnvironmentId: f.envID.String(), Region: testRegion, Spec: spec,
			})
			return callErr
		}},
		{name: "GetDeployment", existing: deploymentID, call: func(ctx context.Context, id string) error {
			_, callErr := deployments.GetDeployment(ctx, &deploymentv1.GetDeploymentRequest{DeploymentId: id})
			return callErr
		}},
		{name: "DeleteDeployment", existing: deploymentID, call: func(ctx context.Context, id string) error {
			_, callErr := deployments.DeleteDeployment(ctx, &deploymentv1.DeleteDeploymentRequest{DeploymentId: id})
			return callErr
		}},
		{name: "WatchDeployment", existing: deploymentID, call: func(ctx context.Context, id string) error {
			req := &deploymentv1.WatchDeploymentRequest{DeploymentId: id}
			return deployments.WatchDeployment(ctx, req, stream)
		}},
		{name: "UpdateResourceDomain", existing: domainID, call: func(ctx context.Context, id string) error {
			req := &domainv1.UpdateResourceDomainRequest{DomainId: id, Domain: new("renamed.example.com")}
			_, callErr := domains.server.UpdateResourceDomain(ctx, req)
			return callErr
		}},
		{name: "DeleteResourceDomain", existing: domainID, call: func(ctx context.Context, id string) error {
			_, callErr := domains.server.DeleteResourceDomain(ctx, &domainv1.DeleteResourceDomainRequest{DomainId: id})
			return callErr
		}},
		{name: "ListPlatformDomains", existing: f.envID.String(), call: func(ctx context.Context, id string) error {
			req := &domainv1.ListPlatformDomainsRequest{EnvironmentId: id}
			_, callErr := domains.server.ListPlatformDomains(ctx, req)
			return callErr
		}},
		{name: "GetEnvironment", existing: f.envID.String(), call: func(ctx context.Context, id string) error {
			_, callErr := environments.GetEnvironment(ctx, &environmentv1.GetEnvironmentRequest{EnvironmentId: id})
			return callErr
		}},
		{name: "UpdateEnvironment", existing: f.envID.String(), call: func(ctx context.Context, id string) error {
			req := &environmentv1.UpdateEnvironmentRequest{EnvironmentId: id, Name: new("renamed")}
			_, callErr := environments.UpdateEnvironment(ctx, req)
			return callErr
		}},
		{name: "DeleteEnvironment", existing: f.envID.String(), call: func(ctx context.Context, id string) error {
			req := &environmentv1.DeleteEnvironmentRequest{EnvironmentId: id}
			_, callErr := environments.DeleteEnvironment(ctx, req)
			return callErr
		}},
		{name: "ListRegions", existing: f.envID.String(), call: func(ctx context.Context, id string) error {
			_, callErr := resources.ListRegions(ctx, &resourcev1.ListRegionsRequest{EnvironmentId: id})
			return callErr
		}},
		{name: "Plan", existing: f.envID.String(), call: func(ctx context.Context, id string) error {
			_, callErr := plans.Plan(ctx, &planv1.PlanRequest{File: file, EnvironmentId: id})
			return callErr
		}},
		{name: "Apply", existing: f.envID.String(), call: func(ctx context.Context, id string) error {
			_, callErr := plans.Apply(ctx, &planv1.ApplyRequest{File: file, EnvironmentId: id})
			return callErr
		}},
	}

	outsider := reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: []genDb.EntityScope{}})
	for _, lookup := range lookups {
		t.Run(lookup.name, func(t *testing.T) {
			existingErr := lookup.call(outsider, lookup.existing)
			missingErr := lookup.call(outsider, uuid.NewString())
			wantSameNotFound(t, existingErr, missingErr)
		})
	}
}

func wantSameNotFound(t *testing.T, existingErr, missingErr error) {
	t.Helper()
	wantCode(t, existingErr, connect.CodeNotFound)
	wantCode(t, missingErr, connect.CodeNotFound)
	existing, missing := publicError(existingErr), publicError(missingErr)
	if existing.Message() != missing.Message() {
		t.Fatalf("existing ID answered %q, missing ID answered %q", existing.Message(), missing.Message())
	}
}
