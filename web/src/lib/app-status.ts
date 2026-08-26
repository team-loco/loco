import { DeploymentPhase } from "@gen/loco/deployment/v1/deployment_pb";
import { ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

export function getStatusLabel(status?: ResourceStatus): string {
	if (status === undefined) return "pending";

	switch (status) {
		case ResourceStatus.HEALTHY:
			return "running";
		case ResourceStatus.DEPLOYING:
			return "deploying";
		case ResourceStatus.DEGRADED:
			return "degraded";
		case ResourceStatus.UNAVAILABLE:
			return "unavailable";
		case ResourceStatus.SUSPENDED:
			return "suspended";
		default:
			return "pending";
	}
}

export function getDeploymentPhaseLabel(phase?: DeploymentPhase): string {
	if (phase === undefined) return "pending";

	switch (phase) {
		case DeploymentPhase.PENDING:
			return "pending";
		case DeploymentPhase.RUNNING:
			return "running";
		case DeploymentPhase.SUCCEEDED:
			return "succeeded";
		case DeploymentPhase.FAILED:
			return "failed";
		case DeploymentPhase.CANCELED:
			return "canceled";
		default:
			return "pending";
	}
}
