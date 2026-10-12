package service

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/team-loco/loco/api/contextkeys"
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const (
	statusSecondRegion  = "eu-west-1"
	statusPhaseReady    = "Ready"
	statusPhaseRollout  = "Deploying"
	statusWaitingMsg    = "Waiting for pods to be ready..."
	statusCrashMsg      = "container app is crash looping: exit code 1"
	statusTwoReplicas   = 2
	statusProduction    = "prod"
	statusClusterSilent = 5 * time.Minute
)

type resourceStatusFixture struct {
	*deployFixture
	ctx            context.Context
	workspaceID    uuid.UUID
	regionCluster  uuid.UUID
	stagingCluster uuid.UUID
	stagingID      uuid.UUID
}

func newResourceStatusFixture(t *testing.T) *resourceStatusFixture {
	t.Helper()
	f := &resourceStatusFixture{deployFixture: newDeployFixture(t)}
	ctx := context.Background()
	f.workspaceID = f.deployFixture.workspaceID(t)
	f.stagingID = f.addStagingEnvironment(t)
	f.stagingCluster = f.otherCluster
	f.setStagingTier(t, f.stagingCluster)
	row := f.pool.QueryRow(ctx, `
WITH c AS (
    INSERT INTO clusters (name, region, provider, is_active, is_default)
    VALUES ('c3', $1, 'kind', true, true) RETURNING id
)
SELECT c.id FROM c`, statusSecondRegion)
	if err := row.Scan(&f.regionCluster); err != nil {
		t.Fatalf("seed second region: %v", err)
	}
	for _, clusterID := range []uuid.UUID{f.clusterID, f.stagingCluster, f.regionCluster} {
		f.heartbeat(t, clusterID, time.Now(), clusterHealthHealthy)
	}
	scopes := []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspaceID, Scope: genDb.ScopeRead},
	}
	f.ctx = context.WithValue(ctx, contextkeys.EntityScopesKey, scopes)
	return f
}

func (f *resourceStatusFixture) heartbeat(t *testing.T, clusterID uuid.UUID, at time.Time, health string) {
	t.Helper()
	update := `UPDATE clusters SET last_heartbeat = $2, health_status = $3 WHERE id = $1`
	if _, err := f.pool.Exec(context.Background(), update, clusterID, at, health); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
}

func (f *resourceStatusFixture) deployTo(
	t *testing.T,
	clusterID, environmentID uuid.UUID,
	region string,
	replicas int32,
) {
	t.Helper()
	params := f.paramsFor(clusterID)
	params.EnvironmentID = environmentID
	params.Region = region
	params.Replicas = replicas
	err := withTx(f.ctx, f.pool, func(qtx *genDb.Queries) error {
		_, deployErr := createDeploymentWithCleanup(f.ctx, qtx, params, staticSpec)
		return deployErr
	})
	if err != nil {
		t.Fatalf("deploy to %s: %v", region, err)
	}
}

func (f *resourceStatusFixture) deployProduction(t *testing.T, replicas int32) {
	t.Helper()
	f.deployTo(t, f.clusterID, f.envID, testRegion, replicas)
}

func (f *resourceStatusFixture) report(t *testing.T, clusterID uuid.UUID, status *agentv1.PlacementStatus) {
	t.Helper()
	p := f.placement(t, clusterID)
	status.PlacementId = p.ID.String()
	status.ObservedRevision = p.DesiredRevision
	f.agentServer().recordStatus(context.Background(), clusterID, status)
}

func (f *resourceStatusFixture) ready(t *testing.T, clusterID uuid.UUID, replicas int32) {
	t.Helper()
	f.report(t, clusterID, &agentv1.PlacementStatus{
		Ready:         true,
		ReadyReplicas: replicas,
		Phase:         statusPhaseReady,
	})
}

func (f *resourceStatusFixture) notReady(t *testing.T, clusterID uuid.UUID, replicas int32) {
	t.Helper()
	f.report(t, clusterID, &agentv1.PlacementStatus{
		ReadyReplicas: replicas,
		Phase:         statusPhaseRollout,
		Message:       statusWaitingMsg,
	})
}

func (f *resourceStatusFixture) failed(t *testing.T, clusterID uuid.UUID, replicas int32) {
	t.Helper()
	f.report(t, clusterID, &agentv1.PlacementStatus{
		ReadyReplicas: replicas,
		Phase:         applicationPhaseFailed,
		Message:       statusCrashMsg,
	})
}

func (f *resourceStatusFixture) server() *ResourceServer {
	return NewResourceServer(f.pool, f.queries, testServiceDefaults(), testStatusStaleAfter)
}

func (f *resourceStatusFixture) statuses(t *testing.T) []*resourcev1.EnvironmentStatus {
	t.Helper()
	server := f.server()
	resourceID := f.resourceID.String()

	got, err := server.GetResource(f.ctx, connect.NewRequest(&resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_ResourceId{ResourceId: resourceID},
	}))
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	fromGet := got.Msg.GetResource().GetEnvironmentStatuses()

	statusResp, err := server.GetResourceStatus(f.ctx, connect.NewRequest(&resourcev1.GetResourceStatusRequest{
		ResourceId: resourceID,
	}))
	if err != nil {
		t.Fatalf("get resource status: %v", err)
	}
	fromStatus := statusResp.Msg.GetResource().GetEnvironmentStatuses()

	listed, err := server.ListWorkspaceResources(f.ctx, connect.NewRequest(&resourcev1.ListWorkspaceResourcesRequest{
		WorkspaceId: f.workspaceID.String(),
	}))
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	resources := listed.Msg.GetResources()
	if len(resources) != 1 {
		t.Fatalf("listed %d resources, want 1", len(resources))
	}
	fromList := resources[0].GetEnvironmentStatuses()

	if !statusesEqual(fromGet, fromStatus) || !statusesEqual(fromGet, fromList) {
		t.Fatalf("read RPCs disagree:\nGetResource %v\nGetResourceStatus %v\nListWorkspaceResources %v",
			fromGet, fromStatus, fromList)
	}
	return fromGet
}

func statusesEqual(a, b []*resourcev1.EnvironmentStatus) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].GetEnvironmentId() != b[i].GetEnvironmentId() || a[i].GetStatus() != b[i].GetStatus() ||
			len(a[i].GetRegions()) != len(b[i].GetRegions()) {
			return false
		}
		for j, region := range a[i].GetRegions() {
			other := b[i].GetRegions()[j]
			if region.GetRegion() != other.GetRegion() || region.GetStatus() != other.GetStatus() ||
				region.GetReadyReplicas() != other.GetReadyReplicas() || region.GetMessage() != other.GetMessage() {
				return false
			}
		}
	}
	return true
}

func (f *resourceStatusFixture) environment(t *testing.T, environmentID uuid.UUID) *resourcev1.EnvironmentStatus {
	t.Helper()
	for _, status := range f.statuses(t) {
		if status.GetEnvironmentId() == environmentID.String() {
			return status
		}
	}
	t.Fatalf("no status for environment %s", environmentID)
	return nil
}

type wantRegion struct {
	region     string
	status     resourcev1.ResourceStatus
	ready      int32
	desired    int32
	deployment deploymentv1.DeploymentPhase
	message    string
}

func (f *resourceStatusFixture) assertProduction(
	t *testing.T,
	want resourcev1.ResourceStatus,
	regions ...wantRegion,
) {
	t.Helper()
	f.assertEnvironment(t, f.envID, want, regions...)
}

func (f *resourceStatusFixture) assertEnvironment(
	t *testing.T,
	environmentID uuid.UUID,
	want resourcev1.ResourceStatus,
	regions ...wantRegion,
) {
	t.Helper()
	got := f.environment(t, environmentID)
	if got.GetStatus() != want {
		t.Errorf("environment %s status = %s, want %s", got.GetEnvironmentName(), got.GetStatus(), want)
	}
	if len(got.GetRegions()) != len(regions) {
		t.Fatalf("environment %s has %d regions, want %d",
			got.GetEnvironmentName(), len(got.GetRegions()), len(regions))
	}
	for i, wantRegion := range regions {
		region := got.GetRegions()[i]
		if region.GetRegion() != wantRegion.region {
			t.Errorf("region %d = %s, want %s", i, region.GetRegion(), wantRegion.region)
		}
		if region.GetStatus() != wantRegion.status {
			t.Errorf("region %s status = %s, want %s", wantRegion.region, region.GetStatus(), wantRegion.status)
		}
		if region.GetReadyReplicas() != wantRegion.ready || region.GetDesiredReplicas() != wantRegion.desired {
			t.Errorf("region %s replicas = %d/%d, want %d/%d", wantRegion.region,
				region.GetReadyReplicas(), region.GetDesiredReplicas(), wantRegion.ready, wantRegion.desired)
		}
		if region.GetDeploymentStatus() != wantRegion.deployment {
			t.Errorf("region %s deployment = %s, want %s",
				wantRegion.region, region.GetDeploymentStatus(), wantRegion.deployment)
		}
		if region.GetMessage() != wantRegion.message {
			t.Errorf("region %s message = %q, want %q", wantRegion.region, region.GetMessage(), wantRegion.message)
		}
	}
}

func TestResourceStatusIsEmptyBeforeAnyDeployment(t *testing.T) {
	f := newResourceStatusFixture(t)
	if statuses := f.statuses(t); len(statuses) != 0 {
		t.Fatalf("statuses = %v, want none", statuses)
	}
}

func TestResourceStatusFollowsTheReplicasTheAgentReports(t *testing.T) {
	f := newResourceStatusFixture(t)
	f.deployProduction(t, statusTwoReplicas)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING,
		ready: 0, desired: 2, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_PENDING,
	})

	f.notReady(t, f.clusterID, 1)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED,
		ready: 1, desired: 2, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_DEPLOYING,
		message: statusWaitingMsg,
	})

	f.ready(t, f.clusterID, statusTwoReplicas)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		ready: 2, desired: 2, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING,
	})

	f.notReady(t, f.clusterID, 1)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED,
		ready: 1, desired: 2, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING,
		message: statusWaitingMsg,
	})

	f.notReady(t, f.clusterID, 0)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE,
		ready: 0, desired: 2, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING,
		message: statusWaitingMsg,
	})

	f.ready(t, f.clusterID, statusTwoReplicas)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		ready: 2, desired: 2, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING,
	})
}

func TestResourceStatusIsUnknownWhileTheClusterIsSilent(t *testing.T) {
	f := newResourceStatusFixture(t)
	f.deployProduction(t, 1)
	f.ready(t, f.clusterID, 1)
	healthy := wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		ready: 1, desired: 1, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING,
	}
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY, healthy)

	f.heartbeat(t, f.clusterID, time.Now().Add(-statusClusterSilent), clusterHealthHealthy)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN,
		ready: 1, desired: 1, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING,
		message: regionMessageClusterSilent,
	})

	f.heartbeat(t, f.clusterID, time.Now(), clusterHealthDegraded)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN,
		ready: 1, desired: 1, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING,
		message: regionMessageClusterUnhealthy,
	})

	f.heartbeat(t, f.clusterID, time.Now(), clusterHealthHealthy)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY, healthy)
}

func TestResourceStatusIsPerEnvironment(t *testing.T) {
	f := newResourceStatusFixture(t)
	f.deployProduction(t, 1)
	f.ready(t, f.clusterID, 1)
	f.deployTo(t, f.stagingCluster, f.stagingID, testRegion, 1)
	f.failed(t, f.stagingCluster, 0)

	statuses := f.statuses(t)
	if len(statuses) != 2 {
		t.Fatalf("got %d environment statuses, want 2", len(statuses))
	}
	if statuses[0].GetEnvironmentName() != statusProduction || statuses[1].GetEnvironmentName() != stagingName {
		t.Errorf("environments = %s, %s; want %s, %s",
			statuses[0].GetEnvironmentName(), statuses[1].GetEnvironmentName(), statusProduction, stagingName)
	}
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		ready: 1, desired: 1, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING,
	})
	f.assertEnvironment(t, f.stagingID, resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE,
		ready: 0, desired: 1, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_FAILED,
		message: statusCrashMsg,
	})
}

func TestResourceStatusSummarizesItsRegions(t *testing.T) {
	f := newResourceStatusFixture(t)
	f.deployTo(t, f.regionCluster, f.envID, statusSecondRegion, 1)
	f.deployProduction(t, 1)
	f.ready(t, f.clusterID, 1)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING,
		wantRegion{
			region: statusSecondRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING,
			ready: 0, desired: 1, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_PENDING,
		},
		wantRegion{
			region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
			ready: 1, desired: 1, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING,
		},
	)

	f.failed(t, f.regionCluster, 0)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED,
		wantRegion{
			region: statusSecondRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE,
			ready: 0, desired: 1, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_FAILED,
			message: statusCrashMsg,
		},
		wantRegion{
			region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
			ready: 1, desired: 1, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING,
		},
	)

	f.notReady(t, f.clusterID, 0)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE,
		wantRegion{
			region: statusSecondRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE,
			ready: 0, desired: 1, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_FAILED,
			message: statusCrashMsg,
		},
		wantRegion{
			region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE,
			ready: 0, desired: 1, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING,
			message: statusWaitingMsg,
		},
	)
}

func TestFailedRedeployLeavesServingReplicasHealthy(t *testing.T) {
	f := newResourceStatusFixture(t)
	f.deployProduction(t, 1)
	f.ready(t, f.clusterID, 1)

	f.deployProduction(t, 1)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		ready: 1, desired: 1, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_PENDING,
	})

	f.failed(t, f.clusterID, 1)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		ready: 1, desired: 1, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_FAILED,
		message: statusCrashMsg,
	})

	f.failed(t, f.clusterID, 0)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE,
		ready: 0, desired: 1, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_FAILED,
		message: statusCrashMsg,
	})
}

func TestRemovedRegionDropsOutOfTheStatus(t *testing.T) {
	f := newResourceStatusFixture(t)
	f.deployTo(t, f.regionCluster, f.envID, statusSecondRegion, 1)
	f.failed(t, f.regionCluster, 0)
	f.deployProduction(t, 1)
	f.ready(t, f.clusterID, 1)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED,
		wantRegion{
			region: statusSecondRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE,
			ready: 0, desired: 1, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_FAILED,
			message: statusCrashMsg,
		},
		wantRegion{
			region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
			ready: 1, desired: 1, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING,
		},
	)

	deployment, err := f.queries.GetActiveDeploymentForResourceAndRegion(f.ctx,
		genDb.GetActiveDeploymentForResourceAndRegionParams{
			ResourceID:    f.resourceID,
			EnvironmentID: f.envID,
			Region:        statusSecondRegion,
		})
	if err != nil {
		t.Fatalf("get active deployment: %v", err)
	}
	deleteCtx := context.WithValue(f.ctx, contextkeys.EntityScopesKey, []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspaceID, Scope: genDb.ScopeWrite},
	})
	deployments := NewDeploymentServer(f.pool, f.queries, nil, "", testServiceDefaults())
	_, err = deployments.DeleteDeployment(deleteCtx, connect.NewRequest(&deploymentv1.DeleteDeploymentRequest{
		DeploymentId: deployment.ID.String(),
	}))
	if err != nil {
		t.Fatalf("delete deployment: %v", err)
	}
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		ready: 1, desired: 1, deployment: deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING,
	})
}
