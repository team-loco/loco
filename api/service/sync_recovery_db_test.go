package service

import (
	"context"
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

func TestReadyAtTheCurrentRevisionRecoversAFailedDeployment(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	server := f.agentServer()

	deploymentID, err := f.deploy(ctx, staticSpec)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	p := f.placement(t, f.clusterID)
	server.recordApplied(ctx, f.clusterID, applied(p.ID, 1))

	server.recordStatus(ctx, f.clusterID, &agentv1.PlacementStatus{
		PlacementId: p.ID.String(), ObservedRevision: 1, Phase: applicationPhaseFailed, Message: "pull secret",
	})
	if status := f.deploymentStatus(t, deploymentID); status != genDb.DeploymentStatusFailed {
		t.Fatalf("deployment status = %s, want failed", status)
	}

	server.recordStatus(ctx, f.clusterID, &agentv1.PlacementStatus{
		PlacementId: p.ID.String(), ObservedRevision: 1, Ready: true, ReadyReplicas: 1, Phase: testPhaseReady,
	})
	if status := f.deploymentStatus(t, deploymentID); status != genDb.DeploymentStatusRunning {
		t.Fatalf("deployment status after a ready report = %s, want running", status)
	}
	d, err := f.queries.GetDeploymentByID(ctx, deploymentID)
	if err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	if d.CompletedAt != nil {
		t.Fatal("a running deployment kept its completed_at")
	}
}

func TestReadyAtAnOlderRevisionDoesNotRecoverAFailedDeployment(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	server := f.agentServer()

	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	second, err := f.deploy(ctx, staticSpec)
	if err != nil {
		t.Fatalf("second deploy: %v", err)
	}
	p := f.placement(t, f.clusterID)
	server.recordApplied(ctx, f.clusterID, &agentv1.Applied{
		PlacementId: p.ID.String(), Revision: 2, Error: "invalid spec",
	})
	if status := f.deploymentStatus(t, second); status != genDb.DeploymentStatusFailed {
		t.Fatalf("deployment status = %s, want failed", status)
	}

	server.recordStatus(ctx, f.clusterID, &agentv1.PlacementStatus{
		PlacementId: p.ID.String(), ObservedRevision: 1, Ready: true, ReadyReplicas: 1, Phase: testPhaseReady,
	})
	if status := f.deploymentStatus(t, second); status != genDb.DeploymentStatusFailed {
		t.Fatalf("a ready report for the previous revision moved the deployment to %s", status)
	}
}

func TestInventoryAheadOfTheDatabaseAdvancesThePlacement(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	server := f.agentServer()

	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	p := f.placement(t, f.clusterID)

	var sent []*agentv1.SyncResponse
	session := newSyncSession(server, f.clusterID, func(msg *agentv1.SyncResponse) error {
		sent = append(sent, msg)
		return nil
	})

	err := session.reconcileInventory(ctx, &agentv1.Inventory{Entries: []*agentv1.InventoryEntry{
		{PlacementId: p.ID.String(), Revision: 5},
	}})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if got := f.placement(t, f.clusterID); got.DesiredRevision != 6 {
		t.Fatalf("desired revision = %d, want 6", got.DesiredRevision)
	}
	if len(sent) != 1 || sent[0].GetApply().GetRevision() != 6 {
		t.Fatalf("sent %v, want one Apply at revision 6", sent)
	}

	server.recordApplied(ctx, f.clusterID, applied(p.ID, 6))
	if got := f.placement(t, f.clusterID); got.AppliedRevision != 6 {
		t.Fatalf("applied revision = %d, want 6", got.AppliedRevision)
	}

	err = withTx(ctx, f.pool, func(qtx *genDb.Queries) error {
		return removePlacement(ctx, qtx, f.resourceID, f.clusterID)
	})
	if err != nil {
		t.Fatalf("remove placement: %v", err)
	}
	sent = nil
	session = newSyncSession(server, f.clusterID, func(msg *agentv1.SyncResponse) error {
		sent = append(sent, msg)
		return nil
	})
	err = session.reconcileInventory(ctx, &agentv1.Inventory{Entries: []*agentv1.InventoryEntry{
		{PlacementId: p.ID.String(), Revision: 9},
	}})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	tombstone := f.placement(t, f.clusterID)
	if !tombstone.DesiredDeleted || tombstone.DesiredRevision != 10 {
		t.Fatalf(
			"tombstone = deleted %v rev %d, want deleted at 10",
			tombstone.DesiredDeleted,
			tombstone.DesiredRevision,
		)
	}
	if len(sent) != 1 || sent[0].GetDelete().GetRevision() != 10 {
		t.Fatalf("sent %v, want one Delete at revision 10", sent)
	}

	server.recordApplied(ctx, f.clusterID, applied(p.ID, 10))
	if n := f.count(t, `SELECT count(*) FROM placements WHERE resource_id = $1`); n != 0 {
		t.Fatal("tombstone not removed after the advanced delete was acked")
	}
}

func TestAPendingInventoryAheadOfARestoredDatabaseAdvancesWithoutMarkingApplied(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	server := f.agentServer()

	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	p := f.placement(t, f.clusterID)

	var sent []*agentv1.SyncResponse
	session := newSyncSession(server, f.clusterID, func(msg *agentv1.SyncResponse) error {
		sent = append(sent, msg)
		return nil
	})
	reconcile := func(revision int64, pending bool) {
		t.Helper()
		sent = nil
		err := session.reconcileInventory(ctx, &agentv1.Inventory{Entries: []*agentv1.InventoryEntry{
			{PlacementId: p.ID.String(), Revision: revision, EnvSecretPending: pending},
		}})
		if err != nil {
			t.Fatalf("reconcile: %v", err)
		}
	}

	reconcile(5, true)
	got := f.placement(t, f.clusterID)
	if got.DesiredRevision != 6 || got.AppliedRevision >= 5 {
		t.Fatalf("placement = desired %d applied %d, want desired 6 and nothing applied",
			got.DesiredRevision, got.AppliedRevision)
	}
	if len(sent) != 1 || sent[0].GetApply().GetRevision() != 6 {
		t.Fatalf("sent %v, want one Apply at revision 6", sent)
	}

	reconcile(6, true)
	if got = f.placement(t, f.clusterID); got.AppliedRevision == 6 {
		t.Fatal("a pending env Secret marked the placement applied")
	}
	if len(sent) != 1 || sent[0].GetApply().GetRevision() != 6 {
		t.Fatalf("sent %v, want the Apply at revision 6 again", sent)
	}

	reconcile(6, false)
	if got = f.placement(t, f.clusterID); got.AppliedRevision != 6 {
		t.Fatalf("applied revision = %d, want 6 once the env Secret is written", got.AppliedRevision)
	}
	if len(sent) != 0 {
		t.Fatalf("sent %v after a complete inventory, want nothing", sent)
	}
}
