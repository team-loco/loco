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

func regionRow(
	status genDb.PlacementStatus,
	ready int32,
	regionStatus resourcev1.ResourceStatus,
) genDb.ListRegionStatusesForResourcesRow {
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
		Status:          regionStatus.String(),
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
	healthy := resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY
	degraded := resourcev1.ResourceStatus_RESOURCE_STATUS_DEGRADED
	deploying := resourcev1.ResourceStatus_RESOURCE_STATUS_DEPLOYING
	unavailable := resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE
	unknown := resourcev1.ResourceStatus_RESOURCE_STATUS_UNKNOWN
	clusterCause := func(cause string) func() genDb.ListRegionStatusesForResourcesRow {
		return func() genDb.ListRegionStatusesForResourcesRow {
			row := regionRow(genDb.PlacementStatusRunning, 2, unknown)
			row.StatusCause = cause
			return row
		}
	}

	tests := []struct {
		name    string
		row     func() genDb.ListRegionStatusesForResourcesRow
		want    resourcev1.ResourceStatus
		message string
	}{
		{
			name: "source build before placement",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.PlacementStatusPending, 0, deploying)
				row.Placed = false
				row.StatusUpdatedAt = nil
				row.Problem = []byte(`null`)
				return row
			},
			want: deploying,
		},
		{
			name: "healthy region keeps its replicas' problem to itself",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				return regionRow(genDb.PlacementStatusRunning, 2, healthy)
			},
			want: healthy,
		},
		{
			name: "surge replicas during a rollout",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				return regionRow(genDb.PlacementStatusDeploying, 3, healthy)
			},
			want: healthy,
		},
		{
			name: "running deployment whose pods all crashed",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				return regionRow(genDb.PlacementStatusRunning, 0, unavailable)
			},
			want:    unavailable,
			message: testStatusMessage,
		},
		{
			name: "running deployment that lost a replica",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				return regionRow(genDb.PlacementStatusRunning, 1, degraded)
			},
			want:    degraded,
			message: testStatusMessage,
		},
		{
			name: "every replica ready but one restarted within the health window",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				return regionRow(genDb.PlacementStatusDegraded, 2, degraded)
			},
			want:    degraded,
			message: testStatusMessage,
		},
		{
			name: "service stopped after too many restarts",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				return regionRow(genDb.PlacementStatusCrashed, 0, unavailable)
			},
			want:    unavailable,
			message: testStatusMessage,
		},
		{
			name: "first rollout before any replica is ready",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				return regionRow(genDb.PlacementStatusDeploying, 0, deploying)
			},
			want:    deploying,
			message: testStatusMessage,
		},
		{
			name: "pending deployment the agent has not reported on",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.PlacementStatusDeploying, 0, deploying)
				row.StatusUpdatedAt = nil
				row.Problem = []byte(`null`)
				return row
			},
			want: deploying,
		},
		{
			name: "redeploy while the previous replicas still serve",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.PlacementStatusDeploying, 2, healthy)
				row.Replicas = replicasJSON(uuid.New(), 2)
				return row
			},
			want: healthy,
		},
		{
			name: "failed redeploy while the previous replicas still serve",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.PlacementStatusFailed, 2, healthy)
				row.Replicas = replicasJSON(uuid.New(), 2)
				row.Problem = []byte(
					`{"reason":"deploy_timeout","detail":"` + testFailureMessage + `"}`,
				)
				return row
			},
			want: healthy,
			message: "The new version did not become healthy within its deploy timeout, so Loco stopped it. " +
				"Check its replicas and logs, then redeploy. " + testFailureMessage,
		},
		{
			name: "failed deployment with nothing serving",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.PlacementStatusFailed, 0, unavailable)
				row.Problem = []byte(
					`{"reason":"deploy_timeout","detail":"` + testFailureMessage + `"}`,
				)
				return row
			},
			want: unavailable,
			message: "The new version did not become healthy within its deploy timeout, so Loco stopped it. " +
				"Check its replicas and logs, then redeploy. " + testFailureMessage,
		},
		{
			name:    "cluster that stopped heartbeating",
			row:     clusterCause(statusCauseClusterSilent),
			want:    unknown,
			message: regionMessageClusterSilent,
		},
		{
			name: "cluster that never heartbeated",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := clusterCause(statusCauseClusterNeverReported)()
				row.LastHeartbeat = nil
				return row
			},
			want:    unknown,
			message: regionMessageClusterNeverHeard,
		},
		{
			name:    "unhealthy cluster",
			row:     clusterCause(statusCauseClusterUnhealthy),
			want:    unknown,
			message: regionMessageClusterUnhealthy,
		},
		{
			name: "cluster that does not serve the platform domains",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.PlacementStatusRunning, 2, degraded)
				row.StatusCause = statusCauseDomainsUnserved
				row.UnservedDomains = []string{"a.example.com", "b.example.com"}
				return row
			},
			want:    degraded,
			message: regionMessageDomainsUnserved + "a.example.com, b.example.com",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			row := tt.row()
			got, err := regionStatus(row)
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
			if tt.want == unknown {
				var since *timestamppb.Timestamp
				if row.LastHeartbeat != nil {
					since = timestamppb.New(*row.LastHeartbeat)
				}
				if !proto.Equal(got.GetStatusSince(), since) {
					t.Errorf("status since = %v, want the last heartbeat %v", got.GetStatusSince(), since)
				}
			}
		})
	}
}

func TestRegionStatusRejectsBrokenInvariants(t *testing.T) {
	t.Parallel()
	healthy := resourcev1.ResourceStatus_RESOURCE_STATUS_HEALTHY
	unavailable := resourcev1.ResourceStatus_RESOURCE_STATUS_UNAVAILABLE
	tests := []struct {
		name string
		row  genDb.ListRegionStatusesForResourcesRow
		want error
	}{
		{
			name: "succeeded deployment still active",
			row:  regionRow(genDb.PlacementStatusSuperseded, 2, healthy),
			want: errFinishedDeploymentActive,
		},
		{
			name: "canceled deployment still active",
			row:  regionRow(genDb.PlacementStatusCanceled, 0, unavailable),
			want: errFinishedDeploymentActive,
		},
		{
			name: "unknown deployment status",
			row:  regionRow(genDb.PlacementStatus("exploded"), 0, unavailable),
			want: errUnknownDeploymentStatus,
		},
		{
			name: "deployment with no desired replicas",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.PlacementStatusRunning, 0, unavailable)
				row.DesiredReplicas = 0
				return row
			}(),
			want: errNoDesiredReplicas,
		},
		{
			name: "active deployment without a placement",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.PlacementStatusRunning, 0, unavailable)
				row.Placed = false
				return row
			}(),
			want: errActiveDeploymentUnplaced,
		},
		{
			name: "unset region status",
			row:  regionRow(genDb.PlacementStatusRunning, 2, resourcev1.ResourceStatus_RESOURCE_STATUS_UNSPECIFIED),
			want: errUnknownResourceStatus,
		},
		{
			name: "unrecognized region status",
			row: func() genDb.ListRegionStatusesForResourcesRow {
				row := regionRow(genDb.PlacementStatusRunning, 2, healthy)
				row.Status = ""
				return row
			}(),
			want: errUnknownResourceStatus,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := regionStatus(tt.row)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err = %v, want %v", err, tt.want)
			}
		})
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
