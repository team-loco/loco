package service

import (
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func TestWorkspaceInventoryForgetsOnlyWorkspacesTheDatabaseDoesNotHold(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	workspaceID := f.workspaceID(t)
	var sent []*agentv1.SyncResponse
	session := newSyncSession(f.agentServer(), f.clusterID, func(msg *agentv1.SyncResponse) error {
		sent = append(sent, msg)
		return nil
	})
	inventory := &agentv1.Inventory{WorkspaceIds: []string{workspaceID.String()}}
	if err := session.reconcileInventory(t.Context(), inventory); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].GetDeleteWorkspace() != workspaceID.String() {
		t.Fatalf("an existing workspace without placements: sent %v, want only DeleteWorkspace", sent)
	}

	if err := f.queries.DeleteWorkspace(t.Context(), workspaceID); err != nil {
		t.Fatal(err)
	}
	sent = nil
	if err := session.reconcileInventory(t.Context(), inventory); err != nil {
		t.Fatal(err)
	}
	if len(sent) != 1 || sent[0].GetForgetWorkspace() != workspaceID.String() {
		t.Fatalf("a workspace the database does not hold: sent %v, want ForgetWorkspace", sent)
	}
}

func TestWorkspaceInventoryRetainsEmptyWorkspacesAndRetriesDeletedWorkspaces(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	workspaceID := f.workspaceID(t)
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeAdmin},
	}})
	if err := f.queries.OpenPlacementWindow(ctx, genDb.OpenPlacementWindowParams{
		ClusterID: f.clusterID, ResourceID: f.resourceID,
	}); err != nil {
		t.Fatal(err)
	}
	var deleted []string
	session := newSyncSession(f.agentServer(), f.clusterID, func(msg *agentv1.SyncResponse) error {
		deleted = append(deleted, msg.GetForgetWorkspace())
		return nil
	})
	inventory := &agentv1.Inventory{WorkspaceIds: []string{workspaceID.String()}}
	if err := session.reconcileInventory(ctx, inventory); err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 0 {
		t.Fatalf("live workspace deletion requested: %v", deleted)
	}
	server := NewWorkspaceServer(f.pool, f.queries, limits.Defaults())
	if _, err := server.DeleteWorkspace(
		ctx,
		&workspacev1.DeleteWorkspaceRequest{WorkspaceId: workspaceID.String(), ConfirmDeleteApps: true},
	); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := session.reconcileInventory(ctx, inventory); err != nil {
			t.Fatal(err)
		}
	}
	if len(deleted) != 2 || deleted[0] != workspaceID.String() || deleted[1] != workspaceID.String() {
		t.Fatalf("workspace deletions = %v", deleted)
	}
	if err := session.reconcileInventory(ctx, &agentv1.Inventory{}); err != nil {
		t.Fatal(err)
	}
	if len(deleted) != 2 {
		t.Fatalf("cleanup repeated after namespace disappeared: %v", deleted)
	}
}

func TestWorkspaceInventoryDeletesNamespacesOfClustersWithoutPlacementsAfterTheGrace(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	workspaceID := f.workspaceID(t)
	ctx := t.Context()
	var deleted []string
	session := newSyncSession(f.agentServer(), f.clusterID, func(msg *agentv1.SyncResponse) error {
		deleted = append(deleted, msg.GetDeleteWorkspace())
		return nil
	})
	inventory := &agentv1.Inventory{WorkspaceIds: []string{workspaceID.String()}}
	reconcile := func(want int) {
		t.Helper()
		if err := session.reconcileInventory(ctx, inventory); err != nil {
			t.Fatal(err)
		}
		if len(deleted) != want {
			t.Fatalf("workspace deletions = %v, want %d", deleted, want)
		}
	}
	if err := f.queries.OpenPlacementWindow(ctx, genDb.OpenPlacementWindowParams{
		ClusterID: f.clusterID, ResourceID: f.resourceID,
	}); err != nil {
		t.Fatal(err)
	}
	reconcile(0)
	removeWindow := func(removedAgo string) {
		t.Helper()
		const update = "UPDATE placement_windows " +
			"SET placed_at = NOW() - interval '1 day', removed_at = NOW() - $2::interval WHERE cluster_id = $1"
		if _, err := f.pool.Exec(ctx, update, f.clusterID, removedAgo); err != nil {
			t.Fatal(err)
		}
	}
	removeWindow("1 minute")
	reconcile(0)
	removeWindow("2 hours")
	reconcile(1)
	if deleted[0] != workspaceID.String() {
		t.Fatalf("deleted %v, want %s", deleted, workspaceID)
	}
}
