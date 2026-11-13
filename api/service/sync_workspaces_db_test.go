package service

import (
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	workspacev1 "github.com/team-loco/loco/gen/go/loco/workspace/v1"
)

func TestWorkspaceInventoryRetainsEmptyWorkspacesAndRetriesDeletedWorkspaces(t *testing.T) {
	f := newDeployFixture(t)
	workspaceID := f.workspaceID(t)
	ctx := reqctx.WithPrincipal(t.Context(), reqctx.Principal{Scopes: []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: workspaceID, Scope: genDb.ScopeAdmin},
	}})
	var deleted []string
	session := newSyncSession(f.agentServer(), f.clusterID, func(msg *agentv1.SyncResponse) error {
		deleted = append(deleted, msg.GetDeleteWorkspace())
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
