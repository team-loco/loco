package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
)

// holdCluster runs lock in a transaction, as a deploy or a status report does, until the returned
// release runs.
func holdCluster(t *testing.T, f *deployFixture, lock func(context.Context, *genDb.Queries) error) func() {
	t.Helper()
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := lock(t.Context(), genDb.New(tx)); err != nil {
		t.Fatal(err)
	}
	released := false
	release := func() {
		if !released {
			released = true
			if err := tx.Rollback(context.Background()); err != nil {
				t.Errorf("release cluster: %v", err)
			}
		}
	}
	t.Cleanup(release)
	return release
}

// sendHeartbeat records the heartbeat in the background and returns a channel closed once it is recorded.
func sendHeartbeat(t *testing.T, f *deployFixture, heartbeat *agentv1.Heartbeat) <-chan struct{} {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.agentServer().recordHeartbeat(context.Background(), f.clusterID, heartbeat)
	}()
	return done
}

func waitClosed(t *testing.T, what string, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for: %s", what)
	}
}

func sameReportHeartbeat() *agentv1.Heartbeat {
	return &agentv1.Heartbeat{
		Role:     clusterv1.ClusterRole_CLUSTER_ROLE_ALL,
		Health:   &agentv1.AgentHealth{KubernetesHealthy: true, ControllerHealthy: true},
		Capacity: &agentv1.AgentCapacity{Platforms: []string{testPlatform}},
	}
}

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
			waitClosed(t, "first heartbeat recorded", sendHeartbeat(t, f, sameReportHeartbeat()))
			holdCluster(t, f, func(ctx context.Context, q *genDb.Queries) error { return lock(ctx, q, f.clusterID) })

			first := f.heartbeatAt(t)
			waitClosed(t, "heartbeat recorded while a write shared the cluster",
				sendHeartbeat(t, f, sameReportHeartbeat()))
			if !f.heartbeatAt(t).After(*first) {
				t.Fatal("the heartbeat was not recorded")
			}
		})
	}
}

func TestAHeartbeatChangingTheRoleWaitsForADeployThatCheckedIt(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	release := holdCluster(t, f, func(ctx context.Context, q *genDb.Queries) error {
		_, err := q.LockAssignableCluster(ctx, f.clusterID)
		return err
	})

	heartbeat := sameReportHeartbeat()
	heartbeat.Role = clusterv1.ClusterRole_CLUSTER_ROLE_BUILDS
	done := sendHeartbeat(t, f, heartbeat)
	waitFor(t, "heartbeat recorded while a deploy held the cluster", func() bool { return f.heartbeatAt(t) != nil })
	select {
	case <-done:
		t.Fatal("the role changed while a deploy that checked it was open")
	case <-time.After(300 * time.Millisecond):
	}

	release()
	waitClosed(t, "the role changed once the deploy ended", done)
	role, err := f.queries.GetClusterRole(t.Context(), f.clusterID)
	if err != nil || role != genDb.ClusterRoleBuilds {
		t.Fatalf("role = %v, err = %v, want builds", role, err)
	}
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
