package service

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	genDb "github.com/team-loco/loco/api/gen/db"
	"github.com/team-loco/loco/api/limits"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

// reportReplicas reports pods as the replicas of the resource's placement in environmentID on
// clusterID, and returns the replicas the API should list for them.
func (f *resourceStatusFixture) reportReplicas(
	t *testing.T,
	resourceID, environmentID, clusterID uuid.UUID,
	replicas ...*deploymentv1.Replica,
) []*deploymentv1.Replica {
	t.Helper()
	p, err := f.queries.GetResourceEnvironmentPlacement(
		context.Background(),
		genDb.GetResourceEnvironmentPlacementParams{
			ResourceID: resourceID, EnvironmentID: environmentID, ClusterID: clusterID,
		},
	)
	if err != nil {
		t.Fatalf("get placement: %v", err)
	}
	ready := int32(0)
	want := make([]*deploymentv1.Replica, 0, len(replicas))
	for _, replica := range replicas {
		replica.DeploymentId = p.DeploymentID.String()
		replica.PlacementRevision = p.DesiredRevision
		if replica.GetReady() {
			ready++
		}
		want = append(want, proto.CloneOf(replica))
	}
	f.agentServer().recordStatus(context.Background(), clusterID, 0, &agentv1.PlacementStatus{
		PlacementId: p.ID.String(), ObservedRevision: p.DesiredRevision,
		ReadyReplicas: ready, Available: ready > 0, Healthy: ready > 0, Replicas: replicas,
	})
	return want
}

func servingReplica(name, node string, started time.Time) *deploymentv1.Replica {
	return &deploymentv1.Replica{
		Uid: "uid-" + name, Name: name, Node: node, Ready: true,
		State: string(loco.ReplicaServing), StateSince: timestamppb.New(started),
		StartedAt: timestamppb.New(started),
	}
}

func crashedReplica(name, node string, started, exited time.Time) *deploymentv1.Replica {
	return &deploymentv1.Replica{
		Uid: "uid-" + name, Name: name, Node: node, Restarts: 3,
		State: string(loco.ReplicaFailing), StateSince: timestamppb.New(exited),
		StartedAt: timestamppb.New(started), Detail: "restarting · 3 restarts",
		LastExit: &deploymentv1.ReplicaExit{Reason: "OOMKilled", ExitCode: 137, FinishedAt: timestamppb.New(exited)},
	}
}

func (f *resourceStatusFixture) region(t *testing.T, environmentID uuid.UUID, name string) *resourcev1.RegionStatus {
	t.Helper()
	for _, region := range f.environment(t, environmentID).GetRegions() {
		if region.GetRegion() == name {
			return region
		}
	}
	t.Fatalf("environment %s has no region %s", environmentID, name)
	return nil
}

func assertReplicas(t *testing.T, region *resourcev1.RegionStatus, want []*deploymentv1.Replica) {
	t.Helper()
	byName := func(a, b *deploymentv1.Replica) int { return strings.Compare(a.GetName(), b.GetName()) }
	got := slices.SortedFunc(slices.Values(region.GetReplicas()), byName)
	want = slices.SortedFunc(slices.Values(want), byName)
	if len(got) != len(want) {
		t.Fatalf("region %s replicas = %v, want %v", region.GetRegion(), got, want)
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Errorf("region %s replica %d = %v, want %v", region.GetRegion(), i, got[i], want[i])
		}
	}
}

// otherWorkspaceResource deploys a resource of another workspace to the production cluster and region.
func (f *resourceStatusFixture) otherWorkspaceResource(t *testing.T) (uuid.UUID, uuid.UUID) {
	t.Helper()
	workspaceID := f.tenant.workspace(t, "other-ws")
	environmentID := f.tenant.defaultEnvironment(t, workspaceID)
	resourceID := f.tenant.resource(t, workspaceID, "svc", &resourcev1.ServiceSpec{
		Routing: &resourcev1.RoutingConfig{Port: new(int32(8080))},
	})
	params := f.paramsFor(f.clusterID)
	params.ResourceID, params.EnvironmentID, params.Region = resourceID, environmentID, testRegion
	err := withTx(f.ctx, f.pool, func(qtx *genDb.Queries) error {
		_, deployErr := recordTestDeployment(f.ctx, qtx, limits.Install(limits.Defaults()), params, staticSpec)
		return deployErr
	})
	if err != nil {
		t.Fatalf("deploy the other workspace's resource: %v", err)
	}
	return resourceID, environmentID
}

func TestResourceStatusListsTheReplicasOfEachPlacement(t *testing.T) {
	t.Parallel()
	f := newResourceStatusFixture(t)
	f.deployProduction(t, statusTwoReplicas)
	f.deployTo(t, f.regionCluster, f.envID, statusSecondRegion, 1)
	f.deployTo(t, f.stagingCluster, f.stagingID, testRegion, 1)
	otherResource, otherEnvironment := f.otherWorkspaceResource(t)
	started := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	exited := started.Add(30 * time.Minute)

	production := f.reportReplicas(t, f.resourceID, f.envID, f.clusterID,
		servingReplica("svc-a", "node-1", started),
		crashedReplica("svc-b", "node-2", started, exited),
	)
	secondRegion := f.reportReplicas(t, f.resourceID, f.envID, f.regionCluster,
		servingReplica("svc-eu", "node-eu", started))
	staging := f.reportReplicas(t, f.resourceID, f.stagingID, f.stagingCluster,
		servingReplica("svc-staging", "node-s", started))
	f.reportReplicas(t, otherResource, otherEnvironment, f.clusterID, servingReplica("other-ws", "node-1", started))

	assertReplicas(t, f.region(t, f.envID, testRegion), production)
	assertReplicas(t, f.region(t, f.envID, statusSecondRegion), secondRegion)
	assertReplicas(t, f.region(t, f.stagingID, testRegion), staging)
}

func TestListedResourcesCarryNoReplicas(t *testing.T) {
	t.Parallel()
	f := newResourceStatusFixture(t)
	f.deployProduction(t, 1)
	f.reportReplicas(t, f.resourceID, f.envID, f.clusterID, servingReplica("svc-a", "node-1", time.Now()))

	listed, err := f.server().ListWorkspaceResources(f.ctx, &resourcev1.ListWorkspaceResourcesRequest{
		WorkspaceId: f.workspaceID.String(),
	})
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	regions := 0
	for _, resource := range listed.GetResources() {
		for _, environment := range resource.GetEnvironmentStatuses() {
			for _, region := range environment.GetRegions() {
				regions++
				if len(region.GetReplicas()) != 0 {
					t.Errorf("listed region %s carries replicas %v", region.GetRegion(), region.GetReplicas())
				}
			}
		}
	}
	if regions == 0 {
		t.Fatal("the listed resource has no region status")
	}
}

func TestSilentClusterKeepsTheLastReportedReplicas(t *testing.T) {
	t.Parallel()
	f := newResourceStatusFixture(t)
	f.deployProduction(t, 1)
	want := f.reportReplicas(t, f.resourceID, f.envID, f.clusterID, servingReplica("svc-a", "node-1", time.Now()))

	f.heartbeat(t, f.clusterID, time.Now().Add(-statusClusterSilent), clusterHealthHealthy)
	region := f.region(t, f.envID, testRegion)
	if region.GetStatus() != resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN {
		t.Errorf("silent region status = %s, want UNKNOWN", region.GetStatus())
	}
	if region.GetReportedAt() == nil {
		t.Error("silent region has no reported_at")
	}
	assertReplicas(t, region, want)
}

func TestSuspendedResourceShowsNoReplicas(t *testing.T) {
	t.Parallel()
	f := newResourceStatusFixture(t)
	f.deployProduction(t, 1)
	f.reportReplicas(t, f.resourceID, f.envID, f.clusterID, servingReplica("svc-a", "node-1", time.Now()))

	f.mustSuspend(t)
	assertReplicas(t, f.region(t, f.envID, testRegion), nil)
}

func TestRedeployedRegionShowsNoReplicasUntilReported(t *testing.T) {
	t.Parallel()
	f := newResourceStatusFixture(t)
	f.deployProduction(t, 1)
	f.reportReplicas(t, f.resourceID, f.envID, f.clusterID, &deploymentv1.Replica{
		Uid: "uid-svc-a", Name: "svc-a", State: string(loco.ReplicaStarting),
	})

	f.deployProduction(t, 1)
	assertReplicas(t, f.region(t, f.envID, testRegion), nil)
}

func TestRegionStatusSinceMarksTheLastStatusChange(t *testing.T) {
	t.Parallel()
	f := newResourceStatusFixture(t)
	f.deployProduction(t, statusTwoReplicas)
	started := time.Now().Add(-time.Hour)
	serving := func(name string) *deploymentv1.Replica { return servingReplica(name, "node-1", started) }

	f.reportReplicas(t, f.resourceID, f.envID, f.clusterID, serving("svc-a"), serving("svc-b"))
	healthy := f.region(t, f.envID, testRegion)
	if healthy.GetStatus() != resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY {
		t.Fatalf("status = %s, want HEALTHY", healthy.GetStatus())
	}
	f.reportReplicas(t, f.resourceID, f.envID, f.clusterID, serving("svc-a"), serving("svc-b"))
	again := f.region(t, f.envID, testRegion)
	if !proto.Equal(again.GetStatusSince(), healthy.GetStatusSince()) {
		t.Errorf("status_since moved from %v to %v while the status stayed healthy",
			healthy.GetStatusSince(), again.GetStatusSince())
	}

	f.reportReplicas(t, f.resourceID, f.envID, f.clusterID, serving("svc-a"))
	degraded := f.region(t, f.envID, testRegion)
	if degraded.GetStatus() != resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED {
		t.Fatalf("status = %s, want DEGRADED", degraded.GetStatus())
	}
	if !degraded.GetStatusSince().AsTime().After(healthy.GetStatusSince().AsTime()) {
		t.Errorf("degraded since %v is not after healthy since %v", degraded.GetStatusSince(), healthy.GetStatusSince())
	}
	if environment := f.environment(t, f.envID); !proto.Equal(environment.GetStatusSince(), degraded.GetStatusSince()) {
		t.Errorf(
			"environment status_since = %v, want the region's %v",
			environment.GetStatusSince(),
			degraded.GetStatusSince(),
		)
	}
}

func TestSilentRegionStatusSinceIsTheLastHeartbeat(t *testing.T) {
	t.Parallel()
	f := newResourceStatusFixture(t)
	f.deployProduction(t, 1)
	f.reportReplicas(t, f.resourceID, f.envID, f.clusterID, servingReplica("svc-a", "node-1", time.Now()))

	beat := time.Now().Add(-statusClusterSilent).UTC().Truncate(time.Millisecond)
	f.heartbeat(t, f.clusterID, beat, clusterHealthHealthy)
	got := f.region(t, f.envID, testRegion).GetStatusSince().AsTime()
	if !got.Equal(beat) {
		t.Errorf("silent region status_since = %v, want the last heartbeat %v", got, beat)
	}
}
