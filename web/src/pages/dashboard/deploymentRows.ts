import { DeploymentStatus, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { DAY_MS, tsMs } from "@/lib/time";

export const DEPLOYMENT_WINDOW_MS = DAY_MS;
export const DEPLOYMENT_LIMIT = 10;
export const ALL_DEPLOYMENTS = "all";

export type DeploymentState = "running" | "progress" | "failed" | "superseded" | "other";

export const DEPLOYMENT_FILTERS: { value: DeploymentState; label: string }[] = [
	{ value: "running", label: "Running" },
	{ value: "progress", label: "In progress" },
	{ value: "failed", label: "Failed" },
];

export function deploymentState(d: Deployment): DeploymentState {
	switch (d.status) {
		case DeploymentStatus.RUNNING:
			return "running";
		case DeploymentStatus.SUPERSEDED:
			return "superseded";
		case DeploymentStatus.BUILDING:
			return "progress";
		case DeploymentStatus.DEPLOYING:
			return "progress";
		case DeploymentStatus.FAILED:
			return "failed";
		case DeploymentStatus.CANCELED:
			return "other";
		case DeploymentStatus.UNSPECIFIED:
			return "other";
	}
}

export function withinWindow<T extends { deployment: Deployment }>(rows: T[], nowMs: number): T[] {
	return rows.filter((r) => nowMs - tsMs(r.deployment.createdAt) <= DEPLOYMENT_WINDOW_MS);
}

export function deploymentCounts(rows: { deployment: Deployment }[]): Map<DeploymentState, number> {
	const counts = new Map<DeploymentState, number>();
	for (const r of rows) {
		const state = deploymentState(r.deployment);
		counts.set(state, (counts.get(state) ?? 0) + 1);
	}
	return counts;
}

export function filterDeployments<T extends { deployment: Deployment }>(rows: T[], filter: string): T[] {
	const matching = filter === ALL_DEPLOYMENTS ? rows : rows.filter((r) => deploymentState(r.deployment) === filter);
	return matching.slice(0, DEPLOYMENT_LIMIT);
}
