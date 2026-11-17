package service

import (
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
)

func TestNextDeploymentStatus(t *testing.T) {
	for _, tt := range []struct {
		name                string
		previous, want      genDb.DeploymentStatus
		available, deadline bool
	}{
		{"deploying waits", genDb.DeploymentStatusDeploying, genDb.DeploymentStatusDeploying, false, false},
		{"available", genDb.DeploymentStatusDeploying, genDb.DeploymentStatusRunning, true, false},
		{"deadline", genDb.DeploymentStatusDeploying, genDb.DeploymentStatusFailed, false, true},
		{"failed retries", genDb.DeploymentStatusFailed, genDb.DeploymentStatusFailed, false, false},
		{"failed recovers", genDb.DeploymentStatusFailed, genDb.DeploymentStatusRunning, true, false},
		{"availability wins", genDb.DeploymentStatusFailed, genDb.DeploymentStatusRunning, true, true},
		{"running unhealthy", genDb.DeploymentStatusRunning, genDb.DeploymentStatusRunning, false, false},
		{"restart waits", genDb.DeploymentStatusDeploying, genDb.DeploymentStatusDeploying, false, false},
		{"restart ready", genDb.DeploymentStatusDeploying, genDb.DeploymentStatusRunning, true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got := nextDeploymentStatus(
				tt.previous,
				&agentv1.PlacementStatus{Available: tt.available, ProgressDeadlineExceeded: tt.deadline},
			)
			if got != tt.want {
				t.Fatalf("status=%s want %s", got, tt.want)
			}
		})
	}
}

func TestDeploymentPhaseMapping(t *testing.T) {
	for _, tt := range []struct {
		status   genDb.DeploymentStatus
		phase    deploymentv1.DeploymentPhase
		terminal bool
	}{
		{genDb.DeploymentStatusBuilding, deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_PENDING, false},
		{genDb.DeploymentStatusDeploying, deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_DEPLOYING, false},
		{genDb.DeploymentStatusRunning, deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_RUNNING, false},
		{genDb.DeploymentStatusFailed, deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_FAILED, true},
		{genDb.DeploymentStatusCanceled, deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_CANCELED, true},
		{genDb.DeploymentStatusSuperseded, deploymentv1.DeploymentPhase_DEPLOYMENT_PHASE_SUCCEEDED, true},
	} {
		if parseDeploymentPhase(tt.status) != tt.phase || isTerminalDeploymentStatus(tt.status) != tt.terminal {
			t.Fatalf("mapping %s", tt.status)
		}
	}
}
