package service

import (
	"context"
	"maps"
	"strconv"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	planv1 "github.com/team-loco/loco/gen/go/loco/plan/v1"
)

const (
	movedDigest        = "sha256:9999999999999999999999999999999999999999999999999999999999999999"
	applyFileImageOnly = `  worker:
    image: nginx:1.27
    port: 8080
    regions:
      us-east-1: { cpu: 100m, memory: 64Mi, replicas: { min: 1, max: 1 } }
`
	emptyServicesFile      = "version: 1\npartial: web\nservices: {}\n"
	guardWorker            = "worker"
	lockTimeout            = 2 * time.Second
	interleavingRuns       = 10
	lockPollInterval       = 10 * time.Millisecond
	writersPerInterleaving = 2
)

func (f *deployFixture) setPartial(t *testing.T, name, partial string) {
	t.Helper()
	update := `UPDATE resources SET partial = $2 WHERE name = $1`
	if _, err := f.pool.Exec(context.Background(), update, name, partial); err != nil {
		t.Fatalf("set partial of %s: %v", name, err)
	}
}

func (f *deployFixture) useLockTimeout(t *testing.T) {
	t.Helper()
	cfg := f.pool.Config()
	cfg.ConnConfig.RuntimeParams["lock_timeout"] = strconv.FormatInt(lockTimeout.Milliseconds(), 10)
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("pool with lock timeout: %v", err)
	}
	t.Cleanup(pool.Close)
	f.pool = pool
	f.queries = genDb.New(pool)
}

func TestApplyRefusesAnImageTagThatMovedSinceThePlan(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	file := planFileHeader + applyFileImageOnly
	resolver := &fakeResolver{digest: testDigest}
	server := NewPlanServer(f.pool, f.queries, resolver, testRegistryHost, testServiceDefaults())
	readCtx := context.WithValue(context.Background(), contextkeys.EntityScopesKey, f.workspaceReadScopes(t))
	planned, err := server.Plan(readCtx, connect.NewRequest(&planv1.PlanRequest{
		File:          []byte(file),
		EnvironmentId: f.envID.String(),
	}))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	reviewed := planned.Msg.GetImages()
	if len(reviewed) != 1 {
		t.Fatalf("plan images = %v, want the one reference", reviewed)
	}

	resolver.digest = movedDigest
	writeCtx := context.WithValue(context.Background(), contextkeys.EntityScopesKey, f.applyScopes(t))
	_, err = server.Apply(writeCtx, connect.NewRequest(&planv1.ApplyRequest{
		File:          []byte(file),
		EnvironmentId: f.envID.String(),
		Revision:      planned.Msg.GetRevision(),
		Images:        reviewed,
	}))
	refusal := wantRefusal(t, err)
	if maps.Equal(refusal.GetImages(), reviewed) || len(refusal.GetImages()) != 1 {
		t.Fatalf("refusal images = %v, want the moved digest", refusal.GetImages())
	}
	if f.hasResource(t, guardWorker) {
		t.Fatal("worker was created from a tag that moved since the plan")
	}
}

func TestApplyOfAnEmptyServicesMapDeletesOnlyThePartialsServices(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	f.addResource(t, planOld, planPartial)
	f.addResource(t, planTheirs, planOtherPartial)

	planned, err := plan(t, f, emptyServicesFile, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	ops := planned.GetOperations()
	if len(ops) != 1 || ops[0].GetService() != planOld ||
		ops[0].GetKind() != planv1.PlanOperationKind_PLAN_OPERATION_KIND_DELETE {
		t.Fatalf("operations = %v, want only the delete of %s", ops, planOld)
	}

	_, err = applyFile(t, f, emptyServicesFile, applyOptions{}, f.adminScopes(t))
	wantRefusal(t, err)
	if !f.hasResource(t, planOld) {
		t.Fatal("old was deleted without confirmation")
	}

	if _, err := applyFile(
		t,
		f,
		emptyServicesFile,
		applyOptions{confirmDestructive: true},
		f.adminScopes(t),
	); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if f.hasResource(t, planOld) {
		t.Fatal("old survived the confirmed apply")
	}
	if !f.hasResource(t, planTheirs) || !f.hasResource(t, planSvc) {
		t.Fatal("the apply deleted a service the partial does not own")
	}
}

func TestApplyCommitsNothingForInvalidValues(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	file := planFileHeader + `  worker:
    port: 8080
    regions:
      us-east-1: { cpu: invalid, memory: 64Mi, replicas: { min: 1, max: 20 } }
`
	_, err := applyFile(t, f, file, applyOptions{}, f.applyScopes(t))
	refusal := wantRefusal(t, err)
	if len(refusal.GetErrors()) != 1 || refusal.GetErrors()[0].GetPath() != "regions.us-east-1" {
		t.Fatalf("errors = %v, want the region's values refused", refusal.GetErrors())
	}
	if f.hasResource(t, guardWorker) {
		t.Fatal("worker was created with invalid values")
	}
	if rev := f.revision(t, f.envID); rev != 0 {
		t.Fatalf("revision = %d, want 0", rev)
	}
}

func TestApplyDeleteInOneEnvironmentKeepsTheOther(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	f.setPartial(t, planSvc, planPartial)
	stagingID := f.addTypedEnvironment(t, stagingName, stagingName)
	f.setClusterTier(t, f.otherCluster, stagingName)
	ctx := context.Background()
	if _, err := f.deploy(ctx, staticSpec); err != nil {
		t.Fatalf("deploy production: %v", err)
	}
	staging := f.paramsFor(f.otherCluster)
	staging.EnvironmentID = stagingID
	err := withTx(ctx, f.pool, func(qtx *genDb.Queries) error {
		_, deployErr := createDeploymentWithCleanup(ctx, qtx, staging, staticSpec)
		return deployErr
	})
	if err != nil {
		t.Fatalf("deploy staging: %v", err)
	}

	confirmed := applyOptions{confirmDestructive: true}
	if _, applyErr := applyFile(t, f, emptyServicesFile, confirmed, f.adminScopes(t)); applyErr != nil {
		t.Fatalf("Apply: %v", applyErr)
	}
	if !f.hasResource(t, planSvc) {
		t.Fatal("the production apply deleted the service staging still runs")
	}
	if got := len(f.activeDeployments(t, f.envID)); got != 0 {
		t.Fatalf("production active deployments = %d, want 0", got)
	}
	if got := len(f.activeDeployments(t, stagingID)); got != 1 {
		t.Fatalf("staging active deployments = %d, want 1", got)
	}
	again, err := plan(t, f, emptyServicesFile, f.workspaceReadScopes(t))
	if err != nil {
		t.Fatalf("Plan after apply: %v", err)
	}
	if len(again.GetOperations()) != 0 {
		t.Fatalf("operations after apply = %v, want none", again.GetOperations())
	}
}

// holdResourceLock locks the resource row in its own transaction until the returned function
// runs, so writers that need the row line up behind it in the order their lock protocol takes.
func (f *deployFixture) holdResourceLock(t *testing.T) func() {
	t.Helper()
	ctx := context.Background()
	tx, err := f.pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin gate: %v", err)
	}
	if _, err := tx.Exec(ctx, `SELECT id FROM resources WHERE id = $1 FOR UPDATE`, f.resourceID); err != nil {
		t.Fatalf("lock resource: %v", err)
	}
	return func() {
		if err := tx.Rollback(ctx); err != nil {
			t.Errorf("release gate: %v", err)
		}
	}
}

func (f *deployFixture) waitForLockWaiters(t *testing.T, want int) {
	t.Helper()
	query := `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`
	deadline := time.Now().Add(lockTimeout)
	for time.Now().Before(deadline) {
		var waiting int
		if err := f.pool.QueryRow(context.Background(), query).Scan(&waiting); err != nil {
			t.Fatalf("count lock waiters: %v", err)
		}
		if waiting >= want {
			return
		}
		time.Sleep(lockPollInterval)
	}
	t.Fatalf("fewer than %d transactions waited on a lock", want)
}

func TestApplyAndTransferPartialDoNotDeadlock(t *testing.T) {
	for range interleavingRuns {
		f := newDeployFixture(t)
		f.prepareApply(t)
		f.setPartial(t, planSvc, planPartial)
		f.useLockTimeout(t)
		release := f.holdResourceLock(t)

		var wg sync.WaitGroup
		var applyErr, transferErr error
		wg.Go(func() {
			_, applyErr = applyFile(t, f, emptyServicesFile, applyOptions{confirmDestructive: true}, f.adminScopes(t))
		})
		wg.Go(func() {
			transferErr = transferPartial(t, f, planOtherPartial)
		})
		f.waitForLockWaiters(t, writersPerInterleaving)
		release()
		wg.Wait()

		if applyErr != nil && connect.CodeOf(applyErr) != connect.CodeFailedPrecondition {
			t.Fatalf("Apply = %v, want success or a stale-revision refusal", applyErr)
		}
		if transferErr != nil && connect.CodeOf(transferErr) != connect.CodeNotFound {
			t.Fatalf("TransferPartial = %v, want success or not found after the delete", transferErr)
		}
	}
}
