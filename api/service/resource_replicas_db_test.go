package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

// reportReplicas reports the named pods as the replicas of the placement in environmentID on clusterID.
func (f *resourceStatusFixture) reportReplicas(
	t *testing.T,
	environmentID, clusterID uuid.UUID,
	replicas ...*agentv1.ReplicaReport,
) []*deploymentv1.Replica {
	t.Helper()
	p := f.placementIn(t, environmentID, clusterID)
	ready := int32(0)
	want := make([]*deploymentv1.Replica, 0, len(replicas))
	for _, replica := range replicas {
		replica.DeploymentId = p.DeploymentID.String()
		replica.PlacementRevision = p.DesiredRevision
		if replica.GetReady() {
			ready++
		}
		want = append(want, &deploymentv1.Replica{
			Uid: replica.GetUid(), Name: replica.GetName(), Node: replica.GetNode(),
			PlacementRevision: replica.GetPlacementRevision(), DeploymentId: replica.GetDeploymentId(),
			Ready: replica.GetReady(), State: replica.GetState(), StateSince: replica.GetStateSince(),
			Restarts: replica.GetRestarts(), Detail: replica.GetDetail(),
			StartedAt: replica.GetStartedAt(), LastExit: replica.GetLastExit(),
		})
	}
	f.report(t, environmentID, clusterID, &agentv1.PlacementStatus{
		ReadyReplicas: ready, Available: ready > 0, Replicas: replicas,
	})
	return want
}

func servingReplica(name, node string, started time.Time) *agentv1.ReplicaReport {
	return &agentv1.ReplicaReport{
		Uid: "uid-" + name, Name: name, Node: node, Ready: true,
		State: string(loco.ReplicaServing), StateSince: timestamppb.New(started),
		StartedAt: timestamppb.New(started),
	}
}

func crashedReplica(name, node string, started, exited time.Time) *agentv1.ReplicaReport {
	return &agentv1.ReplicaReport{
		Uid: "uid-" + name, Name: name, Node: node, Restarts: 3,
		State: string(loco.ReplicaFailing), StateSince: timestamppb.New(exited),
		StartedAt: timestamppb.New(started), Detail: "restarting · 3 restarts",
		LastExit: &deploymentv1.ReplicaExit{Reason: "OOMKilled", ExitCode: 137, At: timestamppb.New(exited)},
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
	got := region.GetReplicas()
	if len(got) != len(want) {
		t.Fatalf("region %s replicas = %v, want %v", region.GetRegion(), got, want)
	}
	for i := range want {
		if !proto.Equal(got[i], want[i]) {
			t.Errorf("region %s replica %d = %v, want %v", region.GetRegion(), i, got[i], want[i])
		}
	}
}

func TestResourceStatusListsTheReplicasOfEachPlacement(t *testing.T) {
	t.Parallel()
	f := newResourceStatusFixture(t)
	f.deployProduction(t, statusTwoReplicas)
	f.deployTo(t, f.regionCluster, f.envID, statusSecondRegion, 1)
	f.deployTo(t, f.stagingCluster, f.stagingID, testRegion, 1)
	started := time.Now().Add(-time.Hour).UTC().Truncate(time.Second)
	exited := started.Add(30 * time.Minute)

	production := f.reportReplicas(t, f.envID, f.clusterID,
		servingReplica("svc-a", "node-1", started),
		crashedReplica("svc-b", "node-2", started, exited),
	)
	secondRegion := f.reportReplicas(t, f.envID, f.regionCluster, servingReplica("svc-eu", "node-eu", started))
	staging := f.reportReplicas(t, f.stagingID, f.stagingCluster, servingReplica("svc-staging", "node-s", started))

	assertReplicas(t, f.region(t, f.envID, testRegion), production)
	assertReplicas(t, f.region(t, f.envID, statusSecondRegion), secondRegion)
	assertReplicas(t, f.region(t, f.stagingID, testRegion), staging)
}

func TestListedResourcesCarryNoReplicas(t *testing.T) {
	t.Parallel()
	f := newResourceStatusFixture(t)
	f.deployProduction(t, 1)
	f.reportReplicas(t, f.envID, f.clusterID, servingReplica("svc-a", "node-1", time.Now()))

	listed, err := f.server().ListWorkspaceResources(f.ctx, &resourcev1.ListWorkspaceResourcesRequest{
		WorkspaceId: f.workspaceID.String(),
	})
	if err != nil {
		t.Fatalf("list resources: %v", err)
	}
	for _, resource := range listed.GetResources() {
		for _, environment := range resource.GetEnvironmentStatuses() {
			for _, region := range environment.GetRegions() {
				if len(region.GetReplicas()) != 0 {
					t.Errorf("listed region %s carries replicas %v", region.GetRegion(), region.GetReplicas())
				}
			}
		}
	}
}

func TestSilentClusterKeepsTheLastReportedReplicas(t *testing.T) {
	t.Parallel()
	f := newResourceStatusFixture(t)
	f.deployProduction(t, 1)
	want := f.reportReplicas(t, f.envID, f.clusterID, servingReplica("svc-a", "node-1", time.Now()))

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
	f.reportReplicas(t, f.envID, f.clusterID, servingReplica("svc-a", "node-1", time.Now()))

	ctx := context.Background()
	var organization uuid.UUID
	if err := f.pool.QueryRow(ctx, `SELECT org_id FROM workspaces WHERE id=$1`, f.workspaceID).
		Scan(&organization); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, `UPDATE organizations SET suspended_at=NOW(), suspension_reason='fixture'
WHERE id=$1`, organization); err != nil {
		t.Fatal(err)
	}
	if _, err := f.queries.SuspendOrgPlacements(ctx, organization); err != nil {
		t.Fatal(err)
	}
	assertReplicas(t, f.region(t, f.envID, testRegion), nil)
}
