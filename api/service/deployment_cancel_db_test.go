package service

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

const cancelUserName = "Cancel Tester"

// cancelContext is a user principal with the scope on the deployment's resource.
func (f *deployFixture) cancelContext(t *testing.T, deploymentID string, scope genDb.Scope) context.Context {
	t.Helper()
	resourceID, err := f.queries.GetDeploymentResourceID(t.Context(), uuid.MustParse(deploymentID))
	if err != nil {
		t.Fatal(err)
	}
	var userID uuid.UUID
	if err := f.pool.QueryRow(t.Context(), `UPDATE users SET name = $1 WHERE id = (SELECT id FROM users LIMIT 1)
RETURNING id`, cancelUserName).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	return reqctx.WithPrincipal(t.Context(), reqctx.Principal{
		ActorType: genDb.DeploymentActorTypeUser,
		ActorID:   userID,
		Method:    genDb.DeploymentMethodWeb,
		Entity:    genDb.Entity{Type: genDb.EntityTypeUser, ID: userID},
		Scopes:    []genDb.EntityScope{{EntityType: genDb.EntityTypeResource, EntityID: resourceID, Scope: scope}},
	})
}

func (f *deployFixture) cancelDeployment(t *testing.T, id string) error {
	t.Helper()
	_, err := f.deploymentServer().CancelDeployment(
		f.cancelContext(t, id, genDb.ScopeWrite),
		&deploymentv1.CancelDeploymentRequest{DeploymentId: id},
	)
	return err
}

func (f *deployFixture) placementOf(t *testing.T, resourceID, clusterID uuid.UUID) genDb.Placement {
	t.Helper()
	p, err := f.queries.GetResourceEnvironmentPlacement(t.Context(), genDb.GetResourceEnvironmentPlacementParams{
		ResourceID: resourceID, EnvironmentID: f.envID, ClusterID: clusterID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *deployFixture) reportPlacement(t *testing.T, p genDb.Placement, ready bool) {
	t.Helper()
	f.agentServer().recordStatus(t.Context(), p.ClusterID, 0, &agentv1.PlacementStatus{
		PlacementId:      p.ID.String(),
		ObservedRevision: p.DesiredRevision,
		Available:        ready,
		Healthy:          ready,
		ReadyReplicas:    1,
	})
}

func TestCancelRevertsARegionToThePreviousDeployment(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	first, err := createTestDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatal(err)
	}
	f.finishRollouts(t)
	second, err := deployService(t, f, imageService())
	if err != nil {
		t.Fatal(err)
	}
	before := f.placement(t, f.clusterID)
	if err := f.cancelDeployment(t, second.GetDeploymentId()); err != nil {
		t.Fatal(err)
	}
	after := f.placement(t, f.clusterID)
	if after.DeploymentID == nil || after.DeploymentID.String() != first.GetDeploymentId() ||
		after.DesiredRevision <= before.DesiredRevision {
		t.Fatalf("placement deployment=%v revision=%d, want %s above revision %d",
			after.DeploymentID, after.DesiredRevision, first.GetDeploymentId(), before.DesiredRevision)
	}
	if got := desiredPayload(t, f).AppSpec.DeploymentID; got != first.GetDeploymentId() {
		t.Fatalf("desired Application deployment = %s, want %s", got, first.GetDeploymentId())
	}
	canceled := f.getDeployment(t, second.GetDeploymentId())
	if canceled.GetStatus() != deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_CANCELED ||
		canceled.GetCanceledByName() != cancelUserName {
		t.Fatalf("canceled deployment status=%v by=%q", canceled.GetStatus(), canceled.GetCanceledByName())
	}
	if got := f.deploymentStatus(t, uuid.MustParse(first.GetDeploymentId())); got != genDb.DeploymentStatusRunning {
		t.Fatalf("previous deployment status = %s, want running", got)
	}
	if n := f.eventCount(t, events.DeploymentCanceled); n != 1 {
		t.Fatalf("deployment.canceled events = %d, want 1", n)
	}
}

func TestCancelOfAFirstDeploymentRemovesItsApplication(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	created, err := createTestDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.cancelDeployment(t, created.GetDeploymentId()); err != nil {
		t.Fatal(err)
	}
	if p := f.placement(t, f.clusterID); !p.DesiredDeleted {
		t.Fatal("canceling the only deployment left its Application in place")
	}
}

func TestCancelOfAFinishedDeploymentIsRefused(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	created, err := createTestDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatal(err)
	}
	f.finishRollouts(t)
	wantCode(t, f.cancelDeployment(t, created.GetDeploymentId()), connect.CodeFailedPrecondition)
	if got := f.deploymentStatus(t, uuid.MustParse(created.GetDeploymentId())); got != genDb.DeploymentStatusRunning {
		t.Fatalf("refused cancel left status %s", got)
	}
}

func TestCancelNeedsWriteAccess(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	created, err := createTestDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.deploymentServer().CancelDeployment(
		f.cancelContext(t, created.GetDeploymentId(), genDb.ScopeRead),
		&deploymentv1.CancelDeploymentRequest{DeploymentId: created.GetDeploymentId()},
	)
	wantCode(t, err, connect.CodePermissionDenied)
}

func TestCancelKeepsARegionThatAlreadyRuns(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	first, second, worker, other := twoRegionRollout(t, f)
	finished := f.cancelImpact(t, second)
	if len(finished) != 1 || finished[0].GetRegion() != testRegion || finished[0].GetPrevious().GetId() != first {
		t.Fatalf("finished regions = %v, want %s back to %s", finished, testRegion, first)
	}
	resp, err := f.deploymentServer().CancelDeployment(
		f.cancelContext(t, second, genDb.ScopeWrite),
		&deploymentv1.CancelDeploymentRequest{DeploymentId: second},
	)
	if err != nil {
		t.Fatal(err)
	}
	if ids := resp.GetRollbackDeploymentIds(); len(ids) != 0 {
		t.Fatalf("rollbacks = %v, want none", ids)
	}
	if p := f.placementOf(t, worker.ID, f.clusterID); p.DeploymentID.String() != second {
		t.Fatalf("running region deployment = %s, want %s", p.DeploymentID, second)
	}
	if p := f.placementOf(t, worker.ID, other); p.DeploymentID.String() != first {
		t.Fatalf("reverted region deployment = %s, want %s", p.DeploymentID, first)
	}
	regions := placementRegions(f.getDeployment(t, second))
	if regions[testRegion] != deploymentv1.PlacementStatus_PLACEMENT_STATUS_RUNNING ||
		regions[otherRegion] != deploymentv1.PlacementStatus_PLACEMENT_STATUS_CANCELED {
		t.Fatalf("placements after cancel = %v", regions)
	}
}

func TestCancelWhileBuildingCancelsTheBuild(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	buildID := f.queued(t)
	id := deployBuild(t, f, buildID)
	if err := f.cancelDeployment(t, id.String()); err != nil {
		t.Fatal(err)
	}
	if got := f.row(t, buildID).Status; got != genDb.BuildStatusCanceled {
		t.Fatalf("build status = %s, want canceled", got)
	}
	if got := f.deploymentStatus(t, id); got != genDb.DeploymentStatusCanceled {
		t.Fatalf("deployment status = %s, want canceled", got)
	}
}

func TestAnImageDeploySupersedesABuildingDeployment(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	buildID := f.queued(t)
	building := deployBuild(t, f, buildID)
	replacement, err := deployService(t, f.deployFixture, imageService())
	if err != nil {
		t.Fatal(err)
	}
	if got := f.row(t, buildID).Status; got != genDb.BuildStatusCanceled {
		t.Fatalf("build status = %s, want canceled", got)
	}
	var supersededBy string
	if err := f.pool.QueryRow(t.Context(), `SELECT data->>$3 FROM events WHERE type = $1 AND subject_id = $2`,
		events.DeploymentCanceled, building, events.FieldSupersededBy).Scan(&supersededBy); err != nil {
		t.Fatal(err)
	}
	if supersededBy != replacement.GetDeploymentId() {
		t.Fatalf("supersededBy = %s, want %s", supersededBy, replacement.GetDeploymentId())
	}
}

func TestCancelLeavesARestartedRegionRunning(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	first, err := createTestDeployment(t, f, sameRegionSpec)
	if err != nil {
		t.Fatal(err)
	}
	f.finishRollouts(t)
	if err = restartResource(t, f, f.envID, "", genDb.ScopeWrite); err != nil {
		t.Fatal(err)
	}
	wantCode(t, f.cancelDeployment(t, first.GetDeploymentId()), connect.CodeFailedPrecondition)
	second, err := deployService(t, f, imageService())
	if err != nil {
		t.Fatal(err)
	}
	if err = f.cancelDeployment(t, second.GetDeploymentId()); err != nil {
		t.Fatal(err)
	}
	p := f.placement(t, f.clusterID)
	if p.DesiredDeleted || p.DeploymentID == nil || p.DeploymentID.String() != first.GetDeploymentId() {
		t.Fatalf("placement deployment=%v deleted=%t, want %s",
			p.DeploymentID, p.DesiredDeleted, first.GetDeploymentId())
	}
}

func TestCancelKeepsABuildAnotherDeploymentWaitsFor(t *testing.T) {
	t.Parallel()
	f := newBuildFixture(t)
	buildID := f.queued(t)
	first := deployBuild(t, f, buildID)
	second, err := deployService(t, f.deployFixture, &deploymentv1.ServiceDeploymentSpec{
		Build: &deploymentv1.BuildSource{Type: buildSourceTypeDockerfile, BuildId: &buildID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := f.deploymentStatus(t, first); got != genDb.DeploymentStatusCanceled {
		t.Fatalf("superseded deployment status = %s, want canceled", got)
	}
	if got := f.row(t, buildID).Status; got != genDb.BuildStatusQueued {
		t.Fatalf("build status after supersede = %s, want queued", got)
	}
	if err := f.cancelDeployment(t, second.GetDeploymentId()); err != nil {
		t.Fatal(err)
	}
	if got := f.row(t, buildID).Status; got != genDb.BuildStatusCanceled {
		t.Fatalf("build status after cancel = %s, want canceled", got)
	}
}

// twoRegionRollout applies a first deployment to both regions, finishes it, then applies a
// second that already runs in testRegion and still deploys in otherRegion.
func twoRegionRollout(t *testing.T, f *deployFixture) (first, second string, worker genDb.Resource, other uuid.UUID) {
	t.Helper()
	f.prepareApply(t)
	f.addOtherRegionCluster(t)
	row := f.pool.QueryRow(t.Context(), `SELECT id FROM clusters WHERE region = $1`, otherRegion)
	if err := row.Scan(&other); err != nil {
		t.Fatal(err)
	}
	first = applyOK(t, f, planFileHeader+applyFileTwoRegions, applyOptions{}).GetDeployments()[0].GetDeploymentId()
	f.finishRollouts(t)
	second = applyOK(t, f, planFileHeader+twoRegionsBiggerCPU, applyOptions{}).GetDeployments()[0].GetDeploymentId()
	worker, _ = f.resourceByName(t, applyWorker)
	f.reportPlacement(t, f.placementOf(t, worker.ID, f.clusterID), true)
	return first, second, worker, other
}

func (f *deployFixture) cancelWithRollback(t *testing.T, id string) []string {
	t.Helper()
	resp, err := f.deploymentServer().CancelDeployment(
		f.cancelContext(t, id, genDb.ScopeWrite),
		&deploymentv1.CancelDeploymentRequest{DeploymentId: id, RollbackFinishedRegions: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	return resp.GetRollbackDeploymentIds()
}

func (f *deployFixture) cancelImpact(t *testing.T, id string) []*deploymentv1.FinishedRegion {
	t.Helper()
	resp, err := f.deploymentServer().GetDeploymentCancelImpact(
		f.cancelContext(t, id, genDb.ScopeRead),
		&deploymentv1.GetDeploymentCancelImpactRequest{DeploymentId: id},
	)
	if err != nil {
		t.Fatal(err)
	}
	return resp.GetFinishedRegions()
}

func TestCancelWithRollbackAndNoFinishedRegionQueuesNothing(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	if _, err := createTestDeployment(t, f, sameRegionSpec); err != nil {
		t.Fatal(err)
	}
	f.finishRollouts(t)
	second, err := deployService(t, f, imageService())
	if err != nil {
		t.Fatal(err)
	}
	if finished := f.cancelImpact(t, second.GetDeploymentId()); len(finished) != 0 {
		t.Fatalf("finished regions = %v, want none", finished)
	}
	if rollbacks := f.cancelWithRollback(t, second.GetDeploymentId()); len(rollbacks) != 0 {
		t.Fatalf("rollbacks = %v, want none", rollbacks)
	}
	if n := f.eventCount(t, events.DeploymentCreated); n != 2 {
		t.Fatalf("deployment.created events = %d, want the 2 deploys", n)
	}
}

func TestCancelWithRollbackRestoresTheFinishedRegion(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	first, second, worker, other := twoRegionRollout(t, f)
	rollbacks := f.cancelWithRollback(t, second)
	if len(rollbacks) != 1 {
		t.Fatalf("rollbacks = %v, want one", rollbacks)
	}
	rollback := f.getDeployment(t, rollbacks[0])
	if rollback.GetTrigger() != deploymentv1.DeploymentTrigger_DEPLOYMENT_TRIGGER_ROLLBACK ||
		rollback.GetSourceDeploymentId() != first || rollback.GetRollbackOfDeploymentId() != second {
		t.Fatalf(
			"rollback trigger=%v source=%s of=%s, want rollback of %s to %s",
			rollback.GetTrigger(),
			rollback.GetSourceDeploymentId(),
			rollback.GetRollbackOfDeploymentId(),
			second,
			first,
		)
	}
	if regions := placementRegions(rollback); len(regions) != 1 || regions[testRegion] == 0 {
		t.Fatalf("rollback regions = %v, want only %s", regions, testRegion)
	}
	if p := f.placementOf(t, worker.ID, f.clusterID); p.DeploymentID.String() != rollbacks[0] {
		t.Fatalf("finished region deployment = %s, want the rollback %s", p.DeploymentID, rollbacks[0])
	}
	if p := f.placementOf(t, worker.ID, other); p.DeploymentID.String() != first {
		t.Fatalf("reverted region deployment = %s, want %s", p.DeploymentID, first)
	}
	var recorded []string
	if err := f.pool.QueryRow(t.Context(),
		`SELECT ARRAY(SELECT jsonb_array_elements_text(data->$3)) FROM events WHERE type = $1 AND subject_id = $2`,
		events.DeploymentCanceled, second, events.FieldRollbacks).Scan(&recorded); err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 || recorded[0] != rollbacks[0] {
		t.Fatalf("deployment.canceled rollbacks = %v, want %v", recorded, rollbacks)
	}
	if err := f.cancelDeployment(t, rollbacks[0]); err != nil {
		t.Fatalf("cancel the rollback: %v", err)
	}
	if p := f.placementOf(t, worker.ID, f.clusterID); p.DeploymentID.String() != second {
		t.Fatalf("region after canceling the rollback runs %s, want %s", p.DeploymentID, second)
	}
}

func TestCancelWithRollbackSkipsARegionWithoutAnEarlierDeployment(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	f.addOtherRegionCluster(t)
	first := applyOK(t, f, planFileHeader+applyFileTwoRegions, applyOptions{}).GetDeployments()[0].GetDeploymentId()
	worker, _ := f.resourceByName(t, applyWorker)
	f.reportPlacement(t, f.placementOf(t, worker.ID, f.clusterID), true)
	finished := f.cancelImpact(t, first)
	if len(finished) != 1 || finished[0].GetRegion() != testRegion || finished[0].GetPrevious() != nil {
		t.Fatalf("finished regions = %v, want %s with no previous deployment", finished, testRegion)
	}
	if rollbacks := f.cancelWithRollback(t, first); len(rollbacks) != 0 {
		t.Fatalf("rollbacks = %v, want none", rollbacks)
	}
	if p := f.placementOf(t, worker.ID, f.clusterID); p.DeploymentID.String() != first {
		t.Fatalf("finished region deployment = %s, want %s", p.DeploymentID, first)
	}
}

func TestCancelImpactSkipsAnEarlierDeploymentThatFailed(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	first, second, _, _ := twoRegionRollout(t, f)
	failed := uuid.New()
	if _, err := f.pool.Exec(t.Context(), `
INSERT INTO deployments (id, resource_id, status, environment_id, spec, spec_version, trigger, actor_type, actor_id,
  method, created_at)
SELECT $1, resource_id, 'failed', environment_id, spec, spec_version, trigger, actor_type, actor_id, method,
  created_at + INTERVAL '1 microsecond'
FROM deployments WHERE id = $2`, failed, uuid.MustParse(first)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(t.Context(), `
INSERT INTO deployment_placements (deployment_id, cluster_id, region, replicas, status, image, started_at)
SELECT $1, cluster_id, region, replicas, 'failed', image, NOW()
FROM deployment_placements WHERE deployment_id = $2 AND cluster_id = $3`,
		failed, uuid.MustParse(first), f.clusterID); err != nil {
		t.Fatal(err)
	}
	finished := f.cancelImpact(t, second)
	if len(finished) != 1 || finished[0].GetPrevious().GetId() != first {
		t.Fatalf("finished regions = %v, want %s back to %s, not the failed %s", finished, testRegion, first, failed)
	}
}

func TestCancelWithRollbackPrunesHistory(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	first, second, _, _ := twoRegionRollout(t, f)
	f.setLimit(t, limits.DeploymentHistory, 2)
	old := uuid.New()
	if _, err := f.pool.Exec(t.Context(), `
INSERT INTO deployments (id, resource_id, status, environment_id, spec, spec_version, trigger, actor_type, actor_id,
  method, created_at)
SELECT $1, resource_id, 'superseded', environment_id, spec, spec_version, trigger, actor_type, actor_id, method,
  created_at - INTERVAL '5 days'
FROM deployments WHERE id = $2`, old, uuid.MustParse(first)); err != nil {
		t.Fatal(err)
	}
	if rollbacks := f.cancelWithRollback(t, second); len(rollbacks) != 1 {
		t.Fatalf("rollbacks = %v, want one", rollbacks)
	}
	if f.deploymentExists(t, old) {
		t.Fatal("the rollback left a finished deployment past the history limit")
	}
}
