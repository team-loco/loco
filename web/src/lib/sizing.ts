import { create } from "@bufbuild/protobuf";
import { SizeLimitsSchema, type SizeLimits } from "@gen/loco/config/v1/config_pb";

import { formatCpu, formatMemory, parseCpuMilli, parseMemMi } from "./quantity";

// The sizes the forms offer are a UI choice; GetConfig's size limits decide which of them
// an installation accepts, so the API stays the only source of the limits.
const CPU_PRESETS = ["100m", "250m", "500m", "1", "2"];
const MEMORY_PRESETS = ["128Mi", "256Mi", "512Mi", "1Gi", "2Gi", "4Gi"];
const BYTES_PER_MI = 1_048_576;

export interface SizeOption {
	value: string;
	label: string;
}

export interface SizeOptions {
	cpu: SizeOption[];
	memory: SizeOption[];
	replicas: ReplicaRange | null;
}

export interface ReplicaRange {
	min: number;
	max: number;
}

export function sizeOptions(limits: SizeLimits | undefined): SizeOptions {
	const l = limits ?? create(SizeLimitsSchema);
	const minMemory = Number(l.minMemoryBytes) / BYTES_PER_MI;
	const maxMemory = Number(l.maxMemoryBytes) / BYTES_PER_MI;
	return {
		cpu: CPU_PRESETS.filter((p) => within(parseCpuMilli(p), l.minCpuMillis, l.maxCpuMillis)).map((p) => ({
			value: p,
			label: formatCpu(parseCpuMilli(p)),
		})),
		memory: MEMORY_PRESETS.filter((p) => within(parseMemMi(p), minMemory, maxMemory)).map((p) => ({
			value: p,
			label: formatMemory(parseMemMi(p)),
		})),
		replicas: limits === undefined ? null : { min: limits.minReplicas, max: limits.maxReplicas },
	};
}

function within(n: number, min: number, max: number): boolean {
	return n >= min && n <= max;
}
