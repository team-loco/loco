import type { MetricSeries } from "@gen/loco/observability/v1/observability_pb";

import { maybeTsMs } from "@/lib/time";

export type MetricRange = "1h" | "6h" | "24h";

export const METRIC_RANGES: MetricRange[] = ["1h", "6h", "24h"];

export const RANGE_MS: Record<MetricRange, number> = {
	"1h": 3_600_000,
	"6h": 21_600_000,
	"24h": 86_400_000,
};

export const BAND_AGGREGATIONS = ["min", "p50", "max"] as const;

export type BandAggregation = (typeof BAND_AGGREGATIONS)[number];

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

export interface BandPoint {
	time: number;
	median: number | null;
	band: [number, number] | null;
}

export function mergeBand(parts: Record<BandAggregation, MetricPointPct[]>): BandPoint[] {
	const byTime = new Map<number, Partial<Record<BandAggregation, number>>>();
	for (const aggregation of BAND_AGGREGATIONS) {
		for (const p of parts[aggregation]) {
			const entry = byTime.get(p.time) ?? {};
			entry[aggregation] = p.pct;
			byTime.set(p.time, entry);
		}
	}
	return [...byTime.entries()]
		.toSorted(([a], [b]) => a - b)
		.map(([time, v]) => ({
			time,
			median: v.p50 ?? null,
			band: v.min !== undefined && v.max !== undefined ? [v.min, v.max] : null,
		}));
}

// Absolute units only make sense when every charted region runs the same size.
export function sharedLimit(sizes: readonly number[]): number | undefined {
	const first = sizes[0];
	if (first === undefined || first <= 0 || sizes.some((n) => n !== first)) return undefined;
	return first;
}

export function scaleBand(points: BandPoint[], limit: number): BandPoint[] {
	const k = limit / PERCENT;
	return points.map((p) => ({
		time: p.time,
		median: p.median === null ? null : p.median * k,
		band: p.band === null ? null : [p.band[0] * k, p.band[1] * k],
	}));
}

export function usageSummary(points: BandPoint[]): { average: number | undefined; highest: number | undefined } {
	const medians = points.flatMap((p) => (p.median === null ? [] : [p.median]));
	const highs = points.flatMap((p) => (p.band === null ? (p.median === null ? [] : [p.median]) : [p.band[1]]));
	return {
		average: medians.length === 0 ? undefined : medians.reduce((a, b) => a + b, 0) / medians.length,
		highest: highs.length === 0 ? undefined : Math.max(...highs),
	};
}
