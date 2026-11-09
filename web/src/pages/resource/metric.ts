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

interface BandSummary {
	min: number | undefined;
	median: number | undefined;
	max: number | undefined;
}

export function latestBand(points: BandPoint[]): BandSummary {
	const last = points.at(-1);
	return { min: last?.band?.[0], median: last?.median ?? undefined, max: last?.band?.[1] };
}
