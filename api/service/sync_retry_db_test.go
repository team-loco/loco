package service

import (
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

func (f *deployFixture) setApplyRetryPolicy(t *testing.T, maxAttempts int32, delaySeconds *int32) {
	t.Helper()
	if _, err := f.pool.Exec(
		t.Context(),
		`UPDATE resources SET max_apply_attempts = $2, apply_retry_delay_seconds = $3 WHERE id = $1`,
		f.resourceID,
		maxAttempts,
		delaySeconds,
	); err != nil {
		t.Fatal(err)
	}
}

func (f *deployFixture) expireRetry(t *testing.T) {
	t.Helper()
	if _, err := f.pool.Exec(
		t.Context(),
		`UPDATE placements SET retry_after = NOW() - INTERVAL '1 second' WHERE resource_id = $1`,
		f.resourceID,
	); err != nil {
		t.Fatal(err)
	}
}

func TestPermanentApplyFailuresWaitBeforeAnotherInventorySendsThem(t *testing.T) {
	f := newDeployFixture(t)
	f.setApplyRetryPolicy(t, 3, nil)
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
	f.expireRetry(t)
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
	if got := f.placement(t, f.clusterID); got.ApplyAttempts != 0 {
		t.Fatalf("retrying failure counted %d apply attempts", got.ApplyAttempts)
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

func TestPlacementFailsOnceItRunsOutOfApplyAttempts(t *testing.T) {
	f := newDeployFixture(t)
	f.setApplyRetryPolicy(t, 2, nil)
	server := f.agentServer()
	deploymentID, err := f.deploy(t.Context(), staticSpec)
	if err != nil {
		t.Fatal(err)
	}
	placement := f.placement(t, f.clusterID)
	failed := &agentv1.Applied{
		PlacementId: placement.ID.String(),
		Revision:    placement.DesiredRevision,
		Error:       "invalid application",
	}

	server.recordApplied(t.Context(), f.clusterID, failed)
	got := f.placement(t, f.clusterID)
	if got.ApplyAttempts != 1 || got.ApplyFailed || got.RetryAfter == nil {
		t.Fatalf(
			"first failure left attempts %d, failed %t, retry after %v",
			got.ApplyAttempts,
			got.ApplyFailed,
			got.RetryAfter,
		)
	}
	if status := f.deploymentStatus(t, deploymentID); status == genDb.DeploymentStatusFailed {
		t.Fatal("deployment failed while apply attempts remained")
	}

	f.expireRetry(t)
	server.recordApplied(t.Context(), f.clusterID, failed)
	got = f.placement(t, f.clusterID)
	if got.ApplyAttempts != 2 || !got.ApplyFailed || got.RetryAfter != nil {
		t.Fatalf(
			"last failure left attempts %d, failed %t, retry after %v",
			got.ApplyAttempts,
			got.ApplyFailed,
			got.RetryAfter,
		)
	}
	if status := f.deploymentStatus(t, deploymentID); status != genDb.DeploymentStatusFailed {
		t.Fatalf("deployment status = %s, want failed", status)
	}
	if sent := f.sendPending(t, server); len(sent) != 0 {
		t.Fatalf("fresh session resent %d failed placements", len(sent))
	}
	session := newSyncSession(server, f.clusterID, func(_ *agentv1.SyncResponse) error {
		t.Fatal("inventory resent a failed placement")
		return nil
	})
	if err := session.reconcilePlacementInventory(t.Context(), &agentv1.Inventory{}); err != nil {
		t.Fatal(err)
	}

	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatal(err)
	}
	got = f.placement(t, f.clusterID)
	if got.ApplyAttempts != 0 || got.ApplyFailed {
		t.Fatalf("new revision left attempts %d, failed %t", got.ApplyAttempts, got.ApplyFailed)
	}
	if sent := f.sendPending(t, server); len(sent) != 1 {
		t.Fatalf("new revision sent %d placements, want 1", len(sent))
	}
}

func TestResourceRetryDelayOverridesTheDefault(t *testing.T) {
	f := newDeployFixture(t)
	delaySeconds := int32(60)
	f.setApplyRetryPolicy(t, 3, &delaySeconds)
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
	var waited int32
	if err := f.pool.QueryRow(
		t.Context(),
		`SELECT EXTRACT(EPOCH FROM retry_after - updated_at)::int FROM placements WHERE id = $1`,
		placement.ID,
	).Scan(&waited); err != nil {
		t.Fatal(err)
	}
	if waited != delaySeconds {
		t.Fatalf("retry delay = %ds, want %ds", waited, delaySeconds)
	}
}
