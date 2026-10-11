package service

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func deleteFixtureWorkspace(t *testing.T, f *deployFixture, confirm bool) error {
	t.Helper()
	resource, err := f.queries.GetResourceByID(t.Context(), f.resourceID)
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: resource.WorkspaceID, Scope: genDb.ScopeAdmin},
	}
	ctx := context.WithValue(t.Context(), contextkeys.EntityScopesKey, scopes)
	server := NewWorkspaceServer(f.pool, f.queries, nil)
	workspaceID := resource.WorkspaceID.String()
	request := connect.NewRequest(&workspacev1.DeleteWorkspaceRequest{
		WorkspaceId: workspaceID, ConfirmDeleteApps: confirm,
	})
	_, err = server.DeleteWorkspace(ctx, request)
	return err
}

func TestDeleteWorkspaceRequiresConfirmationForResources(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	err := deleteFixtureWorkspace(t, f, false)
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("delete error = %v, want FailedPrecondition", err)
	}
	if got := f.placement(t, f.clusterID); got.DesiredDeleted {
		t.Fatal("unconfirmed deletion marked a placement deleted")
	}
	if _, err := f.queries.GetResourceByID(t.Context(), f.resourceID); err != nil {
		t.Fatalf("unconfirmed deletion removed the resource: %v", err)
	}
}

func TestDeleteWorkspaceConfirmationDeletesAllResourcePlacements(t *testing.T) {
	f := newDeployFixture(t)
	ctx := t.Context()
	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if _, err := f.deployTo(ctx, f.otherCluster, staticSpec); err != nil {
		t.Fatalf("deploy to other cluster: %v", err)
	}
	var sibling uuid.UUID
	if err := f.pool.QueryRow(ctx, `
INSERT INTO resources (workspace_id, name, type, description, status, spec, spec_version)
SELECT workspace_id, 'sibling', 'service', '', 'healthy', '{}', 1 FROM resources WHERE id = $1
RETURNING id`, f.resourceID).Scan(&sibling); err != nil {
		t.Fatalf("create sibling: %v", err)
	}
	if _, err := f.queries.UpsertPlacement(ctx, genDb.UpsertPlacementParams{
		ResourceID: sibling, ClusterID: f.clusterID, Region: testRegion, DesiredSpec: []byte(`{}`),
		EnvironmentID: f.envID, SecretNames: []string{},
	}); err != nil {
		t.Fatalf("place sibling: %v", err)
	}
	var unrelated, unrelatedEnvironment uuid.UUID
	if err := f.pool.QueryRow(ctx, `
WITH w AS (
    INSERT INTO workspaces (org_id, name, created_by)
    SELECT org_id, 'unrelated', created_by FROM workspaces
    WHERE id = (SELECT workspace_id FROM resources WHERE id = $1) RETURNING id, created_by
), e AS (
    INSERT INTO environments (workspace_id, name, created_by)
    SELECT id, 'prod', created_by FROM w RETURNING id
)
INSERT INTO resources (workspace_id, name, type, description, status, spec, spec_version)
SELECT id, 'unrelated', 'service', '', 'healthy', '{}', 1 FROM w
RETURNING id, (SELECT id FROM e)`, f.resourceID).Scan(&unrelated, &unrelatedEnvironment); err != nil {
		t.Fatalf("create unrelated resource: %v", err)
	}
	if _, err := f.queries.UpsertPlacement(ctx, genDb.UpsertPlacementParams{
		ResourceID: unrelated, ClusterID: f.clusterID, Region: testRegion, DesiredSpec: []byte(`{}`),
		EnvironmentID: unrelatedEnvironment, SecretNames: []string{},
	}); err != nil {
		t.Fatalf("place unrelated resource: %v", err)
	}
	if err := deleteFixtureWorkspace(t, f, true); err != nil {
		t.Fatalf("confirmed delete: %v", err)
	}
	for _, resourceID := range []uuid.UUID{f.resourceID, sibling} {
		var count int
		if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM resources WHERE id = $1`, resourceID).
			Scan(&count); err != nil ||
			count != 0 {
			t.Fatalf("remaining resources = %d, error = %v", count, err)
		}
		if err := f.pool.QueryRow(ctx, `
SELECT count(*) FROM placements WHERE resource_id = $1
AND (NOT desired_deleted OR desired_spec IS NOT NULL OR desired_revision != 2)`, resourceID).
			Scan(&count); err != nil ||
			count != 0 {
			t.Fatalf("placements without delete markers = %d, error = %v", count, err)
		}
	}
	var tombstones int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM placements WHERE desired_deleted`).
		Scan(&tombstones); err != nil ||
		tombstones != 3 {
		t.Fatalf("tombstones = %d, error = %v, want 3", tombstones, err)
	}
	if _, err := f.queries.GetResourceByID(ctx, unrelated); err != nil {
		t.Fatalf("unrelated resource was removed: %v", err)
	}
	var workspaces int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM workspaces`).Scan(&workspaces); err != nil || workspaces != 1 {
		t.Fatalf("remaining workspaces = %d, error = %v, want the unrelated workspace", workspaces, err)
	}
	var deletedEvents int
	if err := f.pool.QueryRow(ctx, `SELECT count(*) FROM events WHERE type IN ('resource.deleted', 'workspace.deleted')`).
		Scan(&deletedEvents); err != nil ||
		deletedEvents != 3 {
		t.Fatalf("delete events = %d, error = %v, want 3", deletedEvents, err)
	}
}

func TestDeleteWorkspaceRollsBackPlacementDeletion(t *testing.T) {
	f := newDeployFixture(t)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if _, err := f.pool.Exec(t.Context(), `
CREATE FUNCTION reject_workspace_delete() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'intentional delete failure'; END;
$$;
CREATE TRIGGER reject_workspace_delete BEFORE DELETE ON workspaces
FOR EACH ROW EXECUTE FUNCTION reject_workspace_delete();`); err != nil {
		t.Fatalf("create rejection trigger: %v", err)
	}
	err := deleteFixtureWorkspace(t, f, true)
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("delete error = %v, want Internal", err)
	}
	if got := f.placement(t, f.clusterID); got.DesiredDeleted || got.DesiredRevision != 1 {
		t.Fatalf("placement changed despite rollback: %+v", got)
	}
	if _, err := f.queries.GetResourceByID(t.Context(), f.resourceID); err != nil {
		t.Fatalf("resource changed despite rollback: %v", err)
	}
	var recorded int
	if err := f.pool.QueryRow(t.Context(), `
SELECT count(*) FROM events
WHERE type IN ('resource.deleted', 'workspace.deleted')`).Scan(&recorded); err != nil || recorded != 0 {
		t.Fatalf("events after rollback = %d, error = %v, want none", recorded, err)
	}
}
