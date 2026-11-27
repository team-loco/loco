import type { AllocationCoverage } from "@gen/loco/capacity/v1/capacity_pb";

import { BYTES_PER_MI, formatCpu, formatMemory } from "./quantity";

export function reservationQuantity(value: bigint | number, memory: boolean): string {
	return memory ? `${formatMemory(Number(value) / BYTES_PER_MI)}B` : formatCpu(Number(value));
}

export function reservationLabel(value: bigint, memory: boolean, coverage: AllocationCoverage | undefined): string {
	if (coverage === undefined) return "Not reported";
	const incomplete = coverage.missingClusters > 0 || coverage.staleClusters > 0;
	if (incomplete && value === 0n) return "Not reported";
	return reservationQuantity(value, memory);
}
