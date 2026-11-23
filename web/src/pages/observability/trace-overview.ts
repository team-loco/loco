import { formatCount } from "@/lib/format";
import { formatClock, formatHourMinute, type TimePreferences } from "@/lib/time";

import { durationLabel } from "./traces";

const BUCKET_SECONDS = [1, 2, 5, 10, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 10800, 21600, 43200, 86400];
const MAX_BARS = 60;
const TICK_STEPS = [1, 2, 2.5, 5, 10];
const SECONDS_PER_MINUTE = 60;
const PERCENT = 100;
const LOW_RATE = 0.1;

/** The narrowest round bucket width that keeps a range to at most MAX_BARS bars. */
export function bucketSeconds(rangeMs: number): number {
	const fits = (width: number) => Math.floor(rangeMs / 1000 / width) + 1 <= MAX_BARS;
	return BUCKET_SECONDS.find(fits) ?? BUCKET_SECONDS[BUCKET_SECONDS.length - 1] ?? 1;
}

/** At least count round values from one step up to max. */
export function niceTicks(max: number, count: number): number[] {
	if (max <= 0) return [];
	const raw = max / count;
	const magnitude = 10 ** Math.floor(Math.log10(raw));
	const step = (TICK_STEPS.findLast((s) => s * magnitude <= raw) ?? 1) * magnitude;
	return Array.from({ length: Math.floor(max / step + 1e-9) }, (_, i) => (i + 1) * step);
}

/** Indexes of consecutive buckets with traces, one run per stretch between empty buckets. */
export function filledRuns(counts: number[]): number[][] {
	const runs: number[][] = [];
	counts.forEach((count, i) => {
		if (count === 0) return;
		const last = runs.at(-1);
		if (last?.at(-1) === i - 1) last.push(i);
		else runs.push([i]);
	});
	return runs;
}

export function errorRateLabel(traces: number, errors: number): string {
	if (traces === 0) return "no traces";
	const rate = errors / traces;
	return `${(rate * PERCENT).toFixed(rate < LOW_RATE && errors > 0 ? 1 : 0)}% errors`;
}

/** A duration tick label without trailing zeros: 250ms, 2.5ms, 1s. */
export function durationTick(nanos: number): string {
	return durationLabel(BigInt(Math.round(nanos)))
		.replace(/(\.\d*?)0+(?=\D)/, "$1")
		.replace(/\.(?=\D)/, "");
}

export interface BucketReadout {
	start: number;
	widthSeconds: number;
	traces: number;
	errors: number;
	p50: bigint;
	p95: bigint;
}

function bucketRange(b: BucketReadout, pref: TimePreferences): string {
	const clock = (ms: number) =>
		b.widthSeconds % SECONDS_PER_MINUTE === 0 ? formatHourMinute(ms, pref) : formatClock(ms, false, pref);
	return `${clock(b.start)}–${clock(b.start + b.widthSeconds * 1000)}`;
}

export interface Readout {
	title: string;
	rows: [label: string, value: string][];
}

/** The bucket's time span with its trace and error counts, and its percentiles when it has traces. */
export function bucketReadout(b: BucketReadout, pref: TimePreferences): Readout {
	const rows: Readout["rows"] = [
		["Traces", formatCount(b.traces)],
		["Errors", formatCount(b.errors)],
	];
	if (b.traces > 0) rows.push(["p50", durationLabel(b.p50)], ["p95", durationLabel(b.p95)]);
	return { title: bucketRange(b, pref), rows };
}

export interface LogScale {
	min: number;
	max: number;
	ticks: number[];
}

const MAX_LOG_TICKS = 4;

/** A logarithmic axis from a power of ten at or below the smallest value to one at or above the largest. */
export function logScale(values: number[]): LogScale {
	const positive = values.filter((v) => v > 0);
	const lowest = Math.floor(Math.log10(Math.min(...positive, 1e6)));
	const highest = Math.max(lowest + 1, Math.ceil(Math.log10(Math.max(...positive, 1e6))));
	const stride = Math.ceil((highest - lowest) / MAX_LOG_TICKS);
	const ticks = Array.from(
		{ length: Math.floor((highest - lowest) / stride) + 1 },
		(_, i) => 10 ** (highest - i * stride),
	);
	return { min: 10 ** lowest, max: 10 ** highest, ticks: ticks.filter((t) => t > 10 ** lowest) };
}

/** Position from the bottom, 0 to 1, of a value on a log scale; values at or below the minimum sit at 0. */
export function logPosition(value: number, scale: LogScale): number {
	if (value <= scale.min) return 0;
	return Math.log10(value / scale.min) / Math.log10(scale.max / scale.min);
}
