package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

func TestClusterLifecyclePreservesIdentityAndFencesStreams(t *testing.T) {
	f := newDeployFixture(t)
	ctx := t.Context()
	firstReplica := startSyncServer(t, f)
	secondReplica := startSyncServer(t, f)
	first := openSync(ctx, t, firstReplica, f.clusterID)
	select {
	case result := <-first:
		t.Fatalf("unexpected stream result: %+v", result)
	case <-time.After(100 * time.Millisecond):
	}
	second := openSync(ctx, t, secondReplica, f.otherCluster)
	select {
	case result := <-second:
		t.Fatalf("unexpected stream result: %+v", result)
	case <-time.After(100 * time.Millisecond):
	}
	for _, id := range []uuid.UUID{f.clusterID, f.otherCluster} {
		if _, err := ManageCluster(ctx, f.pool, ClusterRemove, id, false); !errors.Is(err, ErrClusterNotPaused) {
			t.Fatalf("remove active cluster: %v", err)
		}
		if _, err := ManageCluster(ctx, f.pool, ClusterPause, id, false); err != nil {
			t.Fatal(err)
		}
		health := &agentv1.AgentHealth{KubernetesHealthy: true, ControllerHealthy: true}
		f.agentServer().recordHeartbeat(ctx, id, &agentv1.Heartbeat{Health: health})
		row, err := f.queries.GetClusterByID(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if row.IsActive {
			t.Fatal("heartbeat resumed paused cluster")
		}
		if _, err := ManageCluster(ctx, f.pool, ClusterRemove, id, false); err != nil {
			t.Fatal(err)
		}
	}
	for _, results := range []<-chan syncResult{first, second} {
		got := nextResult(t, results)
		if connect.CodeOf(got.err) != connect.CodePermissionDenied {
			t.Fatalf("removed stream: %v", got.err)
		}
	}
	if _, err := ManageCluster(ctx, f.pool, ClusterResume, f.clusterID, false); !errors.Is(err, ErrClusterRemoved) {
		t.Fatalf("resume removed: %v", err)
	}
	if _, err := ManageCluster(ctx, f.pool, ClusterRestore, f.clusterID, false); err != nil {
		t.Fatal(err)
	}
	restored, err := f.queries.GetClusterByID(ctx, f.clusterID)
	if err != nil {
		t.Fatal(err)
	}
	if restored.IsActive || restored.LastHeartbeat != nil || restored.HealthStatus != nil {
		t.Fatalf("restore reused health or resumed: %+v", restored)
	}
	if _, err := ManageCluster(ctx, f.pool, ClusterResume, f.clusterID, false); err != nil {
		t.Fatal(err)
	}
	reconnected := openSync(ctx, t, firstReplica, f.clusterID)
	select {
	case result := <-reconnected:
		t.Fatalf("restored reconnect failed: %+v", result)
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := ManageCluster(ctx, f.pool, ClusterPause, f.clusterID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := ManageCluster(ctx, f.pool, ClusterRemove, f.clusterID, false); err != nil {
		t.Fatal(err)
	}
	if got := nextResult(t, reconnected); connect.CodeOf(got.err) != connect.CodePermissionDenied {
		t.Fatalf("restored stream removal: %v", got.err)
	}
	if _, err := ManageCluster(ctx, f.pool, ClusterPause, uuid.New(), false); !errors.Is(err, ErrClusterNotFound) {
		t.Fatalf("missing cluster: %v", err)
	}
}

func TestClusterRemovalRefusesWorkloadsAndForceReportsThem(t *testing.T) {
	f := newBuildFixture(t)
	ctx := t.Context()
	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatal(err)
	}
	if _, err := ManageCluster(ctx, f.pool, ClusterPause, f.clusterID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := f.deploy(ctx, staticSpec); !errors.Is(err, ErrClusterNotAssignable) {
		t.Fatalf("deploy while paused: %v", err)
	}
	buildID := uuid.New()
	if _, err := queueBuild(ctx, f.pool, buildID, f.resourceID, f.clusterID); !errors.Is(err, ErrClusterNotAssignable) {
		t.Fatalf("queue build while paused: %v", err)
	}
	result, err := ManageCluster(ctx, f.pool, ClusterRemove, f.clusterID, false)
	if !errors.Is(err, ErrClusterHasWorkloads) || len(result.Workloads) != 2 {
		t.Fatalf("normal removal: %v workloads %+v", err, result.Workloads)
	}
	result, err = ManageCluster(ctx, f.pool, ClusterRemove, f.clusterID, true)
	if err != nil || len(result.Workloads) != 2 {
		t.Fatalf("force removal: %v workloads %+v", err, result.Workloads)
	}
	if _, err := f.deploy(ctx, staticSpec); !errors.Is(err, ErrClusterNotAssignable) {
		t.Fatalf("deploy while removed: %v", err)
	}
	var rows int
	if err := f.pool.QueryRow(ctx, "SELECT count(*) FROM deployments WHERE cluster_id = $1", f.clusterID).
		Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("deployment history changed: %d", rows)
	}
}

func TestPauseWaitsForAnInFlightAssignment(t *testing.T) {
	f := newDeployFixture(t)
	ctx := t.Context()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	qtx := genDb.New(tx)
	if lockErr := lockAssignableCluster(ctx, qtx, f.clusterID); lockErr != nil {
		t.Fatal(lockErr)
	}
	result := make(chan error, 1)
	go func() {
		_, pauseErr := ManageCluster(ctx, f.pool, ClusterPause, f.clusterID, false)
		result <- pauseErr
	}()
	select {
	case pauseErr := <-result:
		t.Fatalf("pause passed an in-flight assignment: %v", pauseErr)
	case <-time.After(100 * time.Millisecond):
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		t.Fatal(commitErr)
	}
	select {
	case pauseErr := <-result:
		if pauseErr != nil {
			t.Fatal(pauseErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("pause remained blocked")
	}
	tx, err = f.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockAssignableCluster(ctx, genDb.New(tx), f.clusterID); !errors.Is(err, ErrClusterNotAssignable) {
		t.Fatalf("assignment after pause: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		t.Fatal(err)
	}
}

func TestSyncRecordsCredentialUsedByTheConnection(t *testing.T) {
	f := newDeployFixture(t)
	const original = "original-rotation-secret"
	const replacement = "replacement-rotation-secret"
	cfg := testAgentConfig()
	cfg.ConnectionSecrets = []string{original, replacement}
	client := startSyncServerWithConfig(t, f, nil, cfg)
	ctx := t.Context()
	first := openSyncWithToken(ctx, t, client, f.clusterID, original)
	waitForSyncGeneration(t, f, f.clusterID, 1)
	requireFingerprint := func(secret string) {
		t.Helper()
		rows, err := f.queries.ListOperatorClusters(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.ID == f.clusterID {
				if row.ConnectionCredentialFingerprint == nil ||
					*row.ConnectionCredentialFingerprint != hashToken(secret) {
					t.Fatalf("unexpected fingerprint: %v", row.ConnectionCredentialFingerprint)
				}
				return
			}
		}
		t.Fatal("connected cluster missing from operator list")
	}
	requireFingerprint(original)
	registerCtx, info := connect.NewClientContext(ctx)
	info.RequestHeader().Set("Authorization", "Bearer "+replacement)
	_, err := client.Register(registerCtx, &agentv1.RegisterRequest{
		ClusterKey:    f.clusterID.String(),
		DisplayName:   "c1",
		ClusterRegion: attachmentRegion,
		Tier:          environmentTypeProduction,
	})
	if err != nil {
		t.Fatal(err)
	}
	requireFingerprint(original)
	second := openSyncWithToken(ctx, t, client, f.clusterID, replacement)
	waitForSyncGeneration(t, f, f.clusterID, 2)
	requireFingerprint(replacement)
	if got := nextResult(t, first); connect.CodeOf(got.err) != connect.CodeAborted {
		t.Fatalf("superseded stream: %v", got.err)
	}
	if _, err := ManageCluster(ctx, f.pool, ClusterPause, f.clusterID, false); err != nil {
		t.Fatal(err)
	}
	if _, err := ManageCluster(ctx, f.pool, ClusterRemove, f.clusterID, false); err != nil {
		t.Fatal(err)
	}
	if got := nextResult(t, second); connect.CodeOf(got.err) != connect.CodePermissionDenied {
		t.Fatalf("removed stream: %v", got.err)
	}
}

func waitForSyncGeneration(t *testing.T, f *deployFixture, id uuid.UUID, minimum int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		generation, err := f.queries.GetClusterSyncGeneration(ctx, id)
		if err == nil && generation >= minimum {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("sync generation never reached %d: %v", minimum, err)
		case <-ticker.C:
		}
	}
}
