import type { Activity, Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { DeploymentStatus } from "@gen/loco/deployment/v1/deployment_pb";

import { deploymentProblem } from "@/lib/deployment-utils";

export function mergeActivity(previous: Activity[], incoming: Activity[]): Activity[] {
	return [...new Map([...previous, ...incoming].map((entry) => [entry.seq, entry])).values()].toSorted((a, b) =>
		a.seq < b.seq ? -1 : a.seq > b.seq ? 1 : 0,
	);
}

export function deploymentEnded(deployment: Deployment): boolean {
	return deployment.status === DeploymentStatus.CANCELED || deployment.status === DeploymentStatus.SUPERSEDED;
}

/** The deployment's status and its placements' statuses, which change the resource's region statuses and history rows. */
export function statusKey(deployment: Deployment): string {
	return [deployment.status, ...deployment.placements.map((p) => `${p.region}=${p.status.toString()}`)].join(",");
}

export function deploymentMessage(deployment: Deployment): string {
	const problem = deploymentProblem(deployment);
	return [problem?.message, problem?.detail].filter(Boolean).join(", ");
}
