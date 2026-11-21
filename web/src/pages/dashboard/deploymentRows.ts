import { DeploymentStatus, type Deployment, type DeploymentStatusCount } from "@gen/loco/deployment/v1/deployment_pb";

import { DAY_MS } from "@/lib/time";

export const DEPLOYMENT_WINDOW_MS = DAY_MS;
export const DEPLOYMENT_LIMIT = 10;
export const ALL_DEPLOYMENTS = "all";

export type DeploymentState = "running" | "progress" | "failed" | "superseded" | "other";

export const DEPLOYMENT_FILTERS: {
	value: Exclude<DeploymentState, "superseded" | "other">;
	label: string;
	statuses: DeploymentStatus[];
}[] = [
	{ value: "running", label: "Running", statuses: [DeploymentStatus.RUNNING] },
	{ value: "progress", label: "In progress", statuses: [DeploymentStatus.BUILDING, DeploymentStatus.DEPLOYING] },
	{
		value: "failed",
		label: "Failed",
		statuses: [DeploymentStatus.FAILED, DeploymentStatus.DEGRADED, DeploymentStatus.CRASHED],
	},
];

function stateOf(status: DeploymentStatus): DeploymentState {
	if (status === DeploymentStatus.SUPERSEDED) return "superseded";
	return DEPLOYMENT_FILTERS.find((f) => f.statuses.includes(status))?.value ?? "other";
}

export function deploymentState(d: Deployment): DeploymentState {
	return stateOf(d.status);
}

/** The statuses a filter lists; empty for all of them. */
export function filterStatuses(filter: string): DeploymentStatus[] {
	return DEPLOYMENT_FILTERS.find((f) => f.value === filter)?.statuses ?? [];
}

export function deploymentCounts(counts: readonly DeploymentStatusCount[]): Map<DeploymentState, number> {
	const states = new Map<DeploymentState, number>();
	for (const c of counts) {
		const state = stateOf(c.status);
		states.set(state, (states.get(state) ?? 0) + c.count);
	}
	return states;
}
