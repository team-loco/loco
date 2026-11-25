package service

import (
	"context"
	"errors"
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

	degraded := proto.CloneOf(builds)
	degraded.Health.ControllerHealthy = false
	report(stream, heartbeatMessage(degraded))
	waitFor(t, "a second heartbeat recorded while the role change waits", func() bool {
		at, health := f.storedHeartbeat(t)
		return at.After(first) && health == clusterHealthDegraded
	})
	if role := f.role(t); role != genDb.ClusterRoleAll {
		t.Fatalf("role = %s while a deploy that checked it is open, want all", role)
	}

	release()
	waitFor(t, "the role changed once the deploy ended", func() bool { return f.role(t) == genDb.ClusterRoleBuilds })
}

func TestAnUnchangedCapabilityReportDoesNotWaitForADeploy(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := f.deploy(t.Context(), staticSpec); err != nil {
		t.Fatalf("deploy: %v", err)
	}
	p := f.placement(t, f.clusterID)
	reportCapabilities(t, f.agentServer(), f.clusterID, healthyHeartbeat())
	stream := openReportStream(t, startSyncServer(t, f), f.clusterID)
	holdTx(t, f, func(ctx context.Context, tx pgx.Tx) error {
		_, err := genDb.New(tx).LockAssignableCluster(ctx, f.clusterID)
		return err
	})

	report(stream, heartbeatMessage(healthyHeartbeat()),
		&agentv1.SyncRequest{Message: &agentv1.SyncRequest_Applied{Applied: applied(p.ID, p.DesiredRevision)}})
	waitFor(t, "the report queued after an unchanged heartbeat applied while a deploy held the cluster", func() bool {
		got := f.placement(t, f.clusterID)
		return got.AppliedRevision == got.DesiredRevision
	})
}

func TestASupersededStreamDoesNotStoreItsCapabilities(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	stream := openReportStream(t, startSyncServer(t, f), f.clusterID)
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

	builds := healthyHeartbeat()
	builds.Role = clusterv1.ClusterRole_CLUSTER_ROLE_BUILDS
	report(stream, heartbeatMessage(builds))
	dbtest.WaitForLockWaiters(t, f.pool, 1)
	if err := tx.Commit(t.Context()); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the waiting capability update finished", func() bool {
		var running bool
		if err := f.pool.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
			WHERE datname = current_database() AND state = 'active' AND pid <> pg_backend_pid()
			  AND query LIKE '%name: UpdateClusterReport%')`).Scan(&running); err != nil {
			t.Fatal(err)
		}
		return !running
	})
	if role := f.role(t); role != genDb.ClusterRoleAll {
		t.Fatalf("role = %s after a superseded stream's report, want all", role)
	}
}

func TestHeartbeatsQueueOneCapabilityUpdateAtATime(t *testing.T) {
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
	if queued := len(session.reports); queued != 1 {
		t.Fatalf("%d capability updates queued for two heartbeats, want 1", queued)
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
