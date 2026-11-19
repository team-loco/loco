import type { Replica, ReplicaExit, ReplicaRestart } from "@gen/loco/deployment/v1/deployment_pb";
import type { MetricSeries } from "@gen/loco/observability/v1/observability_pb";
import { ResourceStatus, type EnvironmentStatus, type RegionStatus } from "@gen/loco/resource/v1/resource_pb";

import { readyIn } from "@/lib/activity-deploys";
import { placementIn } from "@/lib/deployment-utils";
import { agoLabel, maybeTsMs } from "@/lib/time";

import type { RegionView } from "./model";

export const POD_LABEL = "k8s.pod.name";
/** Two collection intervals of the worker's kubeletstats receiver. */
const USAGE_FRESH_MS = 120_000;

function unreportedRegions(environment: EnvironmentStatus | undefined): RegionStatus[] {
	return (environment?.regions ?? []).filter((r) => r.status === ResourceStatus.UNKNOWN);
}

function lastReportMs(regions: RegionStatus[]): number | undefined {
	const reported = regions.flatMap((r) => {
		const ms = maybeTsMs(r.reportedAt);
		return ms === undefined ? [] : [ms];
	});
	return reported.length === 0 ? undefined : Math.min(...reported);
}

export interface ReadyCount {
	ready: number;
	desired: number;
}

/** The region's ready and desired replicas of its live deployment's current revision, and the other ready ones. */
export function regionReady(region: RegionView): ReadyCount & { previous: number } {
	const placement = placementIn(region.current, region.name);
	if (region.current === undefined || placement === undefined)
		return { ready: 0, desired: region.status?.desiredReplicas ?? 0, previous: 0 };
	const ready = readyIn(region.current, placement);
	const previous = placement.replicas.filter((r) => r.ready).length - ready;
	return { ready, desired: placement.desiredReplicas, previous };
}

export function replicaLine(
	environment: EnvironmentStatus | undefined,
	nowMs: number,
	countOf: (region: RegionStatus) => ReadyCount,
): string {
	if (environment === undefined) return "Not deployed";
	const regions = environment.regions;
	const desired = regions.reduce((sum, r) => sum + countOf(r).desired, 0);
	const ready = regions.reduce((sum, r) => sum + countOf(r).ready, 0);
	const counts = `${ready.toString()} of ${desired.toString()} ready`;
	const regionSuffix = regions.length > 1 ? ` · ${regions.length.toString()} regions` : "";
	const unreported = unreportedRegions(environment);
	if (unreported.length === 0) return `${counts}${regionSuffix}`;
	const reportedMs = lastReportMs(unreported);
	if (unreported.length === regions.length) {
		if (reportedMs === undefined) return `Replicas not reported yet${regionSuffix}`;
		return `${counts} at last report, ${agoLabel(reportedMs, nowMs)}${regionSuffix}`;
	}
	const names = unreported.map((r) => r.region).join(", ");
	const age = reportedMs === undefined ? "never reported" : `last reported ${agoLabel(reportedMs, nowMs)}`;
	return `${counts}${regionSuffix} · ${names} ${age}`;
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
	const label = `${exit.reason} · exit ${exit.exitCode.toString()}`;
	const at = maybeTsMs(exit.finishedAt);
	return at === undefined ? label : `${label} · ${agoLabel(at, nowMs)}`;
}

/** What a replica restarted after, in the controller's cause vocabulary (k8sapi RestartCause). */
function restartCause(restart: ReplicaRestart): string {
	if (restart.cause === "health_check") return "health check";
	if (restart.cause === "out_of_memory") return "out of memory";
	return `exit ${restart.exitCode.toString()}`;
}

export function restartLabel(replica: Replica, nowMs: number): string {
	const count = `${replica.restarts.toString()} ${replica.restarts === 1 ? "restart" : "restarts"}`;
	const restart = replica.lastRestart;
	if (restart === undefined) return count;
	const at = maybeTsMs(restart.at);
	const when = at === undefined ? [] : [`last ${agoLabel(at, nowMs)}`];
	return [count, ...when, restartCause(restart)].join(" · ");
}
