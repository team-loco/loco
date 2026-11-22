import { create } from "@bufbuild/protobuf";
import { SizeLimitsSchema, type SizeLimits } from "@gen/loco/config/v1/config_pb";

import { bytesToMi, formatCpu, formatMemory, parseCpuMilli, parseMemMi } from "./quantity";

// The sizes the forms offer are a UI choice; GetConfig's size limits decide which of them
// an installation accepts, so the API stays the only source of the limits.
const CPU_PRESETS = ["100m", "250m", "500m", "1", "2"];
const MEMORY_PRESETS = ["128Mi", "256Mi", "512Mi", "1Gi", "2Gi", "4Gi"];

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
	const minMemory = bytesToMi(l.minMemoryBytes);
	const maxMemory = bytesToMi(l.maxMemoryBytes);
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

function withValue(
	options: SizeOption[],
	value: string,
	parse: (v: string) => number,
	format: (n: number) => string,
): SizeOption[] {
	if (value === "" || options.some((o) => o.value === value)) return options;
	const at = parse(value);
	const next = options.findIndex((o) => parse(o.value) > at);
	const added = { value, label: format(at) };
	return next === -1 ? [...options, added] : [...options.slice(0, next), added, ...options.slice(next)];
}

/** The CPU options with the saved value among them, so the slider thumb sits at it. */
export function cpuWith(options: SizeOption[], value: string): SizeOption[] {
	return withValue(options, value, parseCpuMilli, formatCpu);
}

/** The memory options with the saved value among them, so the slider thumb sits at it. */
export function memoryWith(options: SizeOption[], value: string): SizeOption[] {
	return withValue(options, value, parseMemMi, formatMemory);
}
