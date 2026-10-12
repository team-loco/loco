package service

import (
	"testing"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

func TestPermanentApplyFailuresWaitBeforeAnotherInventorySendsThem(t *testing.T) {
	f := newDeployFixture(t)
	server := f.agentServer()
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatal(err)
	}
	placement := f.placement(t, f.clusterID)
	failed := &agentv1.Applied{
		PlacementId: placement.ID.String(),
		Revision:    placement.DesiredRevision,
		Error:       "invalid application",
	}
	server.recordApplied(t.Context(), f.clusterID, failed)
	if sent := f.sendPending(t, server); len(sent) != 0 {
		t.Fatalf("fresh session resent %d permanently failed placements before its retry time", len(sent))
	}
	var sent int
	session := newSyncSession(server, f.clusterID, func(_ *agentv1.SyncResponse) error { sent++; return nil })
	inventory := &agentv1.Inventory{}
	if err := session.reconcilePlacementInventory(t.Context(), inventory); err != nil {
		t.Fatal(err)
	}
	if sent != 0 {
		t.Fatalf("inventory resent %d placements during backoff", sent)
	}
	if _, err := f.pool.Exec(
		t.Context(),
		`UPDATE placements SET retry_after = NOW() - INTERVAL '1 second' WHERE id = $1`,
		placement.ID,
	); err != nil {
		t.Fatal(err)
	}
	if err := session.reconcilePlacementInventory(t.Context(), inventory); err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Fatalf("expired backoff sent %d placements, want 1", sent)
	}
	server.recordApplied(t.Context(), f.clusterID, failed)
	if err := session.reconcilePlacementInventory(t.Context(), inventory); err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Fatal("another permanent failure did not restart backoff")
	}
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatal(err)
	}
	server.recordApplied(t.Context(), f.clusterID, failed)
	if err := session.sendPendingPlacements(t.Context()); err != nil {
		t.Fatal(err)
	}
	if sent != 2 {
		t.Fatal("a new desired revision remained blocked by the old failure")
	}
}

func TestRetryingApplyErrorsDoNotDeferDelivery(t *testing.T) {
	f := newDeployFixture(t)
	server := f.agentServer()
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatal(err)
	}
	placement := f.placement(t, f.clusterID)
	server.recordApplied(
		t.Context(),
		f.clusterID,
		&agentv1.Applied{
			PlacementId: placement.ID.String(),
			Revision:    placement.DesiredRevision,
			Error:       "resource version changed",
			Retrying:    true,
		},
	)
	if sent := f.sendPending(t, server); len(sent) != 1 {
		t.Fatalf("retrying failure sent %d placements, want 1", len(sent))
	}
}

func TestCurrentInventoryRecoversDuringApplyBackoff(t *testing.T) {
	f := newDeployFixture(t)
	server := f.agentServer()
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatal(err)
	}
	placement := f.placement(t, f.clusterID)
	server.recordApplied(
		t.Context(),
		f.clusterID,
		&agentv1.Applied{PlacementId: placement.ID.String(), Revision: placement.DesiredRevision, Error: "apply error"},
	)
	session := newSyncSession(server, f.clusterID, func(_ *agentv1.SyncResponse) error {
		t.Fatal("current inventory must not resend the placement")
		return nil
	})
	inventory := &agentv1.Inventory{
		Entries: []*agentv1.InventoryEntry{{PlacementId: placement.ID.String(), Revision: placement.DesiredRevision}},
	}
	if err := session.reconcilePlacementInventory(t.Context(), inventory); err != nil {
		t.Fatal(err)
	}
	got := f.placement(t, f.clusterID)
	if got.RetryAfter != nil || got.AppliedRevision != placement.DesiredRevision {
		t.Fatalf("current inventory left backoff %v and applied revision %d", got.RetryAfter, got.AppliedRevision)
	}
}
