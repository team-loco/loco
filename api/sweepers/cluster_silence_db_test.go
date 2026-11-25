package sweepers

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/team-loco/loco/api/db/dbtest"
	genDb "github.com/team-loco/loco/api/gen/db"
)

const testSilentAfter = 90 * time.Second

func (f *fixture) setHeartbeat(t *testing.T, cluster uuid.UUID, heardAgo time.Duration) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, `
INSERT INTO cluster_heartbeats (cluster_id, last_heartbeat, health_status) VALUES ($1, $2, 'healthy')
ON CONFLICT (cluster_id) DO UPDATE SET last_heartbeat = EXCLUDED.last_heartbeat`,
		cluster, time.Now().Add(-heardAgo),
	); err != nil {
		t.Fatal(err)
	}
}

// clusterEvents returns the data of the cluster's events of the type.
func (f *fixture) clusterEvents(t *testing.T, eventType string, cluster uuid.UUID) []map[string]any {
	t.Helper()
	rows, err := f.pool.Query(
		f.ctx,
		`SELECT data FROM events WHERE type = $1 AND subject_id = $2 AND org_id IS NULL ORDER BY seq`,
		eventType,
		cluster,
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

func (f *fixture) sweepClusters(t *testing.T) {
	t.Helper()
	if _, err := NewClusterSilenceSweeper(f.pool, testSilentAfter, time.Minute).Sweep(f.ctx); err != nil {
		t.Fatal(err)
	}
}

func TestClusterSilenceRecordsOneSilentEventPerOutage(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.setHeartbeat(t, f.clusterID, 10*time.Minute)

	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() { f.sweepClusters(t) })
	}
	wg.Wait()
	f.sweepClusters(t)
	silent := f.clusterEvents(t, "cluster.silent", f.clusterID)
	if len(silent) != 1 {
		t.Fatalf("cluster.silent events = %v, want one", silent)
	}
	if silent[0]["name"] != "c1" || silent[0]["region"] != "us-ca-1" {
		t.Fatalf("cluster.silent data = %v, want c1 in us-ca-1", silent[0])
	}
}

func TestClusterSilenceWaitsForAPauseThatIsCommitting(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.setHeartbeat(t, f.clusterID, 10*time.Minute)
	tx, err := f.pool.Begin(f.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(f.ctx)
	q := genDb.New(tx)
	if _, err = q.LockClusterLifecycle(f.ctx, f.clusterID); err != nil {
		t.Fatal(err)
	}
	if err = q.UpdateClusterLifecycle(f.ctx, genDb.UpdateClusterLifecycleParams{ID: f.clusterID}); err != nil {
		t.Fatal(err)
	}

	swept := make(chan int, 1)
	go func() {
		silenced, sweepErr := NewClusterSilenceSweeper(f.pool, testSilentAfter, time.Minute).Sweep(f.ctx)
		if sweepErr != nil {
			t.Error(sweepErr)
		}
		swept <- silenced
	}()
	dbtest.WaitForLockWaiters(t, f.pool, 1)
	if err = tx.Commit(f.ctx); err != nil {
		t.Fatal(err)
	}
	if silenced := <-swept; silenced != 0 {
		t.Fatalf("sweep silenced %d clusters, want none for a cluster paused before the sweep read it", silenced)
	}
	if got := f.clusterEvents(t, "cluster.silent", f.clusterID); len(got) != 0 {
		t.Fatalf("paused cluster recorded %v", got)
	}
}

func TestClusterSilenceSkipsHeardPausedRemovedAndNeverReportedClusters(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	var paused, removed, heard uuid.UUID
	if err := f.pool.QueryRow(f.ctx, `
WITH p AS (INSERT INTO clusters (platforms, name, region, provider, is_active, is_default)
           VALUES (ARRAY['linux/amd64'], 'paused', 'us-ca-1', 'kind', false, false) RETURNING id),
     r AS (INSERT INTO clusters (platforms, name, region, provider, is_active, is_default, removed_at)
           VALUES (ARRAY['linux/amd64'], 'removed', 'us-ca-1', 'kind', false, false, now()) RETURNING id),
     h AS (INSERT INTO clusters (platforms, name, region, provider, is_active, is_default)
           VALUES (ARRAY['linux/amd64'], 'heard', 'us-ca-1', 'kind', true, false) RETURNING id)
SELECT p.id, r.id, h.id FROM p, r, h`).Scan(&paused, &removed, &heard); err != nil {
		t.Fatal(err)
	}
	for _, id := range []uuid.UUID{paused, removed} {
		f.setHeartbeat(t, id, time.Hour)
	}
	f.setHeartbeat(t, heard, time.Second)
	f.sweepClusters(t)
	for name, id := range map[string]uuid.UUID{
		"paused": paused, "removed": removed, "heard": heard, "never reported": f.otherCluster,
	} {
		if got := f.clusterEvents(t, "cluster.silent", id); len(got) != 0 {
			t.Errorf("%s cluster recorded %v", name, got)
		}
	}
}
