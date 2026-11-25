package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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
	report(stream, &agentv1.SyncRequest{Message: &agentv1.SyncRequest_Heartbeat{Heartbeat: builds}})
	dbtest.WaitForLockWaiters(t, f.pool, 1)
	first, _ := f.storedHeartbeat(t)

	degraded := &agentv1.Heartbeat{Role: clusterv1.ClusterRole_CLUSTER_ROLE_BUILDS}
	report(stream, &agentv1.SyncRequest{Message: &agentv1.SyncRequest_Heartbeat{Heartbeat: degraded}})
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
