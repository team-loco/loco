package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	loco "github.com/team-loco/loco/k8sapi/v1alpha1"

	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	"github.com/team-loco/loco/api/reqctx"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	clusterv1 "github.com/team-loco/loco/gen/go/loco/cluster/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const (
	statusSecondRegion  = "us-va-1"
	statusCrashMsg      = "A replica keeps restarting. Check its logs."
	statusTwoReplicas   = 2
	statusProduction    = "prod"
	statusClusterSilent = 5 * time.Minute
	statusWaitTimeout   = 5 * time.Second
	statusPollInterval  = 50 * time.Millisecond
)

type resourceStatusFixture struct {
	*deployFixture
	ctx            context.Context
	silentAfter    time.Duration
	workspaceID    uuid.UUID
	regionCluster  uuid.UUID
	stagingCluster uuid.UUID
	stagingID      uuid.UUID
}

func newResourceStatusFixture(t *testing.T) *resourceStatusFixture {
	t.Helper()
	f := &resourceStatusFixture{deployFixture: newDeployFixture(t), silentAfter: testClusterSilentAfter}
	ctx := context.Background()
	f.workspaceID = f.deployFixture.workspaceID(t)
	f.stagingID = f.addStagingEnvironment(t)
	f.stagingCluster = f.otherCluster

	row := f.pool.QueryRow(ctx, `
WITH c AS (
    INSERT INTO clusters (platforms, name, region, provider, is_active, is_default)
    VALUES (ARRAY['linux/amd64'], 'c3', $1, 'kind', true, true) RETURNING id
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
	f.ctx = reqctx.WithPrincipal(ctx, reqctx.Principal{Scopes: scopes})
	return f
}

func (f *resourceStatusFixture) heartbeat(t *testing.T, clusterID uuid.UUID, at time.Time, health string) {
	t.Helper()
	setHeartbeats(t, f.pool, at, health, clusterID)
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
		_, deployErr := recordTestDeployment(f.ctx, qtx, limits.Install(limits.Defaults()), params, staticSpec)
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

func (f *resourceStatusFixture) report(
	t *testing.T,
	environmentID, clusterID uuid.UUID,
	status *agentv1.PlacementStatus,
) {
	t.Helper()
	p := f.placementIn(t, environmentID, clusterID)
	status.PlacementId = p.ID.String()
	status.ObservedRevision = p.DesiredRevision
	f.agentServer().recordStatus(context.Background(), clusterID, 0, status)
}

// readyReplicas is count ready replicas of deploymentID, as the agent lists them beside its ready count.
func readyReplicas(deploymentID uuid.UUID, count int32) []*deploymentv1.Replica {
	replicas := make([]*deploymentv1.Replica, 0, count)
	for i := range count {
		name := fmt.Sprintf("%s-%d", deploymentID, i)
		replicas = append(replicas, &deploymentv1.Replica{
			Uid: "uid-" + name, Name: name, DeploymentId: deploymentID.String(), Ready: true,
			State: string(loco.ReplicaServing),
		})
	}
	return replicas
}

func (f *resourceStatusFixture) ready(t *testing.T, clusterID uuid.UUID, replicas int32) {
	t.Helper()
	f.report(t, f.envID, clusterID, &agentv1.PlacementStatus{
		Available:     true,
		Healthy:       true,
		Deployed:      true,
		ReadyReplicas: replicas,
	})
}

func (f *resourceStatusFixture) notReady(t *testing.T, clusterID uuid.UUID, replicas int32) {
	t.Helper()
	f.report(t, f.envID, clusterID, &agentv1.PlacementStatus{
		ReadyReplicas: replicas,
	})
}

func (f *resourceStatusFixture) failed(t *testing.T, environmentID, clusterID uuid.UUID) {
	t.Helper()
	f.report(
		t,
		environmentID,
		clusterID,
		&agentv1.PlacementStatus{
			Problem: &agentv1.ProblemReport{Reason: string(loco.ProblemRestarting)},
			Stopped: deployTimedOut(),
		},
	)
}

func (f *resourceStatusFixture) server() *ResourceServer {
	return NewResourceServer(f.pool, f.queries, testServiceDefaults(), f.silentAfter, limits.Defaults())
}

func (f *resourceStatusFixture) statuses(t *testing.T) []*resourcev1.EnvironmentStatus {
	t.Helper()
	server := f.server()
	resourceID := f.resourceID.String()

	got, err := server.GetResource(f.ctx, &resourcev1.GetResourceRequest{
		Key: &resourcev1.GetResourceRequest_ResourceId{ResourceId: resourceID},
	})
	if err != nil {
		t.Fatalf("get resource: %v", err)
	}
	fromGet := got.GetResource().GetEnvironmentStatuses()

	listed, err := server.ListWorkspaceResources(f.ctx, &resourcev1.ListWorkspaceResourcesRequest{
		WorkspaceId: f.workspaceID.String(),
	})
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	resources := listed.GetResources()
	if len(resources) != 1 {
		t.Fatalf("listed %d resources, want 1", len(resources))
	}
	fromList := resources[0].GetEnvironmentStatuses()

	if !statusesEqual(fromGet, fromList) {
		t.Fatalf("read RPCs disagree:\nGetResource %v\nListWorkspaceResources %v", fromGet, fromList)
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
				region.GetReadyReplicas() != other.GetReadyReplicas() || region.GetMessage() != other.GetMessage() ||
				region.GetServingDeploymentId() != other.GetServingDeploymentId() {
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
	deployment deploymentv1.PlacementStatus
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
		t.Errorf("environment %s status = %s, want %s", got.GetEnvironmentSlug(), got.GetStatus(), want)
	}
	if len(got.GetRegions()) != len(regions) {
		t.Fatalf("environment %s has %d regions, want %d",
			got.GetEnvironmentSlug(), len(got.GetRegions()), len(regions))
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
		if region.GetPlacementStatus() != wantRegion.deployment {
			t.Errorf("region %s deployment = %s, want %s",
				wantRegion.region, region.GetPlacementStatus(), wantRegion.deployment)
		}
		if region.GetMessage() != wantRegion.message {
			t.Errorf("region %s message = %q, want %q", wantRegion.region, region.GetMessage(), wantRegion.message)
		}
	}
}

func TestResourceStatusIsEmptyBeforeAnyDeployment(t *testing.T) {
	t.Parallel()
	f := newResourceStatusFixture(t)
	if statuses := f.statuses(t); len(statuses) != 0 {
		t.Fatalf("statuses = %v, want none", statuses)
	}
}

func TestResourceStatusFollowsTheReplicasTheAgentReports(t *testing.T) {
	t.Parallel()
	f := newResourceStatusFixture(t)
	f.deployProduction(t, statusTwoReplicas)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING,
		ready: 0, desired: 2, deployment: deploymentv1.PlacementStatus_PLACEMENT_STATUS_DEPLOYING,
	})

	f.ready(t, f.clusterID, statusTwoReplicas)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		ready: 2, desired: 2, deployment: deploymentv1.PlacementStatus_PLACEMENT_STATUS_RUNNING,
	})

	f.notReady(t, f.clusterID, 0)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE,
		ready: 0, desired: 2, deployment: deploymentv1.PlacementStatus_PLACEMENT_STATUS_DEGRADED,
		message: "",
	})
}

func TestResourceStatusIsUnknownWhileTheClusterIsSilent(t *testing.T) {
	t.Parallel()
	f := newResourceStatusFixture(t)
	f.deployProduction(t, 1)
	f.ready(t, f.clusterID, 1)
	healthy := wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		ready: 1, desired: 1, deployment: deploymentv1.PlacementStatus_PLACEMENT_STATUS_RUNNING,
	}
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY, healthy)

	f.heartbeat(t, f.clusterID, time.Now().Add(-statusClusterSilent), clusterHealthHealthy)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN,
		ready: 1, desired: 1, deployment: deploymentv1.PlacementStatus_PLACEMENT_STATUS_RUNNING,
		message: regionMessageClusterSilent,
	})

	f.heartbeat(t, f.clusterID, time.Now(), clusterHealthDegraded)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN,
		ready: 1, desired: 1, deployment: deploymentv1.PlacementStatus_PLACEMENT_STATUS_RUNNING,
		message: regionMessageClusterUnhealthy,
	})

	f.heartbeat(t, f.clusterID, time.Now(), clusterHealthHealthy)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY, healthy)
}

func TestResourceStatusIsPerEnvironment(t *testing.T) {
	t.Parallel()
	f := newResourceStatusFixture(t)
	f.deployProduction(t, 1)
	f.ready(t, f.clusterID, 1)
	f.deployTo(t, f.stagingCluster, f.stagingID, testRegion, 1)
	f.failed(t, f.stagingID, f.stagingCluster)

	statuses := f.statuses(t)
	if len(statuses) != 2 {
		t.Fatalf("got %d environment statuses, want 2", len(statuses))
	}
	if statuses[0].GetEnvironmentSlug() != statusProduction || statuses[1].GetEnvironmentSlug() != stagingName {
		t.Errorf("environments = %s, %s; want %s, %s",
			statuses[0].GetEnvironmentSlug(), statuses[1].GetEnvironmentSlug(), statusProduction, stagingName)
	}
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		ready: 1, desired: 1, deployment: deploymentv1.PlacementStatus_PLACEMENT_STATUS_RUNNING,
	})
	f.assertEnvironment(t, f.stagingID, resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE,
		ready: 0, desired: 1, deployment: deploymentv1.PlacementStatus_PLACEMENT_STATUS_FAILED,
		message: statusCrashMsg,
	})
}

func TestResourceStatusSummarizesItsRegions(t *testing.T) {
	t.Parallel()
	f := newResourceStatusFixture(t)
	f.deployTo(t, f.regionCluster, f.envID, statusSecondRegion, 1)
	f.deployProduction(t, 1)
	f.ready(t, f.clusterID, 1)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING,
		wantRegion{
			region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
			ready: 1, desired: 1, deployment: deploymentv1.PlacementStatus_PLACEMENT_STATUS_RUNNING,
		},
		wantRegion{
			region: statusSecondRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING,
			ready: 0, desired: 1, deployment: deploymentv1.PlacementStatus_PLACEMENT_STATUS_DEPLOYING,
		},
	)

	f.failed(t, f.envID, f.regionCluster)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED,
		wantRegion{
			region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
			ready: 1, desired: 1, deployment: deploymentv1.PlacementStatus_PLACEMENT_STATUS_RUNNING,
		},
		wantRegion{
			region: statusSecondRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE,
			ready: 0, desired: 1, deployment: deploymentv1.PlacementStatus_PLACEMENT_STATUS_FAILED,
			message: statusCrashMsg,
		},
	)
}

func TestFailedRedeployLeavesServingReplicasHealthy(t *testing.T) {
	t.Parallel()
	f := newResourceStatusFixture(t)
	f.deployProduction(t, statusTwoReplicas)
	previous := *f.placementIn(t, f.envID, f.clusterID).DeploymentID
	f.report(t, f.envID, f.clusterID, &agentv1.PlacementStatus{
		Available:     true,
		Healthy:       true,
		Deployed:      true,
		ReadyReplicas: statusTwoReplicas,
		Replicas:      readyReplicas(previous, statusTwoReplicas),
	})

	f.deployProduction(t, statusTwoReplicas)
	failed := *f.placementIn(t, f.envID, f.clusterID).DeploymentID
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		ready: 2, desired: 2, deployment: deploymentv1.PlacementStatus_PLACEMENT_STATUS_DEPLOYING,
	})
	f.assertServing(t, failed, previous)

	stuck := readyReplicas(failed, 1)
	stuck[0].Ready = false
	stuck[0].State = string(loco.ReplicaStarting)
	f.report(t, f.envID, f.clusterID, &agentv1.PlacementStatus{
		Problem:       &agentv1.ProblemReport{Reason: string(loco.ProblemRestarting)},
		Stopped:       deployTimedOut(),
		ReadyReplicas: statusTwoReplicas,
		Replicas:      append(readyReplicas(previous, statusTwoReplicas), stuck...),
	})
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		ready: 2, desired: 2, deployment: deploymentv1.PlacementStatus_PLACEMENT_STATUS_FAILED,
		message: statusCrashMsg,
	})
	f.assertServing(t, failed, previous)
}

func TestOverlappingRolloutReadsNoMoreReadyThanDesired(t *testing.T) {
	t.Parallel()
	for _, desired := range []int32{1, statusTwoReplicas} {
		t.Run(fmt.Sprintf("%d desired", desired), func(t *testing.T) {
			t.Parallel()
			f := newResourceStatusFixture(t)
			f.deployProduction(t, desired)
			previous := *f.placementIn(t, f.envID, f.clusterID).DeploymentID
			f.report(t, f.envID, f.clusterID, &agentv1.PlacementStatus{
				Available: true, Healthy: true, Deployed: true, ReadyReplicas: desired,
				Replicas: readyReplicas(previous, desired),
			})

			f.deployProduction(t, desired)
			next := *f.placementIn(t, f.envID, f.clusterID).DeploymentID
			f.report(t, f.envID, f.clusterID, &agentv1.PlacementStatus{
				ReadyReplicas: 2,
				Replicas:      append(readyReplicas(previous, 1), readyReplicas(next, 1)...),
			})
			f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY, wantRegion{
				region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
				ready: desired, desired: desired, deployment: deploymentv1.PlacementStatus_PLACEMENT_STATUS_DEPLOYING,
			})
			f.assertServing(t, next, next)
		})
	}
}

func (f *resourceStatusFixture) assertServing(t *testing.T, live, serving uuid.UUID) {
	t.Helper()
	region := f.region(t, f.envID, testRegion)
	if region.GetDeploymentId() != live.String() || region.GetServingDeploymentId() != serving.String() {
		t.Errorf("region deployment %s serving %s, want %s serving %s",
			region.GetDeploymentId(), region.GetServingDeploymentId(), live, serving)
	}
}

func TestRemovedRegionDropsOutOfTheStatus(t *testing.T) {
	t.Parallel()
	f := newResourceStatusFixture(t)
	f.deployTo(t, f.regionCluster, f.envID, statusSecondRegion, 1)
	f.failed(t, f.envID, f.regionCluster)
	f.deployProduction(t, 1)
	f.ready(t, f.clusterID, 1)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED,
		wantRegion{
			region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
			ready: 1, desired: 1, deployment: deploymentv1.PlacementStatus_PLACEMENT_STATUS_RUNNING,
		},
		wantRegion{
			region: statusSecondRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE,
			ready: 0, desired: 1, deployment: deploymentv1.PlacementStatus_PLACEMENT_STATUS_FAILED,
			message: statusCrashMsg,
		},
	)

	deployment, err := f.queries.GetLiveDeploymentForRegion(f.ctx,
		genDb.GetLiveDeploymentForRegionParams{
			ResourceID:    f.resourceID,
			EnvironmentID: f.envID,
			Region:        statusSecondRegion,
		})
	if err != nil {
		t.Fatalf("get active deployment: %v", err)
	}
	deleteCtx := reqctx.WithPrincipal(f.ctx, reqctx.Principal{Scopes: []genDb.EntityScope{
		{EntityType: genDb.EntityTypeWorkspace, EntityID: f.workspaceID, Scope: genDb.ScopeWrite},
	}})
	deployments := NewDeploymentServer(
		f.pool,
		f.queries,
		nil,
		"",
		testServiceDefaults(),
		testClusterSilentAfter,
		testMaxPlacementBytes,
		limits.Defaults(),
		nil,
	)
	_, err = deployments.DeleteDeployment(deleteCtx, &deploymentv1.DeleteDeploymentRequest{
		DeploymentId: deployment.Deployment.ID.String(),
	})
	if err != nil {
		t.Fatalf("delete deployment: %v", err)
	}
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		ready: 1, desired: 1, deployment: deploymentv1.PlacementStatus_PLACEMENT_STATUS_RUNNING,
	})
}

func (f *resourceStatusFixture) waitForProduction(t *testing.T, want resourcev1.ResourceStatus) {
	t.Helper()
	deadline := time.Now().Add(statusWaitTimeout)
	server := f.server()
	for {
		resource, err := server.GetResource(f.ctx, &resourcev1.GetResourceRequest{
			Key: &resourcev1.GetResourceRequest_ResourceId{ResourceId: f.resourceID.String()},
		})
		if err != nil {
			t.Fatalf("get resource: %v", err)
		}
		got := resourcev1.ResourceStatus_RESOURCE_STATUS_UNSPECIFIED
		for _, environment := range resource.GetResource().GetEnvironmentStatuses() {
			if environment.GetEnvironmentId() == f.envID.String() {
				got = environment.GetStatus()
				break
			}
		}
		if got == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("production status = %s, want %s", got, want)
		}
		time.Sleep(statusPollInterval)
	}
}

func TestResourceStatusIsUnknownOnceTheSyncStreamDrops(t *testing.T) {
	f := newResourceStatusFixture(t)
	f.deployProduction(t, 1)
	f.ready(t, f.clusterID, 1)
	f.heartbeat(t, f.clusterID, time.Now().Add(-statusClusterSilent), clusterHealthHealthy)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN,
		ready: 1, desired: 1, deployment: deploymentv1.PlacementStatus_PLACEMENT_STATUS_RUNNING,
		message: regionMessageClusterSilent,
	})

	client := startSyncServer(t, f.deployFixture)
	streamCtx, dropStream := context.WithCancel(f.ctx)
	defer dropStream()
	streamCtx = agentContext(streamCtx, t, f.clusterID)
	stream, err := client.Sync(streamCtx)
	if err != nil {
		t.Fatal(err)
	}
	responsesStopped := make(chan struct{})
	go func() {
		defer close(responsesStopped)
		for {
			if _, receiveErr := stream.Receive(); receiveErr != nil {
				return
			}
		}
	}()
	defer func() {
		dropStream()
		<-responsesStopped
	}()
	heartbeat := &agentv1.SyncRequest{
		Message: &agentv1.SyncRequest_Heartbeat{
			Heartbeat: &agentv1.Heartbeat{
				Role:   clusterv1.ClusterRole_CLUSTER_ROLE_ALL,
				Health: &agentv1.AgentHealth{KubernetesHealthy: true, ControllerHealthy: true},
			},
		},
	}
	if err := stream.Send(heartbeat); err != nil {
		t.Fatal(err)
	}
	f.waitForProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY)
	if err := stream.CloseSend(); err != nil {
		t.Fatal(err)
	}
	<-responsesStopped
	f.heartbeat(t, f.clusterID, time.Now().Add(-statusClusterSilent), clusterHealthHealthy)
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN,
		ready: 1, desired: 1, deployment: deploymentv1.PlacementStatus_PLACEMENT_STATUS_RUNNING,
		message: regionMessageClusterSilent,
	})
}

func TestRegionLastGoodDeploymentIsTheNewestEarlierHealthyOneInTheRegion(t *testing.T) {
	t.Parallel()
	f := newResourceStatusFixture(t)
	f.deployProduction(t, 1)
	first := *f.placementIn(t, f.envID, f.clusterID).DeploymentID
	f.ready(t, f.clusterID, 1)
	f.assertLastGood(t, "")

	f.deployProduction(t, 1)
	second := *f.placementIn(t, f.envID, f.clusterID).DeploymentID
	f.ready(t, f.clusterID, 1)
	f.assertLastGood(t, first.String())

	// A failed deployment is never a rollback target, and the one it failed to replace still is.
	f.deployProduction(t, 1)
	f.failed(t, f.envID, f.clusterID)
	f.assertLastGood(t, second.String())

	// Newer deployments in another region or environment leave this region's target alone.
	f.deployTo(t, f.regionCluster, f.envID, statusSecondRegion, 1)
	f.deployTo(t, f.stagingCluster, f.stagingID, testRegion, 1)
	f.assertLastGood(t, second.String())
}

func (f *resourceStatusFixture) assertLastGood(t *testing.T, want string) {
	t.Helper()
	if got := f.region(t, f.envID, testRegion).GetLastGoodDeploymentId(); got != want {
		t.Errorf("last good deployment %q, want %q", got, want)
	}
}

func TestRegionLastGoodDeploymentSkipsADeploymentReplacedBeforeItRan(t *testing.T) {
	t.Parallel()
	f := newResourceStatusFixture(t)
	f.deployProduction(t, 1)
	first := *f.placementIn(t, f.envID, f.clusterID).DeploymentID
	f.ready(t, f.clusterID, 1)

	f.deployProduction(t, 1)
	f.deployProduction(t, 1)
	f.assertLastGood(t, first.String())

	f.ready(t, f.clusterID, 1)
	f.assertLastGood(t, first.String())
}

func TestResourceStatusStaysHealthyWhileTheAutoscalerAddsAReplica(t *testing.T) {
	t.Parallel()
	f := newResourceStatusFixture(t)
	f.deployProduction(t, 1)
	f.ready(t, f.clusterID, 1)
	f.report(t, f.envID, f.clusterID, &agentv1.PlacementStatus{
		Available: true, Healthy: true, Deployed: true, ReadyReplicas: 1, DesiredReplicas: statusTwoReplicas,
	})
	f.assertProduction(t, resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY, wantRegion{
		region: testRegion, status: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		ready: 1, desired: 1, deployment: deploymentv1.PlacementStatus_PLACEMENT_STATUS_RUNNING,
	})
}
