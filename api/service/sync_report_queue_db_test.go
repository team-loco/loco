package service

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
)

// applyQueuedReports applies the reports the session queued so far, as its stream's worker does.
func applyQueuedReports(ctx context.Context, session *syncSession) {
	for {
		select {
		case apply := <-session.reports:
			apply(ctx)
		default:
			return
		}
	}
}

// openReportStream opens a sync stream as the cluster, discards what the API sends, and returns
// the stream to report on.
func openReportStream(
	t *testing.T,
	client agentv1connect.AgentServiceClient,
	clusterID uuid.UUID,
) agentv1connect.AgentServiceSyncClientStream {
	t.Helper()
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("Authorization", "Bearer "+testAssertion(t, clusterID))
	stream, err := client.Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inventory := &agentv1.SyncRequest{Message: &agentv1.SyncRequest_Inventory{Inventory: &agentv1.Inventory{}}}
	if err := stream.Send(inventory); err != nil {
		t.Fatalf("send inventory: %v", err)
	}
	go func() {
		for {
			if _, err := stream.Receive(); err != nil {
				return
			}
		}
	}()
	return stream
}

// holdResource locks the resource as a write does, until the returned release runs.
func holdResource(t *testing.T, f *deployFixture) func() {
	t.Helper()
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), `SELECT id FROM resources WHERE id = $1 FOR UPDATE`, f.resourceID); err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			if err := tx.Rollback(context.Background()); err != nil {
				t.Errorf("release resource: %v", err)
			}
		}
	}
	t.Cleanup(release)
	return release
}

// report sends the messages in order without waiting for the API to take them; a stalled stream shows
// in what the API records.
func report(stream agentv1connect.AgentServiceSyncClientStream, msgs ...*agentv1.SyncRequest) {
	go func() {
		for _, msg := range msgs {
			if stream.Send(msg) != nil {
				return
			}
		}
	}()
}

func statusReport(id uuid.UUID, revision int64, ready int32) *agentv1.SyncRequest {
	return &agentv1.SyncRequest{Message: &agentv1.SyncRequest_Status{Status: &agentv1.PlacementStatus{
		PlacementId: id.String(), ObservedRevision: revision, ReadyReplicas: ready,
	}}}
}

func heartbeatReport() *agentv1.SyncRequest {
	return &agentv1.SyncRequest{Message: &agentv1.SyncRequest_Heartbeat{Heartbeat: &agentv1.Heartbeat{
		Role: clusterv1.ClusterRole_CLUSTER_ROLE_ALL,
	}}}
}

// waitFor fails the test unless done holds within two seconds.
func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for: %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (f *deployFixture) heartbeatAt(t *testing.T) *time.Time {
	t.Helper()
	var at *time.Time
	if err := f.pool.QueryRow(t.Context(),
		`SELECT last_heartbeat FROM clusters WHERE id = $1`, f.clusterID).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

func TestHeartbeatsFlowWhileAStatusReportWaitsForAWrite(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	p := f.placement(t, f.clusterID)
	stream := openReportStream(t, startSyncServer(t, f), f.clusterID)

	release := holdResource(t, f)
	report(stream, statusReport(p.ID, p.DesiredRevision, 1), statusReport(p.ID, p.DesiredRevision, 2),
		heartbeatReport())
	waitFor(t, "heartbeat recorded while a write held the resource", func() bool {
		return f.heartbeatAt(t) != nil
	})
	if got := f.placement(t, f.clusterID); got.ReadyReplicas != 0 {
		t.Fatalf("a status report applied while a write held the resource: ready %d", got.ReadyReplicas)
	}

	release()
	waitFor(t, "the status reports applied once the write ended", func() bool {
		return f.placement(t, f.clusterID).ReadyReplicas == 2
	})
	time.Sleep(300 * time.Millisecond)
	if got := f.placement(t, f.clusterID); got.ReadyReplicas != 2 {
		t.Fatalf("ready replicas = %d after both reports, want the later report's 2", got.ReadyReplicas)
	}
}

func TestSupersededStreamReportWaitingForAWriteIsRefused(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	p := f.placement(t, f.clusterID)
	client := startSyncServer(t, f)
	first := openReportStream(t, client, f.clusterID)

	release := holdResource(t, f)
	report(first, statusReport(p.ID, p.DesiredRevision, 1), heartbeatReport())
	waitFor(t, "first stream's heartbeat recorded", func() bool {
		return f.heartbeatAt(t) != nil
	})
	before := f.syncGeneration(t)
	openReportStream(t, client, f.clusterID)
	waitFor(t, "a newer stream began while the first stream's report waited", func() bool {
		return f.syncGeneration(t) > before
	})

	release()
	time.Sleep(500 * time.Millisecond)
	if got := f.placement(t, f.clusterID); got.ReadyReplicas != 0 || got.ObservedRevision != 0 {
		t.Fatalf("a superseded stream's report was applied: observed %d ready %d", got.ObservedRevision,
			got.ReadyReplicas)
	}
}

func (f *deployFixture) syncGeneration(t *testing.T) int64 {
	t.Helper()
	var generation int64
	if err := f.pool.QueryRow(t.Context(),
		`SELECT sync_generation FROM clusters WHERE id = $1`, f.clusterID).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	return generation
}
