import type { Activity, Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { DeploymentStatus } from "@gen/loco/deployment/v1/deployment_pb";

export function mergeActivity(previous: Activity[], incoming: Activity[]): Activity[] {
	return [...new Map([...previous, ...incoming].map((entry) => [entry.seq, entry])).values()].toSorted((a, b) =>
		a.seq < b.seq ? -1 : a.seq > b.seq ? 1 : 0,
	);
}

export function deploymentEnded(deployment: Deployment): boolean {
	return deployment.status === DeploymentStatus.CANCELED || deployment.status === DeploymentStatus.SUPERSEDED;
}

export function deploymentMessage(deployment: Deployment): string {
	return [deployment.problem?.message, deployment.problem?.detail].filter(Boolean).join(" · ");
}
