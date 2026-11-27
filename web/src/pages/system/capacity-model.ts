import { ClusterRole, type Cluster, type ClusterCapacitySummary } from "@gen/loco/cluster/v1/cluster_pb";
import type { AllocationSample } from "@gen/loco/capacity/v1/capacity_pb";
import type { Timestamp } from "@bufbuild/protobuf/wkt";

import { reservationQuantity } from "@/lib/reservation";
import { clusterState } from "@/lib/system";
import { tsMs } from "@/lib/time";

export type CapacitySummary = Pick<
	ClusterCapacitySummary,
	| "cpuMillicoresRequested"
	| "memoryBytesRequested"
	| "cpuMillicoresAllocatable"
	| "memoryBytesAllocatable"
	| "capacityReportedAt"
	| "capacityStale"
> & {
	sample?: Pick<AllocationSample, "reportedAt" | "stale"> | undefined;
};
export interface Reservation {
	label: string;
	requested: bigint | undefined;
	allocatable: bigint | undefined;
	memory: boolean;
}
export function reservations(summary: CapacitySummary): Reservation[] {
	return [
		{
			label: "CPU",
			requested: summary.sample === undefined ? undefined : summary.cpuMillicoresRequested,
			allocatable: summary.cpuMillicoresAllocatable,
			memory: false,
		},
		{
			label: "Memory",
			requested: summary.sample === undefined ? undefined : summary.memoryBytesRequested,
			allocatable: summary.memoryBytesAllocatable,
			memory: true,
		},
	];
}
export function headroom(row: Reservation): string {
	if (row.requested === undefined || row.allocatable === undefined) return "Not reported";
	const left = row.allocatable - row.requested;
	return left < 0n
		? `${reservationQuantity(-left, row.memory)} over capacity`
		: `${reservationQuantity(left, row.memory)} unreserved`;
}
export function runsApps(cluster: Cluster): boolean {
	return cluster.role === ClusterRole.APPS || cluster.role === ClusterRole.ALL;
}

type CurrentCapacity = CapacitySummary & {
	sample: { reportedAt: Timestamp; stale: boolean };
	capacityReportedAt: Timestamp;
};
function currentCapacity(report: CapacitySummary): report is CurrentCapacity {
	if (report.sample === undefined || report.sample.stale) return false;
	if (report.capacityStale) return false;
	if (report.sample.reportedAt === undefined || report.capacityReportedAt === undefined) return false;
	return true;
}

/** Only healthy resource clusters with current, complete reports contribute headroom. */
export function availableCapacity(
	clusters: readonly Cluster[],
	summaries: ReadonlyMap<string, CapacitySummary>,
): { capacity: CapacitySummary | undefined; included: number } {
	const eligible = clusters.filter(runsApps).filter((cluster) => clusterState(cluster) === "healthy");
	const reports = eligible.flatMap((cluster) => {
		const report = summaries.get(cluster.id);
		return report !== undefined && currentCapacity(report) ? [report] : [];
	});
	const first = reports[0];
	if (first === undefined) return { capacity: undefined, included: 0 };
	let reportedAt = first.sample.reportedAt;
	let capacityReportedAt = first.capacityReportedAt;
	const totals = {
		cpuMillicoresRequested: 0n,
		memoryBytesRequested: 0n,
	};
	for (const report of reports) {
		if (tsMs(report.sample.reportedAt) < tsMs(reportedAt)) reportedAt = report.sample.reportedAt;
		if (tsMs(report.capacityReportedAt) < tsMs(capacityReportedAt)) capacityReportedAt = report.capacityReportedAt;
		totals.cpuMillicoresRequested += report.cpuMillicoresRequested;
		totals.memoryBytesRequested += report.memoryBytesRequested;
	}
	return {
		capacity: {
			...totals,
			cpuMillicoresAllocatable: sumKnown(reports.map((report) => report.cpuMillicoresAllocatable)),
			memoryBytesAllocatable: sumKnown(reports.map((report) => report.memoryBytesAllocatable)),
			sample: { reportedAt, stale: false },
			capacityReportedAt,
			capacityStale: false,
		},
		included: reports.length,
	};
}

function sumKnown(values: (bigint | undefined)[]): bigint | undefined {
	let sum = 0n;
	for (const value of values) {
		if (value === undefined) return undefined;
		sum += value;
	}
	return sum;
}
