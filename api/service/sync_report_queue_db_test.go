package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	"github.com/team-loco/loco/gen/go/loco/agent/v1/agentv1connect"
	buildv1 "github.com/team-loco/loco/gen/go/loco/build/v1"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
)

// applyQueuedReports applies and settles the reports the session queued, as its stream does.
func applyQueuedReports(ctx context.Context, session *syncSession) error {
	for {
		select {
		case apply := <-session.reports:
			apply(ctx)
		default:
			if err := session.settle(ctx); err != nil {
				return err
			}
			if len(session.reports) == 0 {
				return nil
			}
		}
	}
}

// sendPendingNow sends what is pending, then applies and settles what it queued, as a sync stream does.
func (ss *syncSession) sendPendingNow(ctx context.Context) error {
	if err := ss.sendPending(ctx); err != nil {
		return err
	}
	return applyQueuedReports(ctx, ss)
}

// handleBuildStatus applies and settles a build report as a sync stream does.
func (ss *syncSession) handleBuildStatus(ctx context.Context, status *agentv1.BuildStatus) error {
	return ss.handleNow(ctx, &agentv1.SyncRequest{Message: &agentv1.SyncRequest_BuildStatus{BuildStatus: status}})
}

// reconcileInventory applies and settles an inventory as a sync stream does.
func (ss *syncSession) reconcileInventory(ctx context.Context, inventory *agentv1.Inventory) error {
	return ss.handleNow(ctx, &agentv1.SyncRequest{Message: &agentv1.SyncRequest_Inventory{Inventory: inventory}})
}

// handleNow handles a message, then applies and settles what it queued, as a sync stream does.
func (ss *syncSession) handleNow(ctx context.Context, msg *agentv1.SyncRequest) error {
	if err := ss.handle(ctx, msg); err != nil {
		return err
	}
	return applyQueuedReports(ctx, ss)
}

// openReportStream opens a sync stream as the cluster, discards what the API sends, and returns
// the stream to report on.
func openReportStream(
	t *testing.T,
	client agentv1connect.AgentServiceClient,
	clusterID uuid.UUID,
) agentv1connect.AgentServiceSyncClientStream {
	t.Helper()
	return openReportStreamReceiving(t, client, clusterID, nil)
}

// openReportStreamReceiving opens a sync stream as the cluster, passes what the API sends to received
// unless it is nil, and returns the stream to report on.
func openReportStreamReceiving(
	t *testing.T,
	client agentv1connect.AgentServiceClient,
	clusterID uuid.UUID,
	received chan<- *agentv1.SyncResponse,
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
			msg, err := stream.Receive()
			if err != nil {
				return
			}
			if received == nil {
				continue
			}
			select {
			case received <- msg:
			case <-ctx.Done():
				return
			}
		}
	}()
	return stream
}

// holdResource locks the resource as a write does, until the returned release runs.
func holdResource(t *testing.T, f *deployFixture) func() {
	t.Helper()
	return holdRow(t, f, `SELECT id FROM resources WHERE id = $1 FOR UPDATE`, f.resourceID)
}

// holdRow locks the row the query selects as a write does, until the returned release runs.
func holdRow(t *testing.T, f *deployFixture, query string, id uuid.UUID) func() {
	t.Helper()
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), query, id); err != nil {
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
	if err := f.pool.QueryRow(
		t.Context(),
		`SELECT (SELECT last_heartbeat FROM cluster_heartbeats WHERE cluster_id = $1)`,
		f.clusterID,
	).Scan(&at); err != nil {
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

func TestHeartbeatsFlowWhileAReportWaitsForAWrite(t *testing.T) {
	t.Parallel()
	const lockPlacement = `SELECT id FROM placements WHERE id = $1 FOR UPDATE`
	const lockResource = `SELECT id FROM resources WHERE id = $1 FOR UPDATE`
	cases := map[string]struct {
		lock   string
		report func(p genDb.Placement, buildID string) *agentv1.SyncRequest
		done   func(t *testing.T, f *buildFixture, buildID string) bool
	}{
		"failed apply": {
			lock: lockResource,
			report: func(p genDb.Placement, _ string) *agentv1.SyncRequest {
				failure := applied(p.ID, p.DesiredRevision)
				failure.Error = testApplyFailure
				return &agentv1.SyncRequest{Message: &agentv1.SyncRequest_Applied{Applied: failure}}
			},
			done: func(t *testing.T, f *buildFixture, _ string) bool {
				t.Helper()
				return f.placement(t, f.clusterID).AppliedError != nil
			},
		},
		"applied": {
			lock: lockPlacement,
			report: func(p genDb.Placement, _ string) *agentv1.SyncRequest {
				return &agentv1.SyncRequest{Message: &agentv1.SyncRequest_Applied{
					Applied: applied(p.ID, p.DesiredRevision),
				}}
			},
			done: placementApplied,
		},
		"inventory": {
			lock: lockPlacement,
			report: func(p genDb.Placement, _ string) *agentv1.SyncRequest {
				entry := &agentv1.InventoryEntry{PlacementId: p.ID.String(), Revision: p.DesiredRevision}
				return &agentv1.SyncRequest{Message: &agentv1.SyncRequest_Inventory{
					Inventory: &agentv1.Inventory{Entries: []*agentv1.InventoryEntry{entry}},
				}}
			},
			done: placementApplied,
		},
		"build status": {
			lock: lockResource,
			report: func(_ genDb.Placement, buildID string) *agentv1.SyncRequest {
				return &agentv1.SyncRequest{Message: &agentv1.SyncRequest_BuildStatus{
					BuildStatus: buildStatus(buildID, agentv1.BuildPhase_BUILD_PHASE_RUNNING),
				}}
			},
			done: func(t *testing.T, f *buildFixture, buildID string) bool {
				t.Helper()
				return f.get(t, buildID).GetStatus() == buildv1.BuildStatus_BUILD_STATUS_RUNNING
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newBuildFixture(t)
			if _, err := f.deploy(t.Context(), staticSpec); err != nil {
				t.Fatalf("deploy: %v", err)
			}
			buildID := f.queued(t)
			p := f.placement(t, f.clusterID)
			stream := openReportStream(t, startSyncServer(t, f.deployFixture), f.clusterID)

			held := p.ID
			if tc.lock == lockResource {
				held = f.resourceID
			}
			if _, err := f.pool.Exec(t.Context(),
				`DELETE FROM cluster_heartbeats WHERE cluster_id = $1`, f.clusterID); err != nil {
				t.Fatal(err)
			}
			release := holdRow(t, f.deployFixture, tc.lock, held)
			report(stream, tc.report(p, buildID), heartbeatReport())
			waitFor(t, "heartbeat recorded while a write held the row the report needs", func() bool {
				return f.heartbeatAt(t) != nil
			})
			if tc.done(t, f, buildID) {
				t.Fatal("the report applied while a write held the row it needs")
			}

			release()
			waitFor(t, "the report applied once the write ended", func() bool { return tc.done(t, f, buildID) })
		})
	}
}

func TestHeartbeatsFlowWhileStartingQueuedBuildsWaitsForAWrite(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		role clusterv1.ClusterRole
		lock string
		done func(t *testing.T, f *buildFixture, buildID string, received []*agentv1.SyncResponse) bool
	}{
		"registry credential": {
			role: clusterv1.ClusterRole_CLUSTER_ROLE_ALL,
			lock: `SELECT id FROM builds WHERE id = $1 FOR UPDATE`,
			done: func(_ *testing.T, _ *buildFixture, buildID string, received []*agentv1.SyncResponse) bool {
				return startedWithCredential(received, buildID)
			},
		},
		"builds disabled": {
			role: clusterv1.ClusterRole_CLUSTER_ROLE_APPS,
			lock: `SELECT id FROM resources WHERE id = $1 FOR UPDATE`,
			done: func(t *testing.T, f *buildFixture, buildID string, _ []*agentv1.SyncResponse) bool {
				t.Helper()
				return f.get(t, buildID).GetStatus() == buildv1.BuildStatus_BUILD_STATUS_FAILED
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newBuildFixture(t)
			buildID := f.queued(t)
			held := uuid.MustParse(buildID)
			if tc.role == clusterv1.ClusterRole_CLUSTER_ROLE_APPS {
				held = f.resourceID
			}
			if _, err := f.pool.Exec(t.Context(),
				`UPDATE clusters SET last_heartbeat = NULL, role = $2 WHERE id = $1`,
				f.clusterID, clusterRoleFromProto(tc.role)); err != nil {
				t.Fatal(err)
			}
			release := holdRow(t, f.deployFixture, tc.lock, held)
			cfg := testAgentConfig()
			cfg.RegistryTokenMode = true
			messages := make(chan *agentv1.SyncResponse, 64)
			client := startSyncServerWithConfig(t, f.deployFixture, f.bucket, cfg)
			stream := openReportStreamReceiving(t, client, f.clusterID, messages)
			var received []*agentv1.SyncResponse
			done := func() bool {
				for {
					select {
					case msg := <-messages:
						received = append(received, msg)
					default:
						return tc.done(t, f, buildID, received)
					}
				}
			}

			dbtest.WaitForLockWaiters(t, f.pool, 1)
			report(stream, &agentv1.SyncRequest{Message: &agentv1.SyncRequest_Heartbeat{
				Heartbeat: &agentv1.Heartbeat{Role: tc.role},
			}})
			waitFor(t, "heartbeat recorded while starting queued builds waited for a write", func() bool {
				return f.heartbeatAt(t) != nil
			})
			if done() {
				t.Fatal("the queued build was handled while a write held the row it needs")
			}

			release()
			waitFor(t, "the queued build was handled once the write ended", done)
		})
	}
}

// startedWithCredential reports whether received holds a StartBuild for the build with a registry credential.
func startedWithCredential(received []*agentv1.SyncResponse, buildID string) bool {
	for _, start := range startedBuilds(received) {
		if start.GetBuildId() == buildID && start.GetRegistryCredential() != "" {
			return true
		}
	}
	return false
}

func TestAQueuedBuildIsSentWhileALaterBuildWaitsForAWrite(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	first := f.queued(t)
	second := f.insertBuild(t, genDb.BuildStatusQueued)
	if _, err := f.pool.Exec(t.Context(),
		`UPDATE clusters SET last_heartbeat = NULL WHERE id = $1`, f.clusterID); err != nil {
		t.Fatal(err)
	}
	release := holdRow(t, f.deployFixture, `SELECT id FROM builds WHERE id = $1 FOR UPDATE`, uuid.MustParse(second))
	cfg := testAgentConfig()
	cfg.RegistryTokenMode = true
	messages := make(chan *agentv1.SyncResponse, 64)
	client := startSyncServerWithConfig(t, f.deployFixture, f.bucket, cfg)
	stream := openReportStreamReceiving(t, client, f.clusterID, messages)
	var received []*agentv1.SyncResponse
	started := func(buildID string) bool {
		for {
			select {
			case msg := <-messages:
				received = append(received, msg)
			default:
				return startedWithCredential(received, buildID)
			}
		}
	}

	dbtest.WaitForLockWaiters(t, f.pool, 1)
	report(stream, heartbeatReport())
	waitFor(t, "the first build sent while a write held the second", func() bool { return started(first) })
	waitFor(t, "heartbeat recorded while a write held the second build", func() bool {
		return f.heartbeatAt(t) != nil
	})
	if started(second) {
		t.Fatal("the second build was sent while a write held it")
	}

	release()
	waitFor(t, "the second build sent once the write ended", func() bool { return started(second) })
}

func TestAnInventoryMissingAPlacementResendsIt(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	p := f.placement(t, f.clusterID)
	if _, err := f.pool.Exec(t.Context(),
		`UPDATE placements SET applied_revision = desired_revision WHERE id = $1`, p.ID); err != nil {
		t.Fatal(err)
	}
	ctx, info := connect.NewClientContext(t.Context())
	info.RequestHeader().Set("Authorization", "Bearer "+testAssertion(t, f.clusterID))
	stream, err := startSyncServer(t, f).Sync(ctx)
	if err != nil {
		t.Fatal(err)
	}
	inventory := func(entries ...*agentv1.InventoryEntry) *agentv1.SyncRequest {
		return &agentv1.SyncRequest{Message: &agentv1.SyncRequest_Inventory{
			Inventory: &agentv1.Inventory{Entries: entries},
		}}
	}
	held := &agentv1.InventoryEntry{PlacementId: p.ID.String(), Revision: p.DesiredRevision}
	if err := stream.Send(inventory(held)); err != nil {
		t.Fatalf("send inventory: %v", err)
	}
	applies := make(chan *agentv1.Apply, 4)
	go func() {
		for {
			msg, err := stream.Receive()
			if err != nil {
				return
			}
			if apply := msg.GetApply(); apply != nil {
				applies <- apply
			}
		}
	}()
	select {
	case apply := <-applies:
		t.Fatalf("sent %v to a cluster that holds it", apply)
	case <-time.After(500 * time.Millisecond):
	}

	if err := stream.Send(inventory()); err != nil {
		t.Fatalf("send inventory: %v", err)
	}
	select {
	case apply := <-applies:
		if apply.GetPlacementId() != p.ID.String() {
			t.Fatalf("resent %s, want %s", apply.GetPlacementId(), p.ID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an inventory without the placement did not resend it")
	}
}

func TestASupersededStreamSkipsItsQueuedReports(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	p := f.placement(t, f.clusterID)
	session := newSyncSession(f.agentServer(), f.clusterID, func(*agentv1.SyncResponse) error { return nil })
	session.generation = f.syncGeneration(t)
	msg := &agentv1.SyncRequest{Message: &agentv1.SyncRequest_Applied{Applied: applied(p.ID, p.DesiredRevision)}}
	if err := session.handle(t.Context(), msg); err != nil {
		t.Fatalf("queue applied: %v", err)
	}
	if _, err := f.pool.Exec(t.Context(),
		`UPDATE clusters SET sync_generation = sync_generation + 1 WHERE id = $1`, f.clusterID); err != nil {
		t.Fatal(err)
	}
	if err := applyQueuedReports(t.Context(), session); !errors.Is(err, errSyncSuperseded) {
		t.Fatalf("settle = %v, want %v", err, errSyncSuperseded)
	}
	if got := f.placement(t, f.clusterID); got.AppliedRevision == p.DesiredRevision {
		t.Fatal("a superseded stream's applied report was recorded")
	}
}

func placementApplied(t *testing.T, f *buildFixture, _ string) bool {
	t.Helper()
	p := f.placement(t, f.clusterID)
	return p.AppliedRevision == p.DesiredRevision
}

func TestSupersededStreamReportWaitingForAWriteIsRefused(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	p := f.placement(t, f.clusterID)
	generation := f.syncGeneration(t)

	release := holdResource(t, f)
	recorded := make(chan struct{})
	go func() {
		defer close(recorded)
		f.agentServer().recordStatus(context.Background(), f.clusterID, generation, &agentv1.PlacementStatus{
			PlacementId: p.ID.String(), ObservedRevision: p.DesiredRevision, ReadyReplicas: 1,
		})
	}()
	time.Sleep(200 * time.Millisecond)

	superseded := make(chan error, 1)
	go func() {
		_, err := f.pool.Exec(context.Background(),
			`UPDATE clusters SET sync_generation = sync_generation + 1 WHERE id = $1`, f.clusterID)
		superseded <- err
	}()
	select {
	case err := <-superseded:
		if err != nil {
			t.Fatalf("supersede the stream: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a status report waiting for a write held up a newer stream")
	}

	release()
	<-recorded
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
