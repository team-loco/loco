import { ClusterRole, type Cluster, type ClusterCapacity } from "@gen/loco/cluster/v1/cluster_pb";

import { BYTES_PER_MI, formatCpu, formatMemory } from "@/lib/quantity";
import { tsMs } from "@/lib/time";
import { clusterState } from "@/lib/system";

export interface Reservation {
	label: string;
	requested: number;
	allocatable: number;
	format: (value: number) => string;
}

export function reservations(capacity: ClusterCapacity): Reservation[] {
	return [
		{
			label: "CPU",
			requested: Number(capacity.cpuMillicoresRequested),
			allocatable: Number(capacity.cpuMillicoresAllocatable),
			format: formatCpu,
		},
		{
			label: "Memory",
			requested: Number(capacity.memoryBytesRequested),
			allocatable: Number(capacity.memoryBytesAllocatable),
			format: (value) => `${formatMemory(value / BYTES_PER_MI)}B`,
		},
		{
			label: "Pod slots",
			requested: capacity.podsAssigned,
			allocatable: capacity.podsAllocatable,
			format: (value) => value.toLocaleString(),
		},
	];
}

export function runsApps(cluster: Cluster): boolean {
	return cluster.role === ClusterRole.APPS || cluster.role === ClusterRole.ALL;
}

/** Only healthy application clusters contribute current deployment headroom. */
export function availableCapacity(clusters: readonly Cluster[]): {
	capacity: ClusterCapacity | undefined;
	included: number;
} {
	const eligible = clusters.filter(runsApps).filter((cluster) => clusterState(cluster) === "healthy");
	const reports = eligible.flatMap((cluster) =>
		cluster.capacity === undefined || cluster.capacity.stale ? [] : [cluster.capacity],
	);
	const [first] = reports;
	if (first === undefined) return { capacity: undefined, included: 0 };
	const sum = {
		...first,
		cpuMillicoresAllocatable: 0n,
		cpuMillicoresRequested: 0n,
		memoryBytesAllocatable: 0n,
		memoryBytesRequested: 0n,
		podsAllocatable: 0,
		podsAssigned: 0,
		podsRunning: 0,
	};
	for (const capacity of reports) {
		if (tsMs(capacity.reportedAt) < tsMs(sum.reportedAt)) sum.reportedAt = capacity.reportedAt;
		sum.cpuMillicoresAllocatable += capacity.cpuMillicoresAllocatable;
		sum.cpuMillicoresRequested += capacity.cpuMillicoresRequested;
		sum.memoryBytesAllocatable += capacity.memoryBytesAllocatable;
		sum.memoryBytesRequested += capacity.memoryBytesRequested;
		sum.podsAllocatable += capacity.podsAllocatable;
		sum.podsAssigned += capacity.podsAssigned;
		sum.podsRunning += capacity.podsRunning;
	}
	return { capacity: sum, included: reports.length };
}

export function headroom(reservation: Reservation): string {
	const left = reservation.allocatable - reservation.requested;
	return left < 0 ? `${reservation.format(-left)} over capacity` : `${reservation.format(left)} unreserved`;
}
