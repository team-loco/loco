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

export function errorRateLabel(traces: number, errors: number): string {
	if (traces === 0) return "no traces";
	const rate = errors / traces;
	return `${(rate * PERCENT).toFixed(rate < LOW_RATE && errors > 0 ? 1 : 0)}% errors`;
}

/** A duration tick label without trailing zeros: 250 ms, 2.5 ms, 1 s. */
export function durationTick(nanos: number): string {
	return durationLabel(BigInt(Math.round(nanos)))
		.replace(/(\.\d*?)0+ /, "$1 ")
		.replace(/\. /, " ");
}

function traceCount(n: number, word: string): string {
	return `${formatCount(n)} ${word}${n === 1 ? "" : "s"}`;
}

export interface BucketReadout {
	start: number;
	widthSeconds: number;
	traces: number;
	errors: number;
	p50: bigint;
	p95: bigint;
}

/** "13:05–13:06 · 42 traces · 3 errors · p50 12.00 ms · p95 80.00 ms" */
export function bucketReadout(b: BucketReadout, pref: TimePreferences): string {
	const clock = (ms: number) =>
		b.widthSeconds % SECONDS_PER_MINUTE === 0 ? formatHourMinute(ms, pref) : formatClock(ms, false, pref);
	const parts = [`${clock(b.start)}–${clock(b.start + b.widthSeconds * 1000)}`, traceCount(b.traces, "trace")];
	if (b.errors > 0) parts.push(traceCount(b.errors, "error"));
	if (b.traces > 0) parts.push(`p50 ${durationLabel(b.p50)}`, `p95 ${durationLabel(b.p95)}`);
	return parts.join(" · ");
}
