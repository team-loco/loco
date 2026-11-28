import type { DeploymentBucket } from "@gen/loco/deployment/v1/deployment_pb";
import type { MetricSeries } from "@gen/loco/observability/v1/observability_pb";

import { formatCount } from "@/lib/format";
import { deploySummaries } from "@/lib/deployMarkers";
import { maybeTsMs } from "@/lib/time";
import type { LimitStep } from "@/lib/limit-history";

export interface ChartMarker {
	ms: number;
	label: string;
}

export const METRIC_RANGES = ["1h", "6h", "24h"] as const;

export type MetricRange = (typeof METRIC_RANGES)[number];

export const RANGE_MS: Record<MetricRange, number> = { "1h": 3_600_000, "6h": 21_600_000, "24h": 86_400_000 };

/** A chart marker per bucket of deployments, at their mean creation time, naming the version of a lone one. */
export function deploymentMarkers(buckets: readonly DeploymentBucket[]): ChartMarker[] {
	return deploySummaries(buckets).map((s) => ({
		ms: s.ms,
		label: s.only === undefined ? `${formatCount(s.count)} deployments` : `deployed ${s.only.version}`,
	}));
}

export interface UsagePoint {
	time: number;
	[key: string]: number | undefined;
}

/** Absolute gauge readings in display units, independent of historical allocations. */
export function usageRows(
	series: readonly { region: string; values: readonly MetricSeries[] }[],
	resourceId: string,
	unitsPerValue: number,
): UsagePoint[] {
	const rows = new Map<number, UsagePoint>();
	for (const { region, values } of series) {
		for (const value of values) {
			if (value.resourceId !== "" && value.resourceId !== resourceId) continue;
			for (const point of value.points) {
				const time = maybeTsMs(point.timestamp);
				if (time === undefined) continue;
				const row = rows.get(time) ?? { time };
				row[region] = point.value * unitsPerValue;
				rows.set(time, row);
			}
		}
	}
	return [...rows.values()].toSorted((a, b) => a.time - b.time);
}

/** Known shared allocations only: missing predecessors leave the earlier interval blank. */
export function allocationRows(
	limits: ReadonlyMap<string, readonly LimitStep[]>,
	from: number,
	to: number,
): { time: number; limit: number | undefined }[] {
	const times = new Set([from, to]);
	for (const steps of limits.values()) {
		for (const step of steps) {
			if (step.from > from && step.from < to) times.add(step.from);
		}
	}
	const rows: { time: number; limit: number | undefined }[] = [];
	for (const time of [...times].toSorted((a, b) => a - b)) {
		const limit = sharedLimit(
			[...limits.values()].map((steps) => steps.findLast((step) => step.from <= time)?.limit ?? 0),
		);
		const previous = rows.at(-1)?.limit;
		// End the known segment at the boundary before leaving the next interval blank.
		if (previous !== undefined && limit === undefined) rows.push({ time, limit: previous });
		rows.push({ time, limit });
	}
	return rows;
}

// One reference line represents every region only when their known allocations match.
function sharedLimit(sizes: readonly number[]): number | undefined {
	const first = sizes[0];
	if (first === undefined || first <= 0 || sizes.some((n) => n !== first)) return undefined;
	return first;
}
