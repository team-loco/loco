package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/proto"

	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
)

func TestAHeartbeatDoesNotWaitForAWriteThatSharesTheCluster(t *testing.T) {
	t.Parallel()
	locks := map[string]func(context.Context, *genDb.Queries, uuid.UUID) error{
		"assignable cluster": func(ctx context.Context, q *genDb.Queries, id uuid.UUID) error {
			_, err := q.LockAssignableCluster(ctx, id)
			return err
		},
		"sync generation": func(ctx context.Context, q *genDb.Queries, id uuid.UUID) error {
			_, err := q.LockClusterSyncGeneration(ctx, id)
			return err
		},
	}
	for name, lock := range locks {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newDeployFixture(t)
			server := f.agentServer()
			server.recordHeartbeat(t.Context(), f.clusterID, healthyHeartbeat())
			first := f.heartbeatAt(t)
			holdTx(t, f, func(ctx context.Context, tx pgx.Tx) error { return lock(ctx, genDb.New(tx), f.clusterID) })

			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			server.recordHeartbeat(ctx, f.clusterID, &agentv1.Heartbeat{Role: clusterv1.ClusterRole_CLUSTER_ROLE_ALL})
			if at, health := f.storedHeartbeat(t); !at.After(*first) || health != clusterHealthDegraded {
				t.Fatalf("heartbeat at %v (first %v), health %q, want a later degraded heartbeat", at, first, health)
			}
		})
	}
}

func TestHeartbeatsFlowWhileARoleChangeWaitsForADeployThatCheckedIt(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.agentServer().recordHeartbeat(t.Context(), f.clusterID, healthyHeartbeat())
	stream := openReportStream(t, startSyncServer(t, f), f.clusterID)
	release := holdTx(t, f, func(ctx context.Context, tx pgx.Tx) error {
		_, err := genDb.New(tx).LockAssignableCluster(ctx, f.clusterID)
		return err
	})

	builds := healthyHeartbeat()
	builds.Role = clusterv1.ClusterRole_CLUSTER_ROLE_BUILDS
	report(stream, heartbeatMessage(builds))
	dbtest.WaitForLockWaiters(t, f.pool, 1)
	first, _ := f.storedHeartbeat(t)

	apps := proto.CloneOf(builds)
	apps.Role = clusterv1.ClusterRole_CLUSTER_ROLE_APPS
	apps.Health.ControllerHealthy = false
	report(stream, heartbeatMessage(apps))
	waitFor(t, "a second heartbeat recorded while the role change waits", func() bool {
		at, health := f.storedHeartbeat(t)
		return at.After(first) && health == clusterHealthDegraded
	})
	if role := f.role(t); role != genDb.ClusterRoleAll {
		t.Fatalf("role = %s while a deploy that checked it is open, want all", role)
	}

	release()
	waitFor(t, "the newest role stored after the deploy", func() bool { return f.role(t) == genDb.ClusterRoleApps })
}

func TestAnUnchangedCapabilityReportDoesNotWaitForADeploy(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	reportCapabilities(t, f.agentServer(), f.clusterID, healthyHeartbeat())
	generation, err := f.queries.GetClusterSyncGeneration(t.Context(), f.clusterID)
	if err != nil {
		t.Fatal(err)
	}
	holdTx(t, f, func(ctx context.Context, tx pgx.Tx) error {
		_, err := genDb.New(tx).LockAssignableCluster(ctx, f.clusterID)
		return err
	})

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := f.queries.UpdateClusterReport(ctx, genDb.UpdateClusterReportParams{
		ID: f.clusterID, Generation: generation, Role: genDb.ClusterRoleAll,
		Domains: []string{}, Platforms: []string{testPlatform},
	}); err != nil {
		t.Fatalf("an unchanged report while a deploy held the cluster: %v", err)
	}
}

func TestAnInvalidHeartbeatChangesNothing(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	session := newSyncSession(f.agentServer(), f.clusterID, func(*agentv1.SyncResponse) error { return nil })
	if err := session.handle(t.Context(), heartbeatMessage(&agentv1.Heartbeat{})); err != nil {
		t.Fatal(err)
	}
	if at := f.heartbeatAt(t); at != nil || len(session.capabilities) != 0 {
		t.Fatalf("an invalid heartbeat stored liveness at %v and queued %d capabilities, want neither",
			at, len(session.capabilities))
	}
}

func TestAStaleGenerationDoesNotStoreCapabilities(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	stale, err := f.queries.GetClusterSyncGeneration(t.Context(), f.clusterID)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := tx.Rollback(context.Background()); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			t.Errorf("roll back: %v", err)
		}
	})
	supersede := `UPDATE clusters SET sync_generation = sync_generation + 1 WHERE id = $1`
	if _, err := tx.Exec(t.Context(), supersede, f.clusterID); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	updated := make(chan error, 1)
	var update sync.WaitGroup
	t.Cleanup(update.Wait)
	update.Go(func() {
		updated <- f.queries.UpdateClusterReport(ctx, genDb.UpdateClusterReportParams{
			ID: f.clusterID, Generation: stale, Role: genDb.ClusterRoleBuilds,
			Domains: []string{}, Platforms: []string{testPlatform},
		})
	})
	dbtest.WaitForLockWaiters(t, f.pool, 1)
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := <-updated; err != nil {
		t.Fatalf("update capabilities: %v", err)
	}
	if role := f.role(t); role != genDb.ClusterRoleAll {
		t.Fatalf("role = %s after an update from a stale generation, want all", role)
	}
}

func TestHeartbeatsDoNotWaitForAFullReportQueue(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	session := newSyncSession(f.agentServer(), f.clusterID, func(*agentv1.SyncResponse) error { return nil })
	for range cap(session.reports) {
		session.reports <- func(context.Context) {}
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	healthy := healthyHeartbeat()
	if err := session.handle(ctx, heartbeatMessage(healthy)); err != nil {
		t.Fatalf("first heartbeat: %v", err)
	}
	first, _ := f.storedHeartbeat(t)
	degraded := proto.CloneOf(healthy)
	degraded.Health.ControllerHealthy = false
	if err := session.handle(ctx, heartbeatMessage(degraded)); err != nil {
		t.Fatalf("second heartbeat: %v", err)
	}
	if at, health := f.storedHeartbeat(t); !at.After(first) || health != clusterHealthDegraded {
		t.Fatalf("heartbeat at %v (first %v), health %q, want a later degraded heartbeat", at, first, health)
	}
}

func TestHeartbeatsKeepOnlyTheNewestCapabilities(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	session := newSyncSession(f.agentServer(), f.clusterID, func(*agentv1.SyncResponse) error { return nil })
	for _, role := range []clusterv1.ClusterRole{
		clusterv1.ClusterRole_CLUSTER_ROLE_APPS, clusterv1.ClusterRole_CLUSTER_ROLE_BUILDS,
	} {
		heartbeat := healthyHeartbeat()
		heartbeat.Role = role
		if err := session.handle(t.Context(), heartbeatMessage(heartbeat)); err != nil {
			t.Fatal(err)
		}
	}
	if pending := len(session.reports) + len(session.capabilities); pending != 1 {
		t.Fatalf("%d updates pending for two heartbeats, want 1", pending)
	}
	if err := applyQueuedReports(t.Context(), session); err != nil {
		t.Fatal(err)
	}
	if role := f.role(t); role != genDb.ClusterRoleBuilds {
		t.Fatalf("role = %s, want the newest heartbeat's builds", role)
	}
}

func heartbeatMessage(heartbeat *agentv1.Heartbeat) *agentv1.SyncRequest {
	return &agentv1.SyncRequest{Message: &agentv1.SyncRequest_Heartbeat{Heartbeat: heartbeat}}
}

// reportCapabilities stores the heartbeat's capabilities as the cluster's current stream does.
func reportCapabilities(t *testing.T, server *AgentServer, clusterID uuid.UUID, heartbeat *agentv1.Heartbeat) {
	t.Helper()
	generation, err := server.queries.GetClusterSyncGeneration(t.Context(), clusterID)
	if err != nil {
		t.Fatal(err)
	}
	server.recordClusterReport(t.Context(), clusterID, generation, heartbeat)
}

func healthyHeartbeat() *agentv1.Heartbeat {
	return &agentv1.Heartbeat{
		Role:     clusterv1.ClusterRole_CLUSTER_ROLE_ALL,
		Health:   &agentv1.AgentHealth{KubernetesHealthy: true, ControllerHealthy: true},
		Capacity: &agentv1.AgentCapacity{Platforms: []string{testPlatform}},
	}
}

func (f *deployFixture) storedHeartbeat(t *testing.T) (time.Time, string) {
	t.Helper()
	var at time.Time
	var health string
	if err := f.pool.QueryRow(t.Context(),
		`SELECT last_heartbeat, health_status FROM cluster_heartbeats WHERE cluster_id = $1`, f.clusterID,
	).Scan(&at, &health); err != nil {
		t.Fatal(err)
	}
	return at, health
}

func (f *deployFixture) role(t *testing.T) genDb.ClusterRole {
	t.Helper()
	role, err := f.queries.GetClusterRole(t.Context(), f.clusterID)
	if err != nil {
		t.Fatal(err)
	}
	return role
}

// setHeartbeats records a heartbeat at the time and health for the clusters, or for every cluster
// when none is given.
func setHeartbeats(t *testing.T, pool *pgxpool.Pool, at time.Time, health string, ids ...uuid.UUID) {
	t.Helper()
	upsert := `
INSERT INTO cluster_heartbeats (cluster_id, last_heartbeat, health_status)
SELECT id, $2, $3 FROM clusters WHERE cardinality($1::uuid[]) = 0 OR id = ANY($1::uuid[])
ON CONFLICT (cluster_id) DO UPDATE
SET last_heartbeat = EXCLUDED.last_heartbeat, health_status = EXCLUDED.health_status`
	if _, err := pool.Exec(context.Background(), upsert, append([]uuid.UUID{}, ids...), at, health); err != nil {
		t.Fatalf("set heartbeats: %v", err)
	}
}

func (f *deployFixture) healthChanges(t *testing.T) []map[string]any {
	t.Helper()
	rows, err := f.pool.Query(
		t.Context(),
		`SELECT data FROM events WHERE type = 'cluster.health_changed' AND subject_id = $1 AND org_id IS NULL ORDER BY seq`,
		f.clusterID,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var all []map[string]any
	for rows.Next() {
		var data map[string]any
		if err := rows.Scan(&data); err != nil {
			t.Fatal(err)
		}
		all = append(all, data)
	}
	return all
}

func TestHeartbeatRecordsOneEventPerHealthChange(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	server := f.agentServer()
	degraded := &agentv1.Heartbeat{
		Role:   clusterv1.ClusterRole_CLUSTER_ROLE_ALL,
		Health: &agentv1.AgentHealth{KubernetesHealthy: true},
	}

	server.recordHeartbeat(t.Context(), f.clusterID, healthyHeartbeat())
	server.recordHeartbeat(t.Context(), f.clusterID, healthyHeartbeat())
	if changes := f.healthChanges(t); len(changes) != 0 {
		t.Fatalf("events for the first and a repeated healthy heartbeat = %v, want none", changes)
	}

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() { server.recordHeartbeat(t.Context(), f.clusterID, degraded) })
	}
	wg.Wait()
	changes := f.healthChanges(t)
	if len(changes) != 1 || changes[0]["old"] != clusterHealthHealthy || changes[0]["new"] != clusterHealthDegraded ||
		changes[0]["region"] == "" || changes[0]["name"] == "" {
		t.Fatalf(
			"events after concurrent degraded heartbeats = %v, want one healthy to degraded naming the cluster",
			changes,
		)
	}

	server.recordHeartbeat(t.Context(), f.clusterID, healthyHeartbeat())
	if changes := f.healthChanges(t); len(changes) != 2 || changes[1]["new"] != clusterHealthHealthy {
		t.Fatalf("events after recovery = %v, want a second one ending healthy", changes)
	}
}
