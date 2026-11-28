package service

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/pkg/servicedefaults"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	locoControllerV1 "github.com/team-loco/loco/k8sapi/v1alpha1"
)

const (
	applyFileDeploy  = "    deploy: { restart: on-failure, maxRestarts: 3, timeout: 120 }\n"
	testApplyFailure = "invalid application"
)

func workerFileWith(deploy string) string {
	return planFileHeader + strings.Replace(applyFileWorker, "    regions:", deploy+"    regions:", 1)
}

func (f *deployFixture) workerResource(t *testing.T) genDb.Resource {
	t.Helper()
	res, found := f.resourceByName(t, applyWorker)
	if !found {
		t.Fatal("worker does not exist")
	}
	return res
}

// storedPolicy is the failure policy the environment configuration holds, with "" and 0 for what it leaves unset.
type storedPolicy struct {
	restart     string
	maxRestarts int32
	timeout     int32
}

func storedDeployPolicy(res genDb.ResourceEnvironment) storedPolicy {
	var policy storedPolicy
	if res.RestartPolicy != nil {
		policy.restart = *res.RestartPolicy
	}
	if res.MaxRestarts != nil {
		policy.maxRestarts = *res.MaxRestarts
	}
	if res.DeployTimeoutSeconds != nil {
		policy.timeout = *res.DeployTimeoutSeconds
	}
	return policy
}

func (f *deployFixture) deployedPolicy(t *testing.T) servicedefaults.FailurePolicy {
	t.Helper()
	spec := f.workerPayload(t, f.clusterID).AppSpec.ServiceSpec
	return servicedefaults.FailurePolicy{Restart: spec.RestartPolicy, DeployTimeoutSeconds: spec.DeployTimeoutSeconds}
}

func (f *deployFixture) workerPlacement(t *testing.T) genDb.Placement {
	t.Helper()
	key := genDb.GetResourceEnvironmentPlacementParams{
		EnvironmentID: f.envID,
		ResourceID:    f.workerResource(t).ID,
		ClusterID:     f.clusterID,
	}
	placement, err := f.queries.GetResourceEnvironmentPlacement(t.Context(), key)
	if err != nil {
		t.Fatalf("get placement: %v", err)
	}
	return placement
}

func (f *deployFixture) expirePlacementRetry(t *testing.T, placementID uuid.UUID) {
	t.Helper()
	if _, err := f.pool.Exec(
		t.Context(),
		`UPDATE placements SET retry_after = NOW() - INTERVAL '1 second' WHERE id = $1`,
		placementID,
	); err != nil {
		t.Fatal(err)
	}
}

func wantResends(t *testing.T, session *syncSession, sent *int, want int) {
	t.Helper()
	if err := session.sendPendingPlacements(t.Context()); err != nil {
		t.Fatal(err)
	}
	if *sent != want {
		t.Fatalf("session sent %d placements, want %d", *sent, want)
	}
}

func failApplyUntilFailed(t *testing.T, f *deployFixture, attempts, delay int32) {
	t.Helper()
	placement := f.workerPlacement(t)
	sent := 0
	session := newSyncSession(f.agentServer(), f.clusterID, func(_ *agentv1.SyncResponse) error { sent++; return nil })
	wantResends(t, session, &sent, 1)
	failure := &agentv1.SyncRequest{Message: &agentv1.SyncRequest_Applied{Applied: &agentv1.Applied{
		PlacementId: placement.ID.String(),
		Revision:    placement.DesiredRevision,
		Error:       testApplyFailure,
	}}}
	delayDuration := time.Duration(delay) * time.Second
	for attempt := int32(1); attempt < attempts; attempt++ {
		if err := session.handleNow(t.Context(), failure); err != nil {
			t.Fatal(err)
		}
		got := f.workerPlacement(t)
		if got.ApplyAttempts != attempt || got.ApplyFailed {
			t.Fatalf("failure %d left attempts %d, failed %t", attempt, got.ApplyAttempts, got.ApplyFailed)
		}
		wait, scheduled := session.nextRetry(t.Context())
		if !session.retryRecorded || !scheduled || wait <= delayDuration-time.Second || wait > delayDuration {
			t.Fatalf("failure %d scheduled a resend in %v (%t), want %v", attempt, wait, scheduled, delayDuration)
		}
		wantResends(t, session, &sent, int(attempt))
		f.expirePlacementRetry(t, placement.ID)
		wantResends(t, session, &sent, int(attempt)+1)
	}
	if err := session.handleNow(t.Context(), failure); err != nil {
		t.Fatal(err)
	}
	if got := f.workerPlacement(t); got.ApplyAttempts != attempts || !got.ApplyFailed || got.RetryAfter != nil {
		t.Fatalf("failure %d left attempts %d, failed %t, retry after %v",
			attempts, got.ApplyAttempts, got.ApplyFailed, got.RetryAfter)
	}
	if status := f.deploymentStatus(t, *placement.DeploymentID); status != genDb.DeploymentStatusFailed {
		t.Fatalf("deployment status = %s, want failed", status)
	}
	if wait, scheduled := session.nextRetry(t.Context()); scheduled {
		t.Fatalf("a failed placement scheduled a resend in %v", wait)
	}
	wantResends(t, session, &sent, int(attempts))
}

func TestApplyStoresTheFailurePolicyAndDeploysWithIt(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	file := workerFileWith(applyFileDeploy)
	applyOK(t, f, file, applyOptions{})

	if got, want := storedDeployPolicy(
		testResourceConfiguration(t, f.queries, f.workerResource(t).ID, f.envID),
	), (storedPolicy{"on-failure", 3, 120}); got != want {
		t.Fatalf("stored policy = %+v, want %+v", got, want)
	}
	want := servicedefaults.FailurePolicy{
		Restart: locoControllerV1.RestartPolicySpec{
			Mode:        locoControllerV1.RestartOnFailure,
			MaxRestarts: 3,
		},
		DeployTimeoutSeconds: 120,
	}
	if got := f.deployedPolicy(t); got != want {
		t.Fatalf("deployed policy = %+v, want %+v", got, want)
	}
	wantCleanPlan(t, f, file)
}

func TestApplyDeploysARestartModeWithoutABudget(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	file := workerFileWith("    deploy: { restart: never }\n")
	applyOK(t, f, file, applyOptions{})

	want := servicedefaults.FailurePolicy{
		Restart:              locoControllerV1.RestartPolicySpec{Mode: locoControllerV1.RestartNever},
		DeployTimeoutSeconds: testServiceDefaults().DeployTimeout,
	}
	if got := f.deployedPolicy(t); got != want {
		t.Fatalf("deployed policy = %+v, want %+v", got, want)
	}
	wantCleanPlan(t, f, file)
}

func TestApplyWithoutARestartPolicyDeploysTheAPIDefaults(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	applyOK(t, f, planFileHeader+applyFileWorker, applyOptions{})

	if got := storedDeployPolicy(
		testResourceConfiguration(t, f.queries, f.workerResource(t).ID, f.envID),
	); got != (storedPolicy{}) {
		t.Fatalf("stored policy = %+v, want none", got)
	}
	if got, want := f.deployedPolicy(t), testServiceDefaults().FailurePolicy(nil, nil, nil); got != want {
		t.Fatalf("deployed policy = %+v, want %+v", got, want)
	}
}

func TestApplyRetriesFollowTheAPIConfig(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	applyOK(t, f, workerFileWith(applyFileDeploy), applyOptions{})
	failApplyUntilFailed(t, f, testApplyAttempts, testApplyRetryDelay)
}

func TestChangingTheFailurePolicyDoesNotRedeploy(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	applyOK(t, f, workerFileWith(applyFileDeploy), applyOptions{})

	changed := workerFileWith("    deploy: { maxRestarts: 5 }\n")
	resp := applyOK(t, f, changed, applyOptions{})
	if started := resp.GetDeployments(); len(started) != 0 {
		t.Fatalf("a failure policy change started deployments %v", started)
	}
	if got := storedDeployPolicy(
		testResourceConfiguration(t, f.queries, f.workerResource(t).ID, f.envID),
	); got != (storedPolicy{maxRestarts: 5}) {
		t.Fatalf("stored policy = %+v, want only 5 restarts", got)
	}
	wantCleanPlan(t, f, changed)

	removed := planFileHeader + applyFileWorker
	applyOK(t, f, removed, applyOptions{})
	if got := storedDeployPolicy(
		testResourceConfiguration(t, f.queries, f.workerResource(t).ID, f.envID),
	); got != (storedPolicy{}) {
		t.Fatalf("stored policy = %+v, want none", got)
	}
	wantCleanPlan(t, f, removed)
}

func TestAFailurePolicyChangeReachesTheDeploymentTheSameApplyStarts(t *testing.T) {
	t.Parallel()
	f := newDeployFixture(t)
	f.prepareApply(t)
	applyOK(t, f, workerFileWith(applyFileDeploy), applyOptions{})
	f.finishRollouts(t)

	changed := workerFileWith("    deploy: { maxRestarts: 1 }\n")
	changed = strings.Replace(changed, "LOG_LEVEL: info", "LOG_LEVEL: debug", 1)
	if started := applyOK(t, f, changed, applyOptions{}).GetDeployments(); len(started) != 1 {
		t.Fatalf("an env change started deployments %v, want one", started)
	}
	want := locoControllerV1.RestartPolicySpec{Mode: locoControllerV1.RestartOnFailure, MaxRestarts: 1}
	if got := f.deployedPolicy(t).Restart; got != want {
		t.Fatalf("deployed restart policy = %+v, want %+v", got, want)
	}
}
