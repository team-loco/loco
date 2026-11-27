import type { AllocationCoverage } from "@gen/loco/capacity/v1/capacity_pb";

import { formatCpu } from "./quantity";
import { formatBytes } from "./traffic";

export function reservationQuantity(value: bigint | number, memory: boolean): string {
	return memory ? formatBytes(Number(value)) : formatCpu(Number(value));
}

export function reservationLabel(value: bigint, memory: boolean, coverage: AllocationCoverage | undefined): string {
	if (coverage === undefined) return "Not reported";
	if (coverage.missingClusters > 0 && value === 0n) return "Not reported";
	return reservationQuantity(value, memory);
}
