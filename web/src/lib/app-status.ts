import { AppStatus, DeploymentPhase } from "@/gen/app/v1/app_pb";

export function getStatusLabel(status?: number): string {
	if (status === undefined || status === null) return "pending";
	switch (status) {
		case AppStatus.AVAILABLE:
			return "running";
		case AppStatus.PROGRESSING:
			return "deploying";
		case AppStatus.DEGRADED:
			return "degraded";
		case AppStatus.UNAVAILABLE:
			return "unavailable";
		case AppStatus.IDLE:
			return "idle";
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
