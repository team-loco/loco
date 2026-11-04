package service

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

const (
	apiDeployAttempts   = 2
	apiDeployRetryDelay = 300
	applyFileDeploy     = "    deploy: { attempts: 3, retryDelay: 60 }\n"
	testApplyFailure    = "invalid application"
)

func workerFileWith(deploy string) string {
	return planFileHeader + strings.Replace(applyFileWorker, "    regions:", deploy+"    regions:", 1)
}

func defaultPolicyAgentServer(f *deployFixture) *AgentServer {
	config := testAgentConfig()
	config.DeployAttempts = apiDeployAttempts
	config.DeployRetryDelay = apiDeployRetryDelay
	return NewAgentServer(f.pool, f.queries, nil, nil, nil, testHeartbeatInterval, config)
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

func (f *deployFixture) retryDelaySeconds(t *testing.T, placementID uuid.UUID) int32 {
	t.Helper()
	var waited int32
	if err := f.pool.QueryRow(
		t.Context(),
		`SELECT EXTRACT(EPOCH FROM retry_after - updated_at)::int FROM placements WHERE id = $1`,
		placementID,
	).Scan(&waited); err != nil {
		t.Fatal(err)
	}
	return waited
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

func failApplyUntilFailed(t *testing.T, f *deployFixture, server *AgentServer, attempts, delay int32) {
	t.Helper()
	placement := f.workerPlacement(t)
	failed := &agentv1.Applied{
		PlacementId: placement.ID.String(),
		Revision:    placement.DesiredRevision,
		Error:       testApplyFailure,
	}
	for attempt := int32(1); attempt < attempts; attempt++ {
		server.recordApplied(t.Context(), f.clusterID, failed)
		got := f.workerPlacement(t)
		if got.ApplyAttempts != attempt || got.ApplyFailed {
			t.Fatalf("failure %d left attempts %d, failed %t", attempt, got.ApplyAttempts, got.ApplyFailed)
		}
		if waited := f.retryDelaySeconds(t, placement.ID); waited != delay {
			t.Fatalf("failure %d deferred the placement %ds, want %ds", attempt, waited, delay)
		}
		f.expirePlacementRetry(t, placement.ID)
	}
	server.recordApplied(t.Context(), f.clusterID, failed)
	if got := f.workerPlacement(t); got.ApplyAttempts != attempts || !got.ApplyFailed || got.RetryAfter != nil {
		t.Fatalf("failure %d left attempts %d, failed %t, retry after %v",
			attempts, got.ApplyAttempts, got.ApplyFailed, got.RetryAfter)
	}
	if status := f.deploymentStatus(t, *placement.DeploymentID); status != genDb.DeploymentStatusFailed {
		t.Fatalf("deployment status = %s, want failed", status)
	}
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
	failApplyUntilFailed(t, f, defaultPolicyAgentServer(f), 3, 60)
}

func TestApplyWithoutADeployPolicyUsesTheAPIDefaults(t *testing.T) {
	f := newDeployFixture(t)
	f.prepareApply(t)
	applyOK(t, f, planFileHeader+applyFileWorker, applyOptions{})

	if got := storedDeployPolicy(f.workerResource(t)); got != [2]int32{} {
		t.Fatalf("stored attempts and retry delay = %v, want none", got)
	}
	failApplyUntilFailed(t, f, defaultPolicyAgentServer(f), apiDeployAttempts, apiDeployRetryDelay)
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
