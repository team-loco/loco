import { PlacementStatus, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { getServiceSpec, placementIn, regionTarget } from "@/lib/deployment-utils";
import { maybeTsMs } from "@/lib/time";

/** A limit in force from `from` until the next step. */
export interface LimitStep {
	from: number;
	limit: number;
}

/** The sizing fields a deployment or resource spec gives a region. */
export interface Sizing {
	cpu?: string | undefined;
	memory?: string | undefined;
}

const NOT_RUN = new Set([PlacementStatus.PENDING, PlacementStatus.FAILED, PlacementStatus.CANCELED]);

/** When a deployment's pods started in a region; undefined if they never ran there. */
export function startedIn(d: Deployment, region: string): number | undefined {
	const placement = placementIn(d, region);
	if (placement === undefined || NOT_RUN.has(placement.status)) return undefined;
	return maybeTsMs(placement.startedAt) ?? maybeTsMs(d.startedAt);
}

/**
 * The limit each deployment set in a region, from when it started there, oldest first.
 * Without any started deployment, the current target's limit holds for all time.
 */
export function limitSteps(
	deployments: readonly Deployment[],
	region: string,
	size: (target: Sizing) => number,
	current: Sizing | undefined,
): LimitStep[] {
	const steps = new Map<string, LimitStep>();
	for (const d of deployments) {
		const from = startedIn(d, region);
		const target = regionTarget(getServiceSpec(d), region);
		const limit = target === undefined ? 0 : size(target);
		if (from !== undefined && limit > 0) steps.set(d.id, { from, limit });
	}
	if (steps.size > 0) return [...steps.values()].toSorted((a, b) => a.from - b.from);
	const limit = current === undefined ? 0 : size(current);
	return limit > 0 ? [{ from: -Infinity, limit }] : [];
}

/** The limit in force at `t`; before the first step, the first step's. */
export function limitAt(steps: readonly LimitStep[], t: number): number | undefined {
	return steps.findLast((s) => s.from <= t)?.limit ?? steps[0]?.limit;
}
