package service

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/clusternotify"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
)

func (f *deployFixture) agentServer() *AgentServer {
	return NewAgentServer(f.pool, f.queries, nil, nil, nil)
}

func applied(placementID uuid.UUID, revision int64) *agentv1.Applied {
	return &agentv1.Applied{PlacementId: placementID.String(), Revision: revision}
}

func TestLateAckNeverAcksNewerRevision(t *testing.T) {
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

	server.recordApplied(ctx, f.clusterID, applied(p.ID, 1))
	if got := f.placement(t, f.clusterID); got.AppliedRevision != 0 {
		t.Fatalf("applied revision after a late ack = %d, want 0", got.AppliedRevision)
	}
	if status := f.deploymentStatus(t, second); status != genDb.DeploymentStatusPending {
		t.Fatalf("deployment status after a late ack = %s, want pending", status)
	}

	server.recordApplied(ctx, f.otherCluster, applied(p.ID, 2))
	if got := f.placement(t, f.clusterID); got.AppliedRevision != 0 {
		t.Fatal("an ack from another cluster was accepted")
	}

	server.recordApplied(ctx, f.clusterID, applied(p.ID, 2))
	if got := f.placement(t, f.clusterID); got.AppliedRevision != 2 {
		t.Fatalf("applied revision = %d, want 2", got.AppliedRevision)
	}
	if status := f.deploymentStatus(t, second); status != genDb.DeploymentStatusDeploying {
		t.Fatalf("deployment status = %s, want deploying", status)
	}
}

func TestApplyErrorFailsDeploymentOnlyWhenNotRetrying(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	server := f.agentServer()

	id, err := f.deploy(ctx, staticSpec)
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	p := f.placement(t, f.clusterID)

	server.recordApplied(ctx, f.clusterID, &agentv1.Applied{
		PlacementId: p.ID.String(), Revision: 1, Error: "conflict", Retrying: true,
	})
	got := f.placement(t, f.clusterID)
	if got.AppliedError == nil || *got.AppliedError != "conflict" {
		t.Fatalf("applied error = %v, want conflict", got.AppliedError)
	}
	if status := f.deploymentStatus(t, id); status != genDb.DeploymentStatusPending {
		t.Fatalf("deployment status while retrying = %s, want pending", status)
	}

	server.recordApplied(ctx, f.clusterID, &agentv1.Applied{
		PlacementId: p.ID.String(), Revision: 1, Error: "invalid spec",
	})
	if status := f.deploymentStatus(t, id); status != genDb.DeploymentStatusFailed {
		t.Fatalf("deployment status = %s, want failed", status)
	}
	if got := f.placement(t, f.clusterID); got.AppliedRevision != 0 {
		t.Fatalf("a failed apply advanced the applied revision to %d", got.AppliedRevision)
	}
}

func TestDeletedPlacementIsKeptUntilTheDeleteIsAcked(t *testing.T) {
	f := newDeployFixture(t)
	ctx := context.Background()
	server := f.agentServer()

	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	p := f.placement(t, f.clusterID)
	server.recordApplied(ctx, f.clusterID, applied(p.ID, 1))

	err := withTx(ctx, f.pool, func(qtx *genDb.Queries) error {
		if removeErr := removeResourcePlacements(ctx, qtx, f.resourceID); removeErr != nil {
			return removeErr
		}
		return qtx.DeleteResource(ctx, f.resourceID)
	})
	if err != nil {
		t.Fatalf("delete resource: %v", err)
	}

	tombstone := f.placement(t, f.clusterID)
	if !tombstone.DesiredDeleted || tombstone.DesiredSpec != nil || tombstone.DesiredRevision != 2 {
		t.Fatalf("tombstone = deleted %v spec %q rev %d", tombstone.DesiredDeleted, tombstone.DesiredSpec,
			tombstone.DesiredRevision)
	}
	if tombstone.DeploymentID != nil {
		t.Fatalf("tombstone still references deployment %v", *tombstone.DeploymentID)
	}

	server.recordApplied(ctx, f.clusterID, applied(p.ID, 1))
	if n := f.count(t, `SELECT count(*) FROM placements WHERE resource_id = $1`); n != 1 {
		t.Fatal("a stale ack removed the tombstone")
	}

	server.recordApplied(ctx, f.clusterID, applied(p.ID, 2))
	if n := f.count(t, `SELECT count(*) FROM placements WHERE resource_id = $1`); n != 0 {
		t.Fatal("tombstone not removed after the delete was acked")
	}
}

func TestStatusAtAnOlderRevisionDoesNotMoveTheDeployment(t *testing.T) {
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

	ready := func(revision int64) *agentv1.PlacementStatus {
		return &agentv1.PlacementStatus{
			PlacementId:      p.ID.String(),
			ObservedRevision: revision,
			Ready:            true,
			ReadyReplicas:    1,
			Phase:            testPhaseReady,
		}
	}

	server.recordStatus(ctx, f.clusterID, ready(1))
	if status := f.deploymentStatus(t, second); status != genDb.DeploymentStatusPending {
		t.Fatalf("deployment status after an old-revision report = %s, want pending", status)
	}

	server.recordStatus(ctx, f.clusterID, ready(2))
	if status := f.deploymentStatus(t, second); status != genDb.DeploymentStatusRunning {
		t.Fatalf("deployment status = %s, want running", status)
	}

	server.recordStatus(ctx, f.clusterID, &agentv1.PlacementStatus{PlacementId: p.ID.String(), ObservedRevision: 1})
	if got := f.placement(t, f.clusterID); got.ObservedRevision != 2 || !got.Ready {
		t.Fatalf("an older report overwrote the status: observed %d ready %v", got.ObservedRevision, got.Ready)
	}
}

func TestInventoryResendsOnlyWhatDiffers(t *testing.T) {
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

	unknown := uuid.NewString()
	err := session.reconcileInventory(ctx, &agentv1.Inventory{Entries: []*agentv1.InventoryEntry{
		{PlacementId: unknown, Revision: 9},
	}})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(sent) != 1 || sent[0].GetApply().GetPlacementId() != p.ID.String() {
		t.Fatalf("sent %v, want one Apply for the missing placement", sent)
	}

	sent = nil
	err = session.reconcileInventory(ctx, &agentv1.Inventory{Entries: []*agentv1.InventoryEntry{
		{PlacementId: p.ID.String(), Revision: 1},
	}})
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(sent) != 0 {
		t.Fatalf("sent %v for an in-sync placement", sent)
	}
	if got := f.placement(t, f.clusterID); got.AppliedRevision != 1 {
		t.Fatalf("inventory did not record the applied revision: %d", got.AppliedRevision)
	}

	if n := f.count(t, `SELECT count(*) FROM placements WHERE resource_id = $1`); n != 1 {
		t.Fatal("an unknown inventory entry changed the placements")
	}

	err = withTx(ctx, f.pool, func(qtx *genDb.Queries) error {
		return removePlacement(ctx, qtx, f.resourceID, f.clusterID)
	})
	if err != nil {
		t.Fatalf("remove placement: %v", err)
	}
	sent = nil
	if err := session.reconcileInventory(ctx, &agentv1.Inventory{}); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if len(sent) != 1 || sent[0].GetDelete().GetRevision() != 2 {
		t.Fatalf("sent %v, want a Delete at revision 2", sent)
	}
}

func startSyncServer(t *testing.T, f *deployFixture) agentv1connect.AgentServiceClient {
	t.Helper()
	return startSyncServerWithSources(t, f, nil)
}

func startSyncServerWithSources(
	t *testing.T,
	f *deployFixture,
	sources SourceBucket,
) agentv1connect.AgentServiceClient {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	notifier := clusternotify.New(f.pool, 200*time.Millisecond)
	if err := notifier.Start(ctx); err != nil {
		t.Fatalf("start notifier: %v", err)
	}
	server := NewAgentServer(f.pool, f.queries, notifier, sources, nil)
	_, handler := agentv1connect.NewAgentServiceHandler(server)

	srv := httptest.NewUnstartedServer(handler)
	srv.EnableHTTP2 = true
	srv.StartTLS()
	t.Cleanup(func() {
		srv.CloseClientConnections()
		srv.Close()
	})
	return agentv1connect.NewAgentServiceClient(srv.Client(), srv.URL)
}

type syncResult struct {
	msg *agentv1.SyncResponse
	err error
}

func openSync(
	ctx context.Context,
	t *testing.T,
	client agentv1connect.AgentServiceClient,
) <-chan syncResult {
	t.Helper()
	stream := client.Sync(ctx)
	stream.RequestHeader().Set("Authorization", "Bearer "+testAgentToken)
	inventory := &agentv1.SyncRequest{Message: &agentv1.SyncRequest_Inventory{Inventory: &agentv1.Inventory{}}}
	if err := stream.Send(inventory); err != nil {
		t.Fatalf("send inventory: %v", err)
	}
	results := make(chan syncResult, 16)
	go func() {
		for {
			msg, err := stream.Receive()
			results <- syncResult{msg: msg, err: err}
			if err != nil {
				return
			}
		}
	}()
	return results
}

func nextResult(t *testing.T, results <-chan syncResult) syncResult {
	t.Helper()
	select {
	case r := <-results:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the sync stream")
	}
	return syncResult{}
}

func TestSyncStreamDeliversChangesAndYieldsToANewerStream(t *testing.T) {
	f := newDeployFixture(t)
	client := startSyncServer(t, f)
	ctx := t.Context()

	first := openSync(ctx, t, client)
	select {
	case r := <-first:
		t.Fatalf("unexpected message before any deploy: %v", r)
	case <-time.After(300 * time.Millisecond):
	}

	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	got := nextResult(t, first)
	if got.err != nil || got.msg.GetApply().GetRevision() != 1 {
		t.Fatalf("first stream got %v, %v; want an Apply at revision 1", got.msg, got.err)
	}

	second := openSync(ctx, t, client)
	got = nextResult(t, second)
	if got.err != nil || got.msg.GetApply().GetRevision() != 1 {
		t.Fatalf("second stream got %v, %v; want the unacked Apply", got.msg, got.err)
	}

	got = nextResult(t, first)
	if got.err == nil {
		t.Fatalf("older stream received %v after a newer one connected", got.msg)
	}
	if code := connect.CodeOf(got.err); code != connect.CodeAborted {
		t.Fatalf("older stream ended with %v, want aborted", got.err)
	}
}

func TestSyncStreamRejectsUnknownAgents(t *testing.T) {
	f := newDeployFixture(t)
	client := startSyncServer(t, f)
	ctx := t.Context()

	stream := client.Sync(ctx)
	stream.RequestHeader().Set("Authorization", "Bearer wrong")
	if sendErr := stream.Send(&agentv1.SyncRequest{}); sendErr != nil {
		t.Logf("send: %v", sendErr)
	}
	_, err := stream.Receive()
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("err = %v, want unauthenticated", err)
	}
}
