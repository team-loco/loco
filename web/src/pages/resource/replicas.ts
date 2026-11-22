import type { Replica, ReplicaExit, ReplicaRestart } from "@gen/loco/deployment/v1/deployment_pb";
import type { MetricSeries } from "@gen/loco/observability/v1/observability_pb";

import { readyIn } from "@/lib/activity-deploys";
import { getServiceSpec, placementIn, regionTarget } from "@/lib/deployment-utils";
import { parseCpuMilli, parseMemMi } from "@/lib/quantity";
import { agoLabel, maybeTsMs } from "@/lib/time";

import { regionDeployment, type RegionView } from "./model";

export const POD_LABEL = "k8s.pod.name";
/** Two collection intervals of the worker's kubeletstats receiver. */
const USAGE_FRESH_MS = 120_000;

/** The region's ready and desired replicas of its live deployment's current revision, and the other ready ones. */
export function regionReady(region: RegionView): { ready: number; desired: number; previous: number } {
	const placement = placementIn(region.current, region.name);
	if (region.current === undefined || placement === undefined)
		return { ready: 0, desired: region.status?.desiredReplicas ?? 0, previous: 0 };
	const ready = readyIn(region.current, placement);
	const previous = placement.replicas.filter((r) => r.ready).length - ready;
	return { ready, desired: placement.desiredReplicas, previous };
}

export interface Scaling {
	min: number;
	max: number;
	cpuTarget: number | undefined;
}

/** The replica range every deployed region runs with, "differs" when regions disagree, undefined when none is deployed. */
export function sharedScaling(regions: readonly RegionView[]): Scaling | "differs" | undefined {
	const all = regions.flatMap((r) => {
		const svc = regionTarget(r.current === undefined ? undefined : getServiceSpec(r.current), r.name);
		return svc === undefined
			? []
			: [{ min: svc.minReplicas ?? 0, max: svc.maxReplicas ?? 0, cpuTarget: svc.scalers?.cpuTarget }];
	});
	const first = all[0];
	if (first === undefined) return undefined;
	const same = all.every((s) => s.min === first.min && s.max === first.max && s.cpuTarget === first.cpuTarget);
	return same ? first : "differs";
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

export function exitLabel(exit: ReplicaExit, nowMs: number): string {
	const label = `${exit.reason}, exit ${exit.exitCode.toString()}`;
	const at = maybeTsMs(exit.finishedAt);
	return at === undefined ? label : `${label}, ${agoLabel(at, nowMs)}`;
}

/** What a replica restarted after, in the controller's cause vocabulary (k8sapi RestartCause). */
function restartCause(restart: ReplicaRestart): string {
	if (restart.cause === "health_check") return "health check";
	if (restart.cause === "out_of_memory") return "out of memory";
	return `exit ${restart.exitCode.toString()}`;
}

export function restartCount(replica: Replica): string {
	return `${replica.restarts.toString()} ${replica.restarts === 1 ? "restart" : "restarts"}`;
}

export function restartLabel(replica: Replica, nowMs: number): string {
	const count = restartCount(replica);
	const restart = replica.lastRestart;
	if (restart === undefined) return count;
	const at = maybeTsMs(restart.at);
	const when = at === undefined ? [] : [`last ${agoLabel(at, nowMs)}`];
	return [count, ...when, restartCause(restart)].join(", ");
}
