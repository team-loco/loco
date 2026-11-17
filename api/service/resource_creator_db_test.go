package service

import (
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

func TestCreateResourceReturnsTheCredentialActor(t *testing.T) {
	t.Parallel()
	f := newSecretFixture(t)
	creators := map[string]string{f.resourceID.String(): f.ownerID.String()}
	server := NewResourceServer(f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults())
	for _, credential := range []genDb.DeploymentActorType{
		genDb.DeploymentActorTypeUser, genDb.DeploymentActorTypeApiToken,
	} {
		ctx := f.owner
		if credential == genDb.DeploymentActorTypeApiToken {
			ctx = creationTokenContext(t, f, genDb.EntityTypeWorkspace, f.workspaceID, genDb.ScopeWrite)
		}
		principal, _ := reqctx.PrincipalFrom(ctx)
		created, err := server.CreateResource(ctx, &resourcev1.CreateResourceRequest{
			WorkspaceId: f.workspaceID.String(), Name: string(credential),
			Type: resourcev1.ResourceType_RESOURCE_TYPE_SERVICE, Spec: testResourceSpec(),
		})
		if err != nil {
			t.Fatal(err)
		}
		resource, err := server.GetResource(f.owner, &resourcev1.GetResourceRequest{
			Key: &resourcev1.GetResourceRequest_ResourceId{ResourceId: created.GetResourceId()},
		})
		if err != nil {
			t.Fatal(err)
		}
		if got := resource.GetResource().GetCreatedBy(); got != principal.ActorID.String() {
			t.Fatalf("%s resource creator = %q, want %s", credential, got, principal.ActorID)
		}
		creators[created.GetResourceId()] = principal.ActorID.String()
	}
	var ids []string
	cursor := ""
	for {
		page, err := server.ListWorkspaceResources(f.owner, &resourcev1.ListWorkspaceResourcesRequest{
			WorkspaceId: f.workspaceID.String(), PageSize: 1, PageToken: cursor,
		})
		if err != nil {
			t.Fatal(err)
		}
		for _, resource := range page.GetResources() {
			if got := resource.GetCreatedBy(); got != creators[resource.GetId()] {
				t.Errorf("listed resource creator = %q, want %s", got, creators[resource.GetId()])
			}
			ids = append(ids, resource.GetId())
		}
		cursor = page.GetNextPageToken()
		if cursor == "" {
			break
		}
	}
	if len(ids) != 3 {
		t.Fatalf("listed %d resources, want 3", len(ids))
	}
}

func TestApplyResourceRecordsTheCredentialActor(t *testing.T) {
	t.Parallel()
	for _, actor := range []genDb.DeploymentActorType{genDb.DeploymentActorTypeUser, genDb.DeploymentActorTypeApiToken} {
		t.Run(string(actor), func(t *testing.T) {
			f := newDeployFixture(t)
			f.prepareApply(t)
			actorID := f.userID
			if actor == genDb.DeploymentActorTypeApiToken {
				actorID = f.apiToken(t)
			}
			ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{
				UserID: f.userID, ActorType: actor, ActorID: actorID,
				Method: genDb.DeploymentMethodCli, Scopes: f.applyScopes(t),
			})
			_, err := newPlanServer(f).Apply(ctx, &planv1.ApplyRequest{
				File: []byte(planFileHeader + planFileNew), EnvironmentId: f.envID.String(),
				Provision: []string{planNew},
			})
			if err != nil {
				t.Fatal(err)
			}
			resource, err := getResourceByName(t, f, f.workspaceReadScopes(t), f.workspaceID(t), planNew)
			if err != nil {
				t.Fatal(err)
			}
			if got := resource.GetCreatedBy(); got != actorID.String() {
				t.Fatalf("applied resource creator = %q, want %s", got, actorID)
			}
		})
	}
}
