import type { MetricSeries } from "@gen/loco/observability/v1/observability_pb";
import { maybeTsMs } from "@/lib/time";

export const TRAFFIC_METRICS = [
	{ metricName: "cluster.upstream_rq_total", aggregation: "rate" },
	{ metricName: "cluster.upstream_rq_xx", aggregation: "rate", attributes: { "envoy.response_code_class": "5" } },
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

export function trafficPoints(metrics: MetricSeries[][]): TrafficPoint[] {
	const points = new Map<string, TrafficPoint>();
	const fields = ["requests", "errors", "bytes", "latency"] as const;
	metrics.forEach((series, index) => {
		const field = fields[index];
		if (field === undefined) return;
		for (const s of series) {
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
