package service

import (
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/reqctx"
	environmentv1 "github.com/team-loco/loco/gen/go/loco/environment/v1"
)

func TestResourceReaderListsTheEnvironmentsOfTheirResourcesWorkspace(t *testing.T) {
	f := newDeployFixture(t)
	workspaceID := f.workspaceID(t)
	var otherResource uuid.UUID
	err := f.pool.QueryRow(t.Context(), `
WITH other AS (
  INSERT INTO workspaces (org_id, name, created_by)
  SELECT org_id, 'other', created_by FROM workspaces WHERE id = $1
  RETURNING id
)
INSERT INTO resources (workspace_id, name, slug, type, description, spec, spec_version)
SELECT id, 'elsewhere', 'elsewhere', 'service', '', '{}', 1 FROM other
RETURNING id`, workspaceID).Scan(&otherResource)
	if err != nil {
		t.Fatalf("create resource in another workspace: %v", err)
	}
	server := NewEnvironmentServer(f.pool, f.queries)
	list := func(scopes []genDb.EntityScope) ([]*environmentv1.Environment, error) {
		ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: scopes})
		resp, listErr := server.ListEnvironments(ctx, &environmentv1.ListEnvironmentsRequest{
			WorkspaceId: workspaceID.String(),
		})
		return resp.GetEnvironments(), listErr
	}
	readResource := func(id uuid.UUID) []genDb.EntityScope {
		return []genDb.EntityScope{{EntityType: genDb.EntityTypeResource, EntityID: id, Scope: genDb.ScopeRead}}
	}

	envs, err := list(readResource(f.resourceID))
	if err != nil {
		t.Fatalf("resource reader: %v", err)
	}
	if len(envs) != 1 {
		t.Fatalf("resource reader environments = %v, want one", envs)
	}
	env := envs[0]
	if env.GetId() != f.envID.String() || env.GetWorkspaceId() != workspaceID.String() || env.GetName() == "" ||
		env.GetType() == environmentv1.EnvironmentType_ENVIRONMENT_TYPE_UNSPECIFIED {
		t.Fatalf("resource reader got %+v, want id, workspace, name and type", env)
	}
	if env.Description != nil || env.GetCreatedBy() != "" || env.GetCreatedAt() != nil ||
		env.GetUpdatedAt() != nil || env.GetRevision() != 0 {
		t.Fatalf("resource reader got environment details: %+v", env)
	}

	envs, err = list(f.workspaceReadScopes(t))
	if err != nil || len(envs) != 1 || envs[0].GetCreatedBy() == "" || envs[0].GetCreatedAt() == nil {
		t.Fatalf("workspace reader got %v (%v), want the full environment", envs, err)
	}

	for name, scopes := range map[string][]genDb.EntityScope{
		"no grants":                       nil,
		"grant in another workspace only": readResource(otherResource),
	} {
		if _, err := list(scopes); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Errorf("%s: error = %v, want permission denied", name, err)
		}
	}
}
