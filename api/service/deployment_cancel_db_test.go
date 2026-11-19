package service

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/events"
	genDb "github.com/team-loco/loco/api/gen/db"
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
	f.prepareApply(t)
	f.addOtherRegionCluster(t)
	var other uuid.UUID
	row := f.pool.QueryRow(t.Context(), `SELECT id FROM clusters WHERE region = $1`, otherRegion)
	if err := row.Scan(&other); err != nil {
		t.Fatal(err)
	}
	first := applyOK(t, f, planFileHeader+applyFileTwoRegions, applyOptions{}).GetDeployments()[0]
	f.finishRollouts(t)
	second := applyOK(t, f, planFileHeader+twoRegionsBiggerCPU, applyOptions{}).GetDeployments()[0]
	worker, _ := f.resourceByName(t, applyWorker)
	running := f.placementOf(t, worker.ID, f.clusterID)
	f.reportPlacement(t, running, true)
	if err := f.cancelDeployment(t, second.GetDeploymentId()); err != nil {
		t.Fatal(err)
	}
	if p := f.placementOf(t, worker.ID, f.clusterID); p.DeploymentID.String() != second.GetDeploymentId() {
		t.Fatalf("running region deployment = %s, want %s", p.DeploymentID, second.GetDeploymentId())
	}
	if p := f.placementOf(t, worker.ID, other); p.DeploymentID.String() != first.GetDeploymentId() {
		t.Fatalf("reverted region deployment = %s, want %s", p.DeploymentID, first.GetDeploymentId())
	}
	regions := placementRegions(f.getDeployment(t, second.GetDeploymentId()))
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
