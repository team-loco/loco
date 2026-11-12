import type { Timestamp } from "@bufbuild/protobuf/wkt";
import type { TraceSpan, TraceSpanEvent } from "@gen/loco/observability/v1/observability_pb";

const NANOS_PER_SECOND = 1_000_000_000n;
const NANOS_PER_MS = 1_000_000n;
const NANOS_PER_MICROSECOND = 1_000n;

export const TRACE_ID_PATTERN = /^[0-9a-f]{32}$/;

function timestampNanos(ts: Timestamp | undefined): bigint {
	return (ts?.seconds ?? 0n) * NANOS_PER_SECOND + BigInt(ts?.nanos ?? 0);
}

export function timestampMs(ts: Timestamp | undefined): number {
	return Number(timestampNanos(ts) / NANOS_PER_MS);
}

export function spanStart(span: TraceSpan): bigint {
	return timestampNanos(span.startTime);
}

export function eventOffsetLabel(span: TraceSpan, event: TraceSpanEvent): string | null {
	if (event.timestamp === undefined) return null;
	return `+${durationLabel(timestampNanos(event.timestamp) - spanStart(span))}`;
}

export function durationLabel(nanos: bigint): string {
	if (nanos >= NANOS_PER_SECOND) return `${(Number(nanos) / Number(NANOS_PER_SECOND)).toFixed(2)} s`;
	if (nanos >= NANOS_PER_MS) return `${(Number(nanos) / Number(NANOS_PER_MS)).toFixed(2)} ms`;
	if (nanos >= NANOS_PER_MICROSECOND) return `${(Number(nanos) / Number(NANOS_PER_MICROSECOND)).toFixed(1)} µs`;
	return `${String(nanos)} ns`;
}

export function axisLabel(nanos: bigint, total: bigint): string {
	const unit =
		total >= NANOS_PER_SECOND
			? { divisor: NANOS_PER_SECOND, name: "s" }
			: total >= NANOS_PER_MS
				? { divisor: NANOS_PER_MS, name: "ms" }
				: total >= NANOS_PER_MICROSECOND
					? { divisor: NANOS_PER_MICROSECOND, name: "µs" }
					: { divisor: 1n, name: "ns" };
	return `${(Number(nanos) / Number(unit.divisor)).toFixed(nanos === 0n ? 0 : 2)} ${unit.name}`;
}

export function spanError(span: TraceSpan): boolean {
	return span.statusCode === "Error" || span.statusCode === "STATUS_CODE_ERROR" || span.statusCode === "2";
}

export function uniqueSpans(spans: TraceSpan[]): TraceSpan[] {
	return [...new Map(spans.map((span) => [`${span.traceId}:${span.spanId}`, span])).values()];
}

export interface TraceSummary {
	id: string;
	root: TraceSpan;
	spans: TraceSpan[];
	start: bigint;
	duration: bigint;
	services: number;
	errors: number;
}

export function summarizeTraces(spans: TraceSpan[]): TraceSummary[] {
	const groups = new Map<string, TraceSpan[]>();
	for (const span of uniqueSpans(spans)) {
		const group = groups.get(span.traceId) ?? [];
		group.push(span);
		groups.set(span.traceId, group);
	}
	return [...groups].map(([id, group]) => {
		group.sort((a, b) =>
			spanStart(a) < spanStart(b) ? -1 : spanStart(a) > spanStart(b) ? 1 : a.spanId.localeCompare(b.spanId),
		);
		const first = group[0];
		if (first === undefined) throw new Error("Empty trace group");
		const start = spanStart(first);
		const end = group.reduce((max, span) => {
			const value = spanStart(span) + span.durationNanos;
			return value > max ? value : max;
		}, start);
		return {
			id,
			root: group.find((span) => span.parentSpanId === "") ?? first,
			spans: group,
			start,
			duration: end - start,
			services: new Set(group.map((span) => span.serviceName)).size,
			errors: group.filter(spanError).length,
		};
	});
}

export interface SpanRow {
	span: TraceSpan;
	depth: number;
}

export function spanTree(spans: TraceSpan[]): SpanRow[] {
	const ordered = uniqueSpans(spans).toSorted((a, b) =>
		spanStart(a) < spanStart(b) ? -1 : spanStart(a) > spanStart(b) ? 1 : a.spanId.localeCompare(b.spanId),
	);
	const ids = new Set(ordered.map((span) => span.spanId));
	const children = new Map<string, TraceSpan[]>();
	for (const span of ordered) {
		const group = children.get(span.parentSpanId) ?? [];
		group.push(span);
		children.set(span.parentSpanId, group);
	}
	const result: SpanRow[] = [];
	const visited = new Set<string>();
	const walk = (root: TraceSpan) => {
		const stack = [{ span: root, depth: 0 }];
		while (stack.length > 0) {
			const row = stack.pop();
			if (row === undefined || visited.has(row.span.spanId)) continue;
			visited.add(row.span.spanId);
			result.push(row);
			for (const child of (children.get(row.span.spanId) ?? []).toReversed()) {
				stack.push({ span: child, depth: row.depth + 1 });
			}
		}
	};
	for (const span of ordered.filter((candidate) => !ids.has(candidate.parentSpanId))) walk(span);
	for (const span of ordered) if (!visited.has(span.spanId)) walk(span);
	return result;
}

const SCALE_FLOOR_MS = 0.5;
const SCALE_LOW_CAP_MS = 10;
const SCALE_HIGH_FLOOR_MS = 100;
const SCALE_HEADROOM = 1.3;
const DECADES_MS = [0.001, 0.01, 0.1, 1, 10, 100, 1_000, 10_000, 100_000, 1_000_000];

export interface DurationScale {
	ticks: { ms: number; y: number; label: string }[];
	/** Vertical position from 0 (top) to 1 (bottom) of a duration in milliseconds. */
	yOf: (ms: number) => number;
	/** Duration in milliseconds at a vertical position, the inverse of yOf. */
	msAt: (y: number) => number;
}

/** A log scale that always spans 10 ms to 100 ms so a few similar traces don't fill the chart. */
export function durationScale(durationsMs: number[]): DurationScale {
	const lo = Math.max(SCALE_FLOOR_MS, Math.min(SCALE_LOW_CAP_MS, ...durationsMs));
	const hi = Math.max(SCALE_HIGH_FLOOR_MS, ...durationsMs) * SCALE_HEADROOM;
	const logLo = Math.log(lo);
	const logSpan = Math.log(hi) - logLo;
	const yOf = (ms: number) => Math.min(1, Math.max(0, 1 - (Math.log(Math.max(ms, lo)) - logLo) / logSpan));
	const msAt = (y: number) => Math.exp(logLo + (1 - y) * logSpan);
	const ticks = DECADES_MS.filter((ms) => ms >= lo && ms <= hi).map((ms) => ({
		ms,
		y: yOf(ms),
		label: durationLabel(BigInt(Math.round(ms * Number(NANOS_PER_MS)))).replace(/\.0+ /, " "),
	}));
	return { ticks, yOf, msAt };
}
