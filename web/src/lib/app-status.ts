import { ResourceStatus, DeploymentPhase } from "@/gen/resource/v1/resource_pb";

export function getStatusLabel(status?: number): string {
	if (status === undefined || status === null) return "pending";
	switch (status) {
		case ResourceStatus.AVAILABLE:
			return "running";
		case ResourceStatus.PROGRESSING:
			return "deploying";
		case ResourceStatus.DEGRADED:
			return "degraded";
		case ResourceStatus.UNAVAILABLE:
			return "unavailable";
		default:
			return "pending";
	}
}

export function getDeploymentPhaseLabel(phase?: number): string {
	if (phase === undefined || phase === null) return "pending";
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
