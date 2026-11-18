import type { MetricSeries } from "@gen/loco/observability/v1/observability_pb";
import { maybeTsMs } from "@/lib/time";

export const TRAFFIC_METRICS = [
	{ metricName: "cluster.upstream_rq_total", aggregation: "rate" },
	{
		metricName: "cluster.upstream_rq_xx",
		aggregation: "rate",
		attributes: { "envoy.response_code_class": "5" },
	},
	{ metricName: "cluster.upstream_cx_rx_bytes_total", aggregation: "rate" },
	{ metricName: "cluster.upstream_rq_time", aggregation: "p95" },
] as const;

export interface TrafficPoint {
	resourceId: string;
	time: number;
	requests?: number;
	errors?: number;
	bytes?: number;
	latency?: number;
}

export function trafficPoints(metrics: (MetricSeries[] | undefined)[]): TrafficPoint[] {
	const points = new Map<string, TrafficPoint>();
	const fields = ["requests", "errors", "bytes", "latency"] as const;
	metrics.forEach((series, index) => {
		const field = fields[index];
		if (field === undefined) return;
		for (const s of series ?? []) {
			for (const p of s.points) {
				const time = maybeTsMs(p.timestamp);
				if (time === undefined) continue;
				const key = `${s.resourceId}/${String(time)}`;
				const point = points.get(key) ?? { resourceId: s.resourceId, time };
				point[field] = p.value;
				points.set(key, point);
			}
		}
	});
	if (metrics[1] !== undefined) {
		for (const point of points.values()) {
			if (point.requests !== undefined && point.errors === undefined) point.errors = 0;
		}
	}
	return [...points.values()].toSorted((a, b) => a.time - b.time);
}

export function latestTraffic(points: TrafficPoint[], resourceId?: string): TrafficPoint | undefined {
	const matching = points.filter((point) => resourceId === undefined || point.resourceId === resourceId);
	const time = Math.max(...matching.map((point) => point.time));
	if (!Number.isFinite(time)) return undefined;
	const result: TrafficPoint = { resourceId: resourceId ?? "", time };
	for (const point of matching) {
		if (point.time !== time) continue;
		for (const field of ["requests", "errors", "bytes"] as const) {
			if (point[field] !== undefined) result[field] = (result[field] ?? 0) + point[field];
		}
		if (resourceId !== undefined && point.latency !== undefined) result.latency = point.latency;
	}
	return result;
}

export function errorShare(point: TrafficPoint | undefined): number | undefined {
	if (point?.requests === undefined) return undefined;
	if (point.requests === 0) return 0;
	return point.errors === undefined ? undefined : (point.errors / point.requests) * 100;
}

export function trafficValue(value: number | undefined, unit: string): string {
	return value === undefined
		? "—"
		: `${new Intl.NumberFormat(undefined, { maximumFractionDigits: 2, notation: "compact" }).format(value)}${unit}`;
}

export type TrafficField = "requests" | "errors" | "bytes";

/** Totals a per-second rate over buckets `stepMs` wide, per resource. */
export function trafficTotals(points: TrafficPoint[], field: TrafficField, stepMs: number): Map<string, number> {
	const totals = new Map<string, number>();
	for (const point of points) {
		const rate = point[field];
		if (rate !== undefined) totals.set(point.resourceId, (totals.get(point.resourceId) ?? 0) + (rate * stepMs) / 1000);
	}
	return totals;
}

export function sumTotals(totals: Map<string, number>): number | undefined {
	return totals.size === 0 ? undefined : [...totals.values()].reduce((a, b) => a + b, 0);
}

/** One value per bucket, summed across resources or for one resource, oldest first. */
export function trafficSeries(points: TrafficPoint[], field: TrafficField, resourceId?: string): number[] {
	const buckets = new Map<number, number>();
	for (const point of points) {
		const rate = point[field];
		if (rate === undefined || (resourceId !== undefined && point.resourceId !== resourceId)) continue;
		buckets.set(point.time, (buckets.get(point.time) ?? 0) + rate);
	}
	return [...buckets.entries()].toSorted((a, b) => a[0] - b[0]).map(([, value]) => value);
}

/** The change from `prior` to `current` as "↑ 6.2%", or undefined when there is nothing to compare. */
export function changeLabel(current: number | undefined, prior: number | undefined): string | undefined {
	if (current === undefined || prior === undefined || prior === 0) return undefined;
	const pct = ((current - prior) / prior) * 100;
	const digits = Math.abs(pct) < 10 ? 1 : 0;
	return `${pct < 0 ? "↓" : "↑"} ${Math.abs(pct).toFixed(digits)}%`;
}

const compact = new Intl.NumberFormat("en-US", { maximumFractionDigits: 1, notation: "compact" });
const BYTE_UNITS = ["B", "KB", "MB", "GB", "TB"];

export function formatCompact(value: number): string {
	return compact.format(value);
}

export function formatBytes(value: number): string {
	let n = value;
	let unit = 0;
	while (n >= 1000 && unit < BYTE_UNITS.length - 1) {
		n /= 1000;
		unit++;
	}
	return `${n.toFixed(n >= 100 || unit === 0 ? 0 : 1)} ${BYTE_UNITS[unit] ?? ""}`;
}
