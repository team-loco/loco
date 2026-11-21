package service

import (
	"testing"

	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
	deploymentv1 "github.com/team-loco/loco/gen/go/loco/deployment/v1"
	loco "github.com/team-loco/loco/k8sapi/v1alpha1"
)

// deployTimedOut is the stop of a revision that missed its deploy timeout with nothing to fall back to.
func deployTimedOut() *agentv1.StoppedRevision {
	return &agentv1.StoppedRevision{PlacementRevision: 2}
}

func TestNextPlacementStatus(t *testing.T) {
	stopped := &agentv1.StoppedRevision{PlacementRevision: 2, Restarts: 6}
	unproven := &agentv1.StoppedRevision{PlacementRevision: 2, Restarts: 6, Rollout: true}
	for _, tt := range []struct {
		name           string
		previous, want genDb.PlacementStatus
		report         *agentv1.PlacementStatus
	}{
		{"deploying waits", genDb.PlacementStatusDeploying, genDb.PlacementStatusDeploying,
			&agentv1.PlacementStatus{}},
		{"available but restarted waits", genDb.PlacementStatusDeploying, genDb.PlacementStatusDeploying,
			&agentv1.PlacementStatus{Available: true}},
		{"healthy", genDb.PlacementStatusDeploying, genDb.PlacementStatusRunning,
			&agentv1.PlacementStatus{Available: true, Healthy: true}},
		{"deploy timeout", genDb.PlacementStatusDeploying, genDb.PlacementStatusFailed,
			&agentv1.PlacementStatus{Stopped: deployTimedOut()}},
		{"failed retries", genDb.PlacementStatusFailed, genDb.PlacementStatusFailed, &agentv1.PlacementStatus{}},
		{"failed recovers", genDb.PlacementStatusFailed, genDb.PlacementStatusRunning,
			&agentv1.PlacementStatus{Available: true, Healthy: true}},
		{"a deploy timeout wins over health", genDb.PlacementStatusDeploying, genDb.PlacementStatusFailed,
			&agentv1.PlacementStatus{Available: true, Healthy: true, Stopped: deployTimedOut()}},
		{"running restarted", genDb.PlacementStatusRunning, genDb.PlacementStatusDegraded,
			&agentv1.PlacementStatus{Available: true}},
		{"running unready", genDb.PlacementStatusRunning, genDb.PlacementStatusDegraded, &agentv1.PlacementStatus{}},
		{"degraded recovers", genDb.PlacementStatusDegraded, genDb.PlacementStatusRunning,
			&agentv1.PlacementStatus{Available: true, Healthy: true}},
		{"stopped rollout", genDb.PlacementStatusDeploying, genDb.PlacementStatusFailed,
			&agentv1.PlacementStatus{Stopped: unproven}},
		{"stopped before it proved itself", genDb.PlacementStatusRunning, genDb.PlacementStatusFailed,
			&agentv1.PlacementStatus{Stopped: unproven}},
		{"stopped while running", genDb.PlacementStatusRunning, genDb.PlacementStatusCrashed,
			&agentv1.PlacementStatus{Stopped: stopped}},
		{"stopped while degraded", genDb.PlacementStatusDegraded, genDb.PlacementStatusCrashed,
			&agentv1.PlacementStatus{Stopped: stopped}},
		{"crashed stays", genDb.PlacementStatusCrashed, genDb.PlacementStatusCrashed,
			&agentv1.PlacementStatus{Stopped: stopped}},
		{"restarts used up after deploying", genDb.PlacementStatusRunning, genDb.PlacementStatusCrashed,
			&agentv1.PlacementStatus{Available: true, Problem: &agentv1.ProblemReport{
				Reason: string(loco.ProblemRestartsExhausted),
			}}},
		{"a deployed revision recovers from used-up restarts", genDb.PlacementStatusCrashed,
			genDb.PlacementStatusRunning, &agentv1.PlacementStatus{Available: true, Healthy: true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := nextPlacementStatus(tt.previous, tt.report); got != tt.want {
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
