package service

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const (
	testHeartbeatInterval  = 20 * time.Second
	testClusterSilentAfter = missedHeartbeats * testHeartbeatInterval
	testFreshHeartbeat     = 10 * time.Second
	testStaleHeartbeat     = 2 * time.Minute
	testDesiredReplicas    = 2
	testStatusMessage      = "A replica keeps restarting. Check its logs."
	testFailureMessage     = "container exited with code 1"
)

var testStatusNow = time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)

func regionRow(status genDb.PlacementStatus, ready int32) genDb.ListRegionStatusesForResourcesRow {
	heartbeat := testStatusNow.Add(-testFreshHeartbeat)
	reported := testStatusNow.Add(-testFreshHeartbeat)
	health := clusterHealthHealthy
	return genDb.ListRegionStatusesForResourcesRow{
		ResourceID:      uuid.New(),
		EnvironmentID:   uuid.New(),
		EnvironmentSlug: "production",
		Region:          testRegion,
		DeploymentID:    uuid.New(),
		PlacementStatus: status,
		DesiredReplicas: testDesiredReplicas,
		Placed:          true,
		ReadyReplicas:   ready,
		Problem:         []byte(`{"reason":"restarting"}`),
		StatusUpdatedAt: &reported,
		Replicas:        []byte(`[]`),
		LastHeartbeat:   &heartbeat,
		HealthStatus:    &health,
	}
}

// replicasJSON is a stored rollout listing ready replicas of deploymentID.
func replicasJSON(deploymentID uuid.UUID, ready int32) []byte {
	replicas := make([]*deploymentv1.Replica, 0, ready)
	for range ready {
		replicas = append(replicas, &deploymentv1.Replica{DeploymentId: deploymentID.String(), Ready: true})
	}
	data, err := json.Marshal(replicas)
	if err != nil {
		panic(err)
	}
	return data
}

func TestRegionStatus(t *testing.T) {
	t.Parallel()
	staleHeartbeat := testStatusNow.Add(-testStaleHeartbeat)
	edgeHeartbeat := testStatusNow.Add(-testClusterSilentAfter)
	unhealthy := clusterHealthUnhealthy
	degradedCluster := clusterHealthDegraded

	tests := []struct {
		name    string
		row     func() genDb.ListRegionStatusesForResourcesRow
		want    resourcev1.ResourceStatus
		message string
	}{
		{
			name: "source build before placement",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.PlacementStatusPending, 0)
				row.Placed = false
				row.StatusUpdatedAt = nil
				row.Problem = []byte(`null`)
				return row
			},
			want: resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING,
		},
		{
			name: "every desired replica ready",
			row:  func() genDb.ListRegionStatusesForResourcesRow { return regionRow(genDb.PlacementStatusRunning, 2) },
			want: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		},
		{
			name: "surge replicas during a rollout",
			row:  func() genDb.ListRegionStatusesForResourcesRow { return regionRow(genDb.PlacementStatusDeploying, 3) },
			want: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		},
		{
			name:    "running deployment whose pods all crashed",
			row:     func() genDb.ListRegionStatusesForResourcesRow { return regionRow(genDb.PlacementStatusRunning, 0) },
			want:    resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE,
			message: testStatusMessage,
		},
		{
			name:    "every replica ready but one restarted within the health window",
			row:     func() genDb.ListRegionStatusesForResourcesRow { return regionRow(genDb.PlacementStatusDegraded, 2) },
			want:    resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED,
			message: testStatusMessage,
		},
		{
			name:    "service stopped after too many restarts",
			row:     func() genDb.ListRegionStatusesForResourcesRow { return regionRow(genDb.PlacementStatusCrashed, 0) },
			want:    resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE,
			message: testStatusMessage,
		},
		{
			name:    "running deployment that lost a replica",
			row:     func() genDb.ListRegionStatusesForResourcesRow { return regionRow(genDb.PlacementStatusRunning, 1) },
			want:    resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED,
			message: testStatusMessage,
		},
		{
			name: "first rollout before any replica is ready",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				return regionRow(genDb.PlacementStatusDeploying, 0)
			},
			want:    resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING,
			message: testStatusMessage,
		},
		{
			name: "pending deployment the agent has not reported on",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.PlacementStatusDeploying, 0)
				row.StatusUpdatedAt = nil
				row.Problem = []byte(`null`)
				return row
			},
			want: resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING,
		},
		{
			name: "redeploy while the previous replicas still serve",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.PlacementStatusDeploying, 2)
				row.Replicas = replicasJSON(uuid.New(), 2)
				return row
			},
			want: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		},
		{
			name: "failed redeploy while the previous replicas still serve",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.PlacementStatusFailed, 2)
				row.Replicas = replicasJSON(uuid.New(), 2)
				row.Problem = []byte(
					`{"reason":"deploy_timeout","detail":"` + testFailureMessage + `"}`,
				)
				return row
			},
			want: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
			message: "The new version did not become healthy within its deploy timeout, so Loco stopped it. " +
				"Check its replicas and logs, then redeploy. " + testFailureMessage,
		},
		{
			name: "failed deployment with nothing serving",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.PlacementStatusFailed, 0)
				row.Problem = []byte(
					`{"reason":"deploy_timeout","detail":"` + testFailureMessage + `"}`,
				)
				return row
			},
			want: resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE,
			message: "The new version did not become healthy within its deploy timeout, so Loco stopped it. " +
				"Check its replicas and logs, then redeploy. " + testFailureMessage,
		},
		{
			name: "healthy report from a cluster that stopped heartbeating",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.PlacementStatusRunning, 2)
				row.LastHeartbeat = &staleHeartbeat
				return row
			},
			want:    resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN,
			message: regionMessageClusterSilent,
		},
		{
			name: "heartbeat exactly at the threshold",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.PlacementStatusRunning, 2)
				row.LastHeartbeat = &edgeHeartbeat
				return row
			},
			want: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		},
		{
			name: "cluster that never heartbeated",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.PlacementStatusRunning, 2)
				row.LastHeartbeat = nil
				return row
			},
			want:    resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN,
			message: regionMessageClusterNeverHeard,
		},
		{
			name: "cluster whose controller is down",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.PlacementStatusRunning, 2)
				row.HealthStatus = &degradedCluster
				return row
			},
			want:    resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN,
			message: regionMessageClusterUnhealthy,
		},
		{
			name: "unhealthy cluster",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.PlacementStatusRunning, 0)
				row.HealthStatus = &unhealthy
				return row
			},
			want:    resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN,
			message: regionMessageClusterUnhealthy,
		},
		{
			name: "cluster without a health status",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.PlacementStatusRunning, 2)
				row.HealthStatus = nil
				return row
			},
			want:    resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN,
			message: regionMessageClusterUnhealthy,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := tt.row()
			got, err := regionStatus(row, testStatusNow, testClusterSilentAfter)
			if err != nil {
				t.Fatalf("regionStatus: %v", err)
			}
			if got.GetStatus() != tt.want {
				t.Errorf("status = %s, want %s", got.GetStatus(), tt.want)
			}
			if got.GetMessage() != tt.message {
				t.Errorf("message = %q, want %q", got.GetMessage(), tt.message)
			}
			ready := min(row.ReadyReplicas, row.DesiredReplicas)
			if got.GetDesiredReplicas() != row.DesiredReplicas || got.GetReadyReplicas() != ready {
				t.Errorf("replicas = %d/%d, want %d/%d",
					got.GetReadyReplicas(), got.GetDesiredReplicas(), ready, row.DesiredReplicas)
			}
			if got.GetDeploymentId() != row.DeploymentID.String() {
				t.Errorf("deployment id = %q, want %q", got.GetDeploymentId(), row.DeploymentID)
			}
		})
	}
}

func TestRegionStatusRejectsBrokenInvariants(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		row  genDb.ListRegionStatusesForResourcesRow
		want error
	}{
		{
			name: "succeeded deployment still active",
			row:  regionRow(genDb.PlacementStatusSuperseded, 2),
			want: errFinishedDeploymentActive,
		},
		{
			name: "canceled deployment still active",
			row:  regionRow(genDb.PlacementStatusCanceled, 0),
			want: errFinishedDeploymentActive,
		},
		{
			name: "unknown deployment status",
			row:  regionRow(genDb.PlacementStatus("exploded"), 0),
			want: errUnknownDeploymentStatus,
		},
		{
			name: "deployment with no desired replicas",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.PlacementStatusRunning, 0)
				row.DesiredReplicas = 0
				return row
			}(),
			want: errNoDesiredReplicas,
		},
		{
			name: "active deployment without a placement",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.PlacementStatusRunning, 0)
				row.Placed = false
				return row
			}(),
			want: errActiveDeploymentUnplaced,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := regionStatus(tt.row, testStatusNow, testClusterSilentAfter)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestRegionStatusNeverOverstatesHealth(t *testing.T) {
	t.Parallel()
	deploymentStatuses := []genDb.PlacementStatus{
		genDb.PlacementStatusPending,
		genDb.PlacementStatusDeploying,
		genDb.PlacementStatusRunning,
		genDb.PlacementStatusFailed,
	}
	heartbeats := []*time.Time{
		nil,
		new(testStatusNow.Add(-testFreshHeartbeat)),
		new(testStatusNow.Add(-testStaleHeartbeat)),
	}
	healths := []*string{nil, new(clusterHealthHealthy), new(clusterHealthDegraded), new(clusterHealthUnhealthy)}
	readies := []int32{0, 1, testDesiredReplicas, testDesiredReplicas + 1}

	for _, deploymentStatus := range deploymentStatuses {
		for _, heartbeat := range heartbeats {
			for _, health := range healths {
				for _, ready := range readies {
					row := regionRow(deploymentStatus, ready)
					row.LastHeartbeat = heartbeat
					row.HealthStatus = health
					got, err := regionStatus(row, testStatusNow, testClusterSilentAfter)
					if err != nil {
						t.Fatalf("regionStatus(%+v): %v", row, err)
					}
					assertStatusMatchesInputs(t, row, got.GetStatus())
				}
			}
		}
	}
}

func assertStatusMatchesInputs(
	t *testing.T,
	row genDb.ListRegionStatusesForResourcesRow,
	status resourcev1.ResourceStatus,
) {
	t.Helper()
	fresh := row.LastHeartbeat != nil &&
		testStatusNow.Sub(*row.LastHeartbeat) <= testClusterSilentAfter &&
		row.HealthStatus != nil && *row.HealthStatus == clusterHealthHealthy
	inFlight := row.PlacementStatus == genDb.PlacementStatusPending ||
		row.PlacementStatus == genDb.PlacementStatusDeploying
	var want resourcev1.ResourceStatus
	switch {
	case !fresh:
		want = resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN
	case row.ReadyReplicas >= row.DesiredReplicas:
		want = resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY
	case row.ReadyReplicas > 0:
		want = resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED
	case inFlight:
		want = resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING
	default:
		want = resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE
	}
	if status != want {
		t.Errorf("deployment %s, heartbeat %v, health %v, ready %d/%d: status %s, want %s",
			row.PlacementStatus, row.LastHeartbeat, row.HealthStatus,
			row.ReadyReplicas, row.DesiredReplicas, status, want)
	}
}

func TestSummarizeRegions(t *testing.T) {
	t.Parallel()
	healthy := resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY
	deploying := resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING
	degraded := resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED
	unavailable := resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE
	unknown := resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN

	tests := []struct {
		name    string
		regions []resourcev1.ResourceStatus
		want    resourcev1.ResourceStatus
	}{
		{name: "one healthy region", regions: []resourcev1.ResourceStatus{healthy}, want: healthy},
		{name: "every region healthy", regions: []resourcev1.ResourceStatus{healthy, healthy}, want: healthy},
		{name: "every region down", regions: []resourcev1.ResourceStatus{unavailable, unavailable}, want: unavailable},
		{name: "every region deploying", regions: []resourcev1.ResourceStatus{deploying, deploying}, want: deploying},
		{name: "every region unknown", regions: []resourcev1.ResourceStatus{unknown, unknown}, want: unknown},
		{name: "every region degraded", regions: []resourcev1.ResourceStatus{degraded, degraded}, want: degraded},
		{name: "one region down", regions: []resourcev1.ResourceStatus{healthy, unavailable}, want: degraded},
		{name: "one region short", regions: []resourcev1.ResourceStatus{healthy, degraded}, want: degraded},
		{name: "new region rolling out", regions: []resourcev1.ResourceStatus{healthy, deploying}, want: deploying},
		{name: "one region silent", regions: []resourcev1.ResourceStatus{healthy, unknown}, want: unknown},
		{name: "silent and down", regions: []resourcev1.ResourceStatus{unknown, unavailable}, want: degraded},
		{name: "rolling out and down", regions: []resourcev1.ResourceStatus{deploying, unavailable}, want: degraded},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			regions := make([]*resourcev1.RegionStatus, len(tt.regions))
			for i, status := range tt.regions {
				regions[i] = &resourcev1.RegionStatus{Region: testRegion, Status: status}
			}
			got, err := summarizeRegions(regions)
			if err != nil {
				t.Fatalf("summarizeRegions: %v", err)
			}
			if got != tt.want {
				t.Errorf("status = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestSummarizeRegionsRejectsEmptyAndUnset(t *testing.T) {
	t.Parallel()
	if _, err := summarizeRegions(nil); !errors.Is(err, errNoRegions) {
		t.Errorf("no regions: err = %v, want %v", err, errNoRegions)
	}
	unset := []*resourcev1.RegionStatus{{Region: testRegion}}
	if _, err := summarizeRegions(unset); !errors.Is(err, errRegionStatusUnset) {
		t.Errorf("unset region: err = %v, want %v", err, errRegionStatusUnset)
	}
}

func TestEnvironmentSince(t *testing.T) {
	t.Parallel()
	healthy := resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY
	degraded := resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED
	unavailable := resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE
	deploying := resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	at := func(minutes int) *timestamppb.Timestamp {
		return timestamppb.New(base.Add(time.Duration(minutes) * time.Minute))
	}

	tests := []struct {
		name    string
		regions []*resourcev1.RegionStatus
		status  resourcev1.ResourceStatus
		want    *timestamppb.Timestamp
	}{
		{
			name: "degraded since the first failing region",
			regions: []*resourcev1.RegionStatus{
				{Status: degraded, StatusSince: at(5)},
				{Status: unavailable, StatusSince: at(2)},
			},
			status: degraded,
			want:   at(2),
		},
		{
			name: "one failing region among healthy ones",
			regions: []*resourcev1.RegionStatus{
				{Status: healthy, StatusSince: at(1)},
				{Status: unavailable, StatusSince: at(7)},
			},
			status: degraded,
			want:   at(7),
		},
		{
			name: "healthy since the last region became healthy",
			regions: []*resourcev1.RegionStatus{
				{Status: healthy, StatusSince: at(3)},
				{Status: healthy, StatusSince: at(9)},
			},
			status: healthy,
			want:   at(9),
		},
		{
			name: "deploying ignores the healthy region",
			regions: []*resourcev1.RegionStatus{
				{Status: healthy, StatusSince: at(1)},
				{Status: deploying, StatusSince: at(4)},
			},
			status: deploying,
			want:   at(4),
		},
		{
			name:    "no region reported",
			regions: []*resourcev1.RegionStatus{{Status: healthy}},
			status:  healthy, want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := environmentSince(tt.regions, tt.status); !proto.Equal(got, tt.want) {
				t.Errorf("environmentSince = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestServingDeployment(t *testing.T) {
	t.Parallel()
	older, newer := uuid.Must(uuid.NewV7()).String(), uuid.Must(uuid.NewV7()).String()
	live := uuid.Must(uuid.NewV7()).String()
	replicas := func(ready map[string]int, notReady string) []*deploymentv1.Replica {
		var list []*deploymentv1.Replica
		for id, count := range ready {
			for range count {
				list = append(list, &deploymentv1.Replica{DeploymentId: id, Ready: true})
			}
		}
		return append(list, &deploymentv1.Replica{DeploymentId: notReady})
	}
	tests := []struct {
		name     string
		replicas []*deploymentv1.Replica
		serving  string
	}{
		{name: "no ready replica", replicas: replicas(nil, live), serving: ""},
		{name: "previous version serves a failed rollout", replicas: replicas(map[string]int{older: 2}, live),
			serving: older},
		{name: "most ready replicas", replicas: replicas(map[string]int{older: 2, live: 1}, live), serving: older},
		{name: "live deployment on a tie", replicas: replicas(map[string]int{newer: 1, live: 1}, live), serving: live},
		{name: "newer deployment on a tie without the live one",
			replicas: replicas(map[string]int{older: 1, newer: 1}, live), serving: newer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if serving := servingDeployment(tt.replicas, live); serving != tt.serving {
				t.Errorf("servingDeployment = %q, want %q", serving, tt.serving)
			}
		})
	}
}
