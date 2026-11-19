package service

import (
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

func TestNextPlacementStatus(t *testing.T) {
	for _, tt := range []struct {
		name                string
		previous, want      genDb.PlacementStatus
		available, deadline bool
	}{
		{"deploying waits", genDb.PlacementStatusDeploying, genDb.PlacementStatusDeploying, false, false},
		{"available", genDb.PlacementStatusDeploying, genDb.PlacementStatusRunning, true, false},
		{"deadline", genDb.PlacementStatusDeploying, genDb.PlacementStatusFailed, false, true},
		{"failed retries", genDb.PlacementStatusFailed, genDb.PlacementStatusFailed, false, false},
		{"failed recovers", genDb.PlacementStatusFailed, genDb.PlacementStatusRunning, true, false},
		{"availability wins", genDb.PlacementStatusFailed, genDb.PlacementStatusRunning, true, true},
		{"running unhealthy", genDb.PlacementStatusRunning, genDb.PlacementStatusRunning, false, false},
		{"running past the deadline", genDb.PlacementStatusRunning, genDb.PlacementStatusFailed, false, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := nextPlacementStatus(
				tt.previous,
				&agentv1.PlacementStatus{Available: tt.available, ProgressDeadlineExceeded: tt.deadline},
			)
			if got != tt.want {
				t.Fatalf("status=%s want %s", got, tt.want)
			}
		})
	}
}

func TestDeploymentStatusMapping(t *testing.T) {
	for _, tt := range []struct {
		status   genDb.DeploymentStatus
		phase    deploymentv1.DeploymentStatus
		terminal bool
	}{
		{genDb.DeploymentStatusBuilding, deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_BUILDING, false},
		{genDb.DeploymentStatusDeploying, deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_DEPLOYING, false},
		{genDb.DeploymentStatusRunning, deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_RUNNING, false},
		{genDb.DeploymentStatusDegraded, deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_DEGRADED, false},
		{genDb.DeploymentStatusCrashed, deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_CRASHED, false},
		{genDb.DeploymentStatusFailed, deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_FAILED, true},
		{genDb.DeploymentStatusCanceled, deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_CANCELED, true},
		{genDb.DeploymentStatusSuperseded, deploymentv1.DeploymentStatus_DEPLOYMENT_STATUS_SUPERSEDED, true},
	} {
		if parseDeploymentStatus(tt.status) != tt.phase || isTerminalDeploymentStatus(tt.phase) != tt.terminal {
			t.Fatalf("mapping %s", tt.status)
		}
	}
}
