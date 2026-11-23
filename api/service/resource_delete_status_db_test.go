package service

import (
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/db/dbtest"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

func TestDeleteResourceAndStatusReportDoNotDeadlock(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	created, err := createTestDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatal(err)
	}
	p := f.placement(t, f.clusterID)
	resourceServer := NewResourceServer(
		f.pool, f.queries, testServiceDefaults(), testClusterSilentAfter, limits.Defaults(),
	)
	admin := reqctx.WithPrincipal(t.Context(), reqctx.Principal{UserID: f.userID, Scopes: f.adminScopes(t)})

	release := sync.OnceFunc(f.holdResourceLock(t))
	t.Cleanup(release)
	var writers sync.WaitGroup
	writers.Go(func() {
		f.agentServer().recordStatus(t.Context(), f.clusterID, 0, &agentv1.PlacementStatus{
			PlacementId: p.ID.String(), DeploymentId: created.GetDeploymentId(), ObservedRevision: p.DesiredRevision,
			Available: true, Healthy: true, Deployed: true, ReadyReplicas: 1,
			Replicas: []*deploymentv1.Replica{{Uid: "pod", DeploymentId: created.GetDeploymentId(), Ready: true,
				State: string(loco.ReplicaServing), PlacementRevision: p.DesiredRevision}},
		})
	})
	dbtest.WaitForLockWaiters(t, f.pool, 1)
	var deleteErr error
	writers.Go(func() {
		_, deleteErr = resourceServer.DeleteResource(admin, &resourcev1.DeleteResourceRequest{
			ResourceId: f.resourceID.String(),
		})
	})
	dbtest.WaitForLockWaiters(t, f.pool, 2)
	release()
	writers.Wait()
	if deleteErr != nil {
		t.Fatal(deleteErr)
	}
	if n := deadlocks(t, f.pool); n != 0 {
		t.Fatalf("Postgres broke %d deadlocks", n)
	}
}

// deadlocks is how many transactions Postgres aborted to break a deadlock in the pool's database.
func deadlocks(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	ctx := t.Context()
	// A session publishes its statistics before it reports ready for the next query.
	for _, conn := range pool.AcquireAllIdle(ctx) {
		_, err := conn.Exec(ctx, "SELECT pg_stat_force_next_flush()")
		conn.Release()
		if err != nil {
			t.Fatalf("flush statistics: %v", err)
		}
	}
	var n int64
	if err := pool.QueryRow(
		ctx, "SELECT deadlocks FROM pg_stat_database WHERE datname = current_database()",
	).Scan(&n); err != nil {
		t.Fatalf("count deadlocks: %v", err)
	}
	return n
}
