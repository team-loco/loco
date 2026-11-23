import type { MetricSeries } from "@gen/loco/observability/v1/observability_pb";
import { maybeTsMs } from "@/lib/time";

export const TRAFFIC_METRICS = [
	{ metricName: "cluster.upstream_rq_completed", aggregation: "rate" },
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

const TRAFFIC_FIELDS = ["requests", "errors", "bytes", "latency"] as const;

export function trafficPoints(metrics: (MetricSeries[] | undefined)[]): TrafficPoint[] {
	const points = new Map<string, TrafficPoint>();
	metrics.forEach((series, index) => {
		const field = TRAFFIC_FIELDS[index];
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
	if (matching.length === 0) return undefined;
	const result: TrafficPoint = { resourceId: resourceId ?? "", time: Math.max(...matching.map((point) => point.time)) };
	const fields = resourceId === undefined ? (["requests", "errors", "bytes"] as const) : TRAFFIC_FIELDS;
	for (const field of fields) {
		const withField = matching.filter((point) => point[field] !== undefined);
		const time = Math.max(...withField.map((point) => point.time));
		for (const point of withField) {
			if (point.time === time) result[field] = (result[field] ?? 0) + (point[field] ?? 0);
		}
	}
	return result;
}

export function errorShare(point: TrafficPoint | undefined): number | undefined {
	if (point?.requests === undefined) return undefined;
	if (point.requests === 0) return 0;
	return point.errors === undefined ? undefined : (point.errors / point.requests) * 100;
}

const SMALLEST_SHOWN = 0.01;
const MS_PER_SECOND = 1000;
const AXIS_INTERVALS = 4;
const MIN_AXIS_TOP = 3 * SMALLEST_SHOWN;
const NICE_STEPS = [1, 2, 2.5, 5, 10];
const MAX_AXIS_DIGITS = 6;

/** Milliseconds from one second up read as seconds. */
function inSeconds(value: number, unit: string): { value: number; unit: string } {
	return unit.trim() === "ms" && Math.abs(value) >= MS_PER_SECOND
		? { value: value / MS_PER_SECOND, unit: " s" }
		: { value, unit };
}

/** A traffic value and its unit, kept apart; the number is "—" without a value. */
export function trafficParts(value: number | undefined, unit: string): { number: string; unit: string } {
	if (value === undefined) return { number: "—", unit: "" };
	const shown = inSeconds(value, unit);
	if (shown.value > 0 && shown.value < SMALLEST_SHOWN)
		return { number: `<${String(SMALLEST_SHOWN)}`, unit: shown.unit };
	const number = new Intl.NumberFormat(undefined, { maximumFractionDigits: 2, notation: "compact" }).format(
		shown.value,
	);
	return { number, unit: shown.unit };
}

export function trafficValue(value: number | undefined, unit: string): string {
	const { number, unit: shown } = trafficParts(value, unit);
	return `${number}${shown}`;
}

function niceStep(raw: number): number {
	const magnitude = 10 ** Math.floor(Math.log10(raw));
	return (NICE_STEPS.find((step) => step * magnitude >= raw) ?? 10) * magnitude;
}

function fractionDigits(step: number): number {
	for (let digits = 0; digits < MAX_AXIS_DIGITS; digits++) {
		if (Math.abs(Number(step.toFixed(digits)) - step) < step * 1e-9) return digits;
	}
	return MAX_AXIS_DIGITS;
}

/** Evenly spaced y ticks from zero to `max` in `unit`, each labelled with enough digits to differ from its neighbours. */
export function trafficAxis(
	peak: number,
	unit: string,
): { ticks: number[]; top: number; format: (value: number) => string } {
	const max = peak > 0 ? Math.max(peak, MIN_AXIS_TOP) : 0;
	const scale = unit.trim() === "ms" && max >= MS_PER_SECOND ? MS_PER_SECOND : 1;
	const shownUnit = scale === 1 ? unit : " s";
	const step = niceStep(max > 0 ? max / scale / AXIS_INTERVALS : 1);
	const count = max > 0 ? Math.ceil(max / scale / step) : 1;
	const ticks = Array.from({ length: count + 1 }, (_, i) => Number((i * step * scale).toPrecision(12)));
	const digits = fractionDigits(step);
	const format = new Intl.NumberFormat(undefined, {
		maximumFractionDigits: step >= MS_PER_SECOND ? 1 : digits,
		notation: step >= MS_PER_SECOND ? "compact" : "standard",
	});
	return {
		ticks,
		top: count * step * scale,
		format: (value) => `${format.format(value / scale)}${shownUnit}`,
	};
}

export type TrafficField = "requests" | "errors" | "bytes";

export interface TrafficWindow {
	startMs: number;
	endMs: number;
}

/**
 * Totals a per-second rate over buckets `stepMs` wide, per resource. The server divides each
 * bucket's count by the part of the bucket inside the query window, so each rate is
 * integrated over that same part.
 */
export function trafficTotals(
	points: TrafficPoint[],
	field: TrafficField,
	stepMs: number,
	window: TrafficWindow,
): Map<string, number> {
	const totals = new Map<string, number>();
	for (const point of points) {
		const rate = point[field];
		if (rate === undefined) continue;
		const widthMs = Math.max(0, Math.min(point.time + stepMs, window.endMs) - Math.max(point.time, window.startMs));
		totals.set(point.resourceId, (totals.get(point.resourceId) ?? 0) + (rate * widthMs) / 1000);
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

/** One value per `stepMs` bucket across the whole window, summed across resources, with zero for buckets without data. */
export function windowSeries(
	points: TrafficPoint[],
	field: TrafficField,
	stepMs: number,
	window: TrafficWindow,
): number[] {
	const buckets = new Map<number, number>();
	for (const point of points) {
		const rate = point[field];
		if (rate !== undefined) buckets.set(point.time, (buckets.get(point.time) ?? 0) + rate);
	}
	const values: number[] = [];
	for (let t = Math.floor(window.startMs / stepMs) * stepMs; t < window.endMs; t += stepMs)
		values.push(buckets.get(t) ?? 0);
	return values;
}

/** The change from `prior` to `current` as "↑ 6.2%", "0%" when it rounds to zero, or undefined when there is nothing to compare. */
export function changeLabel(current: number | undefined, prior: number | undefined): string | undefined {
	if (current === undefined || prior === undefined || prior === 0) return undefined;
	const pct = ((current - prior) / prior) * 100;
	const shown = Math.abs(pct).toFixed(Math.abs(pct) < 10 ? 1 : 0);
	if (Number(shown) === 0) return "0%";
	return `${pct < 0 ? "↓" : "↑"} ${shown}%`;
}

const compact = new Intl.NumberFormat("en-US", { maximumFractionDigits: 1, notation: "compact" });
const BYTE_UNITS = ["B", "KB", "MB", "GB", "TB"];

/** Formats a request or error count, which is whole even when it comes from integrated rates. */
export function formatCompact(value: number): string {
	return compact.format(Math.round(value));
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
