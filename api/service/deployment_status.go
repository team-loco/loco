package service

import (
	genDb "github.com/team-loco/loco/api/gen/db"
	agentv1 "github.com/team-loco/loco/gen/go/loco/agent/v1"
)

func nextDeploymentStatus(previous genDb.DeploymentStatus, report *agentv1.PlacementStatus) genDb.DeploymentStatus {
	switch previous {
	case genDb.DeploymentStatusCanceled, genDb.DeploymentStatusSuperseded:
		return previous
	}
	if report.GetAvailable() {
		return genDb.DeploymentStatusRunning
	}
	if report.GetProgressDeadlineExceeded() {
		return genDb.DeploymentStatusFailed
	}
	return previous
}
