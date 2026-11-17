package service

import (
	"context"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/authz"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func TestCreateWorkspaceRecordsTheCredentialActor(t *testing.T) {
	t.Parallel()
	for _, credential := range []genDb.DeploymentActorType{
		genDb.DeploymentActorTypeUser, genDb.DeploymentActorTypeApiToken,
	} {
		t.Run(string(credential), func(t *testing.T) {
			f := newSecretFixture(t)
			ctx := f.owner
			if credential == genDb.DeploymentActorTypeApiToken {
				ctx = creationTokenContext(t, f, genDb.EntityTypeOrganization, f.orgID, genDb.ScopeWrite)
			}
			principal, _ := reqctx.PrincipalFrom(ctx)
			server := NewWorkspaceServer(f.pool, f.queries, limits.Defaults())
			created, err := server.CreateWorkspace(ctx, &workspacev1.CreateWorkspaceRequest{
				OrgId: f.orgID.String(), Name: "automated",
			})
			if err != nil {
				t.Fatalf("create workspace: %v", err)
			}
			id := uuid.MustParse(created.GetWorkspaceId())
			workspace, err := f.queries.GetWorkspaceByIDQuery(ctx, id)
			if err != nil || workspace.CreatedBy != principal.ActorID {
				t.Fatalf("workspace creator = %s (%v), want %s", workspace.CreatedBy, err, principal.ActorID)
			}
			var environmentCreator uuid.UUID
			if scanErr := f.pool.QueryRow(ctx,
				"SELECT created_by FROM environments WHERE workspace_id = $1 AND name = 'production'", id,
			).Scan(&environmentCreator); scanErr != nil || environmentCreator != principal.ActorID {
				t.Fatalf(
					"default environment creator = %s (%v), want %s",
					environmentCreator,
					scanErr,
					principal.ActorID,
				)
			}
			scopes, err := authz.New(f.queries).UserScopes(ctx, f.ownerID)
			if err != nil {
				t.Fatal(err)
			}
			for _, scope := range []genDb.Scope{genDb.ScopeRead, genDb.ScopeWrite, genDb.ScopeAdmin} {
				want := genDb.EntityScope{EntityType: genDb.EntityTypeWorkspace, EntityID: id, Scope: scope}
				if got, wantGrant := slices.Contains(
					scopes,
					want,
				), credential == genDb.DeploymentActorTypeUser; got != wantGrant {
					t.Errorf("human creator %s grant = %v, want %v", scope, got, wantGrant)
				}
			}
			reader := creationTokenContext(t, f, genDb.EntityTypeOrganization, f.orgID, genDb.ScopeRead)
			_, err = server.CreateWorkspace(reader, &workspacev1.CreateWorkspaceRequest{
				OrgId: f.orgID.String(), Name: "read-only-workspace",
			})
			wantCode(t, err, connect.CodePermissionDenied)
		})
	}
}

func TestCreateEnvironmentRecordsTheCredentialActor(t *testing.T) {
	t.Parallel()
	for _, credential := range []genDb.DeploymentActorType{
		genDb.DeploymentActorTypeUser, genDb.DeploymentActorTypeApiToken,
	} {
		t.Run(string(credential), func(t *testing.T) {
			f := newSecretFixture(t)
			ctx := f.owner
			if credential == genDb.DeploymentActorTypeApiToken {
				ctx = creationTokenContext(t, f, genDb.EntityTypeWorkspace, f.workspaceID, genDb.ScopeWrite)
			}
			principal, _ := reqctx.PrincipalFrom(ctx)
			server := NewEnvironmentServer(f.pool, f.queries, limits.Defaults())
			request := &environmentv1.CreateEnvironmentRequest{
				WorkspaceId: f.workspaceID.String(), Name: "automated",
				Type: environmentv1.EnvironmentType_ENVIRONMENT_TYPE_STAGING,
			}
			created, err := server.CreateEnvironment(ctx, request)
			if err != nil {
				t.Fatalf("create environment: %v", err)
			}
			environment, err := f.queries.GetEnvironmentByID(ctx, uuid.MustParse(created.GetEnvironmentId()))
			if err != nil || environment.CreatedBy != principal.ActorID {
				t.Fatalf("environment creator = %s (%v), want %s", environment.CreatedBy, err, principal.ActorID)
			}
			reader := creationTokenContext(t, f, genDb.EntityTypeWorkspace, f.workspaceID, genDb.ScopeRead)
			request.Name = "read-only-environment"
			_, err = server.CreateEnvironment(reader, request)
			wantCode(t, err, connect.CodePermissionDenied)
			request.WorkspaceId = f.workspace(t, "another").String()
			_, err = server.CreateEnvironment(
				creationTokenContext(t, f, genDb.EntityTypeWorkspace, f.workspaceID, genDb.ScopeWrite), request,
			)
			wantCode(t, err, connect.CodePermissionDenied)
		})
	}
}

func creationTokenContext(
	t *testing.T,
	f *secretFixture,
	entityType genDb.EntityType,
	id uuid.UUID,
	scope genDb.Scope,
) context.Context {
	t.Helper()
	entity := genDb.Entity{Type: entityType, ID: id}
	scopes := []genDb.EntityScope{{EntityType: entityType, EntityID: id, Scope: scope}}
	token, _, err := f.machine.Issue(t.Context(), uuid.NewString(), f.ownerID, entity, scopes, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := f.machine.GetToken(t.Context(), token)
	if err != nil {
		t.Fatal(err)
	}
	return reqctx.WithPrincipal(t.Context(), principal)
}
