package service

import (
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

func TestInventoryDoesNotReconcileUntilTheFinalChunk(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	if _, err := f.deploy(f.ctx, staticSpec); err != nil {
		t.Fatal(err)
	}
	placement := f.placement(t, f.clusterID)
	held := f.insertBuild(t, genDb.BuildStatusRunning)
	missing := f.insertBuild(t, genDb.BuildStatusRunning)
	session, sent := f.dispatchSession()
	chunk := &agentv1.Inventory{Entries: []*agentv1.InventoryEntry{{
		PlacementId: placement.ID.String(), Revision: placement.DesiredRevision,
	}}}
	if err := session.handleNow(f.ctx, inventoryChunk(chunk)); err != nil {
		t.Fatal(err)
	}
	if got := f.placement(t, f.clusterID); got.AppliedRevision != 0 {
		t.Fatal("partial snapshot marked a placement applied")
	}
	if got := f.row(t, missing).Status; got != genDb.BuildStatusRunning {
		t.Fatalf("partial snapshot changed a missing build to %s", got)
	}
	if got := sent.take(); len(got) != 0 {
		t.Fatalf("partial snapshot sent reconciliation commands: %v", got)
	}
	final := &agentv1.Inventory{Complete: true, Builds: []*agentv1.InventoryBuild{{
		BuildId: held, Phase: agentv1.BuildPhase_BUILD_PHASE_RUNNING,
	}}}
	if err := session.handleNow(f.ctx, inventoryChunk(final)); err != nil {
		t.Fatal(err)
	}
	if got := f.placement(t, f.clusterID); got.AppliedRevision != placement.DesiredRevision {
		t.Fatal("complete snapshot lost the placement from the first chunk")
	}
	if got := f.row(t, held).Status; got != genDb.BuildStatusRunning {
		t.Fatalf("complete snapshot lost the held build: %s", got)
	}
	if got := f.row(t, missing).Status; got != genDb.BuildStatusFailed {
		t.Fatalf("complete snapshot did not reconcile the missing build: %s", got)
	}
}

func TestDisconnectedInventoryIsNotCarriedIntoANewSyncSession(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	held := f.insertBuild(t, genDb.BuildStatusRunning)
	session, sent := f.dispatchSession()
	partial := &agentv1.Inventory{Builds: []*agentv1.InventoryBuild{{
		BuildId: held, Phase: agentv1.BuildPhase_BUILD_PHASE_RUNNING,
	}}}
	if err := session.handleNow(f.ctx, inventoryChunk(partial)); err != nil {
		t.Fatal(err)
	}
	if got := f.row(t, held).Status; got != genDb.BuildStatusRunning {
		t.Fatalf("disconnected partial snapshot changed the build: %s", got)
	}
	resynced := newSyncSession(session.server, f.clusterID, sent.send)
	if err := resynced.handleNow(f.ctx, inventoryChunk(&agentv1.Inventory{Complete: true})); err != nil {
		t.Fatal(err)
	}
	if got := f.row(t, held).Status; got != genDb.BuildStatusFailed {
		t.Fatalf("new session retained a build from the old partial snapshot: %s", got)
	}
}

func inventoryChunk(inventory *agentv1.Inventory) *agentv1.SyncRequest {
	return &agentv1.SyncRequest{Message: &agentv1.SyncRequest_InventoryChunk{InventoryChunk: inventory}}
}
