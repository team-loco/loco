import { ResourceStatus, type EnvironmentStatus, type RegionStatus } from "@gen/loco/resource/v1/resource_pb";

import { agoLabel, maybeTsMs } from "@/lib/time";

export function unreportedRegions(environment: EnvironmentStatus | undefined): RegionStatus[] {
	return (environment?.regions ?? []).filter((r) => r.status === ResourceStatus.UNKNOWN);
}

function lastReportMs(regions: RegionStatus[]): number | undefined {
	const reported = regions.flatMap((r) => {
		const ms = maybeTsMs(r.reportedAt);
		return ms === undefined ? [] : [ms];
	});
	return reported.length === 0 ? undefined : Math.min(...reported);
}

export function replicaLine(environment: EnvironmentStatus | undefined, nowMs: number): string {
	if (environment === undefined) return "Not deployed";
	const regions = environment.regions;
	const desired = regions.reduce((sum, r) => sum + r.desiredReplicas, 0);
	const ready = regions.reduce((sum, r) => sum + r.readyReplicas, 0);
	const counts = `${ready.toString()}/${desired.toString()} replicas ready`;
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
