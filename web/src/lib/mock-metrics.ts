import type { Resource } from "@gen/loco/resource/v1/resource_pb";

// ─────────────────────────────────────────────────────────────────────────────
// PLACEHOLDER DATA — none of this reflects the running deployment.
//
// The dashboard renders CPU bars, request counts and uptime from these
// functions. They are deterministic noise derived from the resource id, kept
// here (rather than inline in the components) so the whole fiction is in one
// file and can be deleted in a single change once the otel/ClickHouse
// pipeline feeds the UI for real.
// ─────────────────────────────────────────────────────────────────────────────

/** Stable per-resource seed, so the fake numbers at least don't jitter. */
function seedFromId(resourceId: string): number {
	let seed = 0;
	for (let i = 0; i < resourceId.length; i++) seed += resourceId.charCodeAt(i);
	return seed;
}

function wave(seed: number, scale: number): number {
	return Math.floor((Math.sin(seed) * 0.5 + 0.5) * scale);
}

export interface MockMetrics {
	cpu: number;
	memory: number;
	requests: string;
	uptime: string;
}

function baseMetrics(resourceId: string): MockMetrics {
	const seed = seedFromId(resourceId);
	return {
		cpu: wave(seed, 100),
		memory: Math.floor((Math.cos(seed) * 0.5 + 0.5) * 1000) + 256,
		requests: `${wave(seed * 2, 900).toString()}K`,
		uptime: "99.9%",
	};
}

export function getGridMetrics(resource: Resource) {
	const seed = seedFromId(resource.id);
	return {
		...baseMetrics(resource.id),
		// The real commit/branch live on the deployment, not the resource.
		commit: resource.id.slice(0, 7),
		branch: "main",
		replicas: Math.floor((Math.cos(seed * 3) * 0.5 + 0.5) * 5) + 1,
	};
}

export function getTableMetrics(resource: Resource) {
	const base = baseMetrics(resource.id);
	const seed = seedFromId(resource.id);
	return {
		...base,
		requests: `${base.requests}/day`,
		replicas: Math.floor((Math.cos(seed * 3) * 0.5 + 0.5) * 5) + 1,
	};
}
