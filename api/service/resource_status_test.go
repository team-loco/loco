package service

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	genDb "github.com/team-loco/loco/api/gen/db"
	resourcev1 "github.com/team-loco/loco/gen/go/loco/resource/v1"
)

const (
	testStatusStaleAfter = time.Minute
	testFreshHeartbeat   = 10 * time.Second
	testStaleHeartbeat   = 2 * time.Minute
	testDesiredReplicas  = 2
	testStatusMessage    = "Waiting for pods to be ready..."
	testFailureMessage   = "container exited with code 1"
)

var testStatusNow = time.Date(2026, time.October, 11, 12, 0, 0, 0, time.UTC)

func regionRow(status genDb.DeploymentStatus, ready int32) genDb.ListRegionStatusesForResourcesRow {
	heartbeat := testStatusNow.Add(-testFreshHeartbeat)
	reported := testStatusNow.Add(-testFreshHeartbeat)
	health := clusterHealthHealthy
	return genDb.ListRegionStatusesForResourcesRow{
		ResourceID:       uuid.New(),
		EnvironmentID:    uuid.New(),
		EnvironmentName:  "production",
		Region:           testRegion,
		DeploymentID:     uuid.New(),
		DeploymentStatus: status,
		DesiredReplicas:  testDesiredReplicas,
		Placed:           true,
		ReadyReplicas:    ready,
		StatusMessage:    testStatusMessage,
		StatusUpdatedAt:  &reported,
		LastHeartbeat:    &heartbeat,
		HealthStatus:     &health,
	}
}

func TestRegionStatus(t *testing.T) {
	staleHeartbeat := testStatusNow.Add(-testStaleHeartbeat)
	edgeHeartbeat := testStatusNow.Add(-testStatusStaleAfter)
	unhealthy := clusterHealthUnhealthy
	degradedCluster := clusterHealthDegraded

	tests := []struct {
		name    string
		row     func() genDb.ListRegionStatusesForResourcesRow
		want    resourcev1.ResourceStatus
		message string
	}{
		{
			name: "every desired replica ready",
			row:  func() genDb.ListRegionStatusesForResourcesRow { return regionRow(genDb.DeploymentStatusRunning, 2) },
			want: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		},
		{
			name: "surge replicas during a rollout",
			row:  func() genDb.ListRegionStatusesForResourcesRow { return regionRow(genDb.DeploymentStatusDeploying, 3) },
			want: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		},
		{
			name:    "running deployment whose pods all crashed",
			row:     func() genDb.ListRegionStatusesForResourcesRow { return regionRow(genDb.DeploymentStatusRunning, 0) },
			want:    resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE,
			message: testStatusMessage,
		},
		{
			name:    "running deployment that lost a replica",
			row:     func() genDb.ListRegionStatusesForResourcesRow { return regionRow(genDb.DeploymentStatusRunning, 1) },
			want:    resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED,
			message: testStatusMessage,
		},
		{
			name: "first rollout before any replica is ready",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				return regionRow(genDb.DeploymentStatusDeploying, 0)
			},
			want:    resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING,
			message: testStatusMessage,
		},
		{
			name: "pending deployment the agent has not reported on",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.DeploymentStatusPending, 0)
				row.StatusUpdatedAt = nil
				row.StatusMessage = ""
				return row
			},
			want: resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING,
		},
		{
			name: "redeploy while the previous replicas still serve",
			row:  func() genDb.ListRegionStatusesForResourcesRow { return regionRow(genDb.DeploymentStatusDeploying, 2) },
			want: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		},
		{
			name: "failed redeploy while the previous replicas still serve",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.DeploymentStatusFailed, 2)
				row.DeploymentMessage = testFailureMessage
				return row
			},
			want:    resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
			message: testFailureMessage,
		},
		{
			name: "failed deployment with nothing serving",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.DeploymentStatusFailed, 0)
				row.DeploymentMessage = testFailureMessage
				return row
			},
			want:    resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE,
			message: testFailureMessage,
		},
		{
			name: "healthy report from a cluster that stopped heartbeating",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.DeploymentStatusRunning, 2)
				row.LastHeartbeat = &staleHeartbeat
				return row
			},
			want:    resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN,
			message: regionMessageClusterSilent,
		},
		{
			name: "heartbeat exactly at the threshold",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.DeploymentStatusRunning, 2)
				row.LastHeartbeat = &edgeHeartbeat
				return row
			},
			want: resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY,
		},
		{
			name: "cluster that never heartbeated",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.DeploymentStatusRunning, 2)
				row.LastHeartbeat = nil
				return row
			},
			want:    resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN,
			message: regionMessageClusterNeverHeard,
		},
		{
			name: "cluster whose controller is down",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.DeploymentStatusRunning, 2)
				row.HealthStatus = &degradedCluster
				return row
			},
			want:    resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN,
			message: regionMessageClusterUnhealthy,
		},
		{
			name: "unhealthy cluster",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.DeploymentStatusRunning, 0)
				row.HealthStatus = &unhealthy
				return row
			},
			want:    resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN,
			message: regionMessageClusterUnhealthy,
		},
		{
			name: "cluster without a health status",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.DeploymentStatusRunning, 2)
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
			got, err := regionStatus(row, testStatusNow, testStatusStaleAfter)
			if err != nil {
				t.Fatalf("regionStatus: %v", err)
			}
			if got.GetStatus() != tt.want {
				t.Errorf("status = %s, want %s", got.GetStatus(), tt.want)
			}
			if got.GetMessage() != tt.message {
				t.Errorf("message = %q, want %q", got.GetMessage(), tt.message)
			}
			if got.GetDesiredReplicas() != row.DesiredReplicas || got.GetReadyReplicas() != row.ReadyReplicas {
				t.Errorf("replicas = %d/%d, want %d/%d",
					got.GetReadyReplicas(), got.GetDesiredReplicas(), row.ReadyReplicas, row.DesiredReplicas)
			}
			if got.GetDeploymentId() != row.DeploymentID.String() {
				t.Errorf("deployment id = %q, want %q", got.GetDeploymentId(), row.DeploymentID)
			}
		})
	}
}

func TestRegionStatusRejectsBrokenInvariants(t *testing.T) {
	tests := []struct {
		name string
		row  genDb.ListRegionStatusesForResourcesRow
		want error
	}{
		{
			name: "succeeded deployment still active",
			row:  regionRow(genDb.DeploymentStatusSucceeded, 2),
			want: errFinishedDeploymentActive,
		},
		{
			name: "canceled deployment still active",
			row:  regionRow(genDb.DeploymentStatusCanceled, 0),
			want: errFinishedDeploymentActive,
		},
		{
			name: "unknown deployment status",
			row:  regionRow(genDb.DeploymentStatus("exploded"), 0),
			want: errUnknownDeploymentStatus,
		},
		{
			name: "deployment with no desired replicas",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.DeploymentStatusRunning, 0)
				row.DesiredReplicas = 0
				return row
			}(),
			want: errNoDesiredReplicas,
		},
		{
			name: "active deployment without a placement",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.DeploymentStatusRunning, 0)
				row.Placed = false
				return row
			}(),
			want: errActiveDeploymentUnplaced,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := regionStatus(tt.row, testStatusNow, testStatusStaleAfter)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestRegionStatusNeverOverstatesHealth(t *testing.T) {
	deploymentStatuses := []genDb.DeploymentStatus{
		genDb.DeploymentStatusPending,
		genDb.DeploymentStatusDeploying,
		genDb.DeploymentStatusRunning,
		genDb.DeploymentStatusFailed,
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
					got, err := regionStatus(row, testStatusNow, testStatusStaleAfter)
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
		testStatusNow.Sub(*row.LastHeartbeat) <= testStatusStaleAfter &&
		row.HealthStatus != nil && *row.HealthStatus == clusterHealthHealthy
	inFlight := row.DeploymentStatus == genDb.DeploymentStatusPending ||
		row.DeploymentStatus == genDb.DeploymentStatusDeploying
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
			row.DeploymentStatus, row.LastHeartbeat, row.HealthStatus,
			row.ReadyReplicas, row.DesiredReplicas, status, want)
	}
}

func TestSummarizeRegions(t *testing.T) {
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
	if _, err := summarizeRegions(nil); !errors.Is(err, errNoRegions) {
		t.Errorf("no regions: err = %v, want %v", err, errNoRegions)
	}
	unset := []*resourcev1.RegionStatus{{Region: testRegion}}
	if _, err := summarizeRegions(unset); !errors.Is(err, errRegionStatusUnset) {
		t.Errorf("unset region: err = %v, want %v", err, errRegionStatusUnset)
	}
}
