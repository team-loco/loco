package service

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

const (
	applyFileDeploy  = "    deploy: { attempts: 3, retryDelay: 60 }\n"
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

func storedDeployPolicy(res genDb.Resource) [2]int32 {
	var policy [2]int32
	if res.MaxApplyAttempts != nil {
		policy[0] = *res.MaxApplyAttempts
	}
	if res.ApplyRetryDelaySeconds != nil {
		policy[1] = *res.ApplyRetryDelaySeconds
	}
	return policy
}

func (f *deployFixture) workerPlacement(t *testing.T) genDb.Placement {
	t.Helper()
	key := genDb.GetPlacementForResourceClusterParams{ResourceID: f.workerResource(t).ID, ClusterID: f.clusterID}
	placement, err := f.queries.GetPlacementForResourceCluster(t.Context(), key)
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
		if err := session.handle(t.Context(), failure); err != nil {
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
	if err := session.handle(t.Context(), failure); err != nil {
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

func TestApplyStoresTheDeployPolicyAndApplyRetriesFollowIt(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	file := workerFileWith(applyFileDeploy)
	applyOK(t, f, file, applyOptions{})

	if got := storedDeployPolicy(f.workerResource(t)); got != [2]int32{3, 60} {
		t.Fatalf("stored attempts and retry delay = %v, want [3 60]", got)
	}
	wantCleanPlan(t, f, file)
	failApplyUntilFailed(t, f, 3, 60)
}

func TestApplyWithoutADeployPolicyUsesTheAPIDefaults(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	applyOK(t, f, planFileHeader+applyFileWorker, applyOptions{})

	if got := storedDeployPolicy(f.workerResource(t)); got != [2]int32{} {
		t.Fatalf("stored attempts and retry delay = %v, want none", got)
	}
	failApplyUntilFailed(t, f, testDeployAttempts, testDeployRetryDelay)
}

func TestChangingTheDeployPolicyDoesNotRedeploy(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	applyOK(t, f, workerFileWith(applyFileDeploy), applyOptions{})

	changed := workerFileWith("    deploy: { attempts: 5 }\n")
	resp := applyOK(t, f, changed, applyOptions{})
	if started := resp.GetDeployments(); len(started) != 0 {
		t.Fatalf("a deploy policy change started deployments %v", started)
	}
	if got := storedDeployPolicy(f.workerResource(t)); got != [2]int32{5, 0} {
		t.Fatalf("stored attempts and retry delay = %v, want [5 0]", got)
	}
	wantCleanPlan(t, f, changed)

	removed := planFileHeader + applyFileWorker
	applyOK(t, f, removed, applyOptions{})
	if got := storedDeployPolicy(f.workerResource(t)); got != [2]int32{} {
		t.Fatalf("stored attempts and retry delay = %v, want none", got)
	}
	wantCleanPlan(t, f, removed)
}
