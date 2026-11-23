import type { Replica } from "@gen/loco/deployment/v1/deployment_pb";
import type { MetricSeries } from "@gen/loco/observability/v1/observability_pb";

import { readyIn } from "@/lib/activity-deploys";
import { getServiceSpec, placementIn, regionTarget } from "@/lib/deployment-utils";
import { parseCpuMilli, parseMemMi } from "@/lib/quantity";
import { maybeTsMs } from "@/lib/time";

import { regionDeployment, type RegionView } from "./model";

export const POD_LABEL = "k8s.pod.name";
/** Two collection intervals of the worker's kubeletstats receiver. */
const USAGE_FRESH_MS = 120_000;

/** The region's ready and desired replicas of its live deployment's current revision. */
export function regionReady(region: RegionView): { ready: number; desired: number } {
	const placement = placementIn(region.current, region.name);
	if (region.current === undefined || placement === undefined)
		return { ready: 0, desired: region.status?.desiredReplicas ?? 0 };
	const ready = readyIn(region.current, placement);
	return { ready, desired: placement.desiredReplicas };
}

/** The CPU (millicores) and memory (Mi) limits of the deployment a replica runs, or of the region's current one. */
export function replicaLimits(region: RegionView, replica: Replica): { cpuMilli: number; memoryMi: number } {
	const deployment = regionDeployment(region, replica.deploymentId) ?? region.current;
	const target = regionTarget(deployment === undefined ? undefined : getServiceSpec(deployment), region.name);
	return { cpuMilli: parseCpuMilli(target?.cpu ?? ""), memoryMi: parseMemMi(target?.memory ?? "") };
}

/** Each pod's newest value from pod-grouped series, leaving out pods without a recent sample. */
export function podUsage(series: readonly MetricSeries[], nowMs: number): Map<string, number> {
	const usage = new Map<string, number>();
	for (const s of series) {
		const pod = s.labels[POD_LABEL];
		if (pod === undefined) continue;
		let newestMs = nowMs - USAGE_FRESH_MS;
		for (const point of s.points) {
			const at = maybeTsMs(point.timestamp);
			if (at !== undefined && at >= newestMs) {
				newestMs = at;
				usage.set(pod, point.value);
			}
		}
	}
	return usage;
}

export function restartCount(replica: Replica): string {
	return `${replica.restarts.toString()} ${replica.restarts === 1 ? "restart" : "restarts"}`;
}
