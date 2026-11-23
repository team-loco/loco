import type { DeploymentBucket } from "@gen/loco/deployment/v1/deployment_pb";
import type { MetricSeries } from "@gen/loco/observability/v1/observability_pb";

import { formatCount } from "@/lib/format";
import { deploySummaries } from "@/lib/deployMarkers";
import { maybeTsMs } from "@/lib/time";

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

const PERCENT = 100;

interface MetricPointPct {
	time: number;
	pct: number;
}

export function percentPoints(series: MetricSeries[], resourceId: string): MetricPointPct[] {
	const points: MetricPointPct[] = [];
	for (const s of series) {
		if (s.resourceId !== "" && s.resourceId !== resourceId) continue;
		for (const p of s.points) {
			const time = maybeTsMs(p.timestamp);
			if (time !== undefined) points.push({ time, pct: p.value * PERCENT });
		}
	}
	return points.toSorted((a, b) => a.time - b.time);
}

// Absolute units only make sense when every charted region runs the same size.
export function sharedLimit(sizes: readonly number[]): number | undefined {
	const first = sizes[0];
	if (first === undefined || first <= 0 || sizes.some((n) => n !== first)) return undefined;
	return first;
}
