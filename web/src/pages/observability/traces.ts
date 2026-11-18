import type { Timestamp } from "@bufbuild/protobuf/wkt";
import type { TraceSpan, TraceSpanEvent } from "@gen/loco/observability/v1/observability_pb";

const NANOS_PER_SECOND = 1_000_000_000n;
const NANOS_PER_MS = 1_000_000n;
const NANOS_PER_MICROSECOND = 1_000n;

export const TRACE_ID_PATTERN = /^[0-9a-f]{32}$/;
const SPAN_ID_PATTERN = /^[0-9a-f]{16}$/;

export function timestampNanos(ts: Timestamp | undefined): bigint {
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

// spanLinkSearch opens a linked span in the trace drawer, keeping the page's other query parameters.
export function spanLinkSearch(search: string, traceId: string, spanId: string): string | null {
	if (!TRACE_ID_PATTERN.test(traceId) || !SPAN_ID_PATTERN.test(spanId)) return null;
	const params = new URLSearchParams(search);
	params.set("trace", traceId);
	params.set("span", spanId);
	return `?${params.toString()}`;
}

export function durationLabel(nanos: bigint): string {
	if (nanos === 0n) return "0";
	const ms = Number(nanos) / Number(NANOS_PER_MS);
	if (ms >= 1000) return `${(ms / 1000).toFixed(2)}s`;
	if (ms >= 10) return `${String(Math.round(ms))}ms`;
	if (ms >= 1) return `${ms.toFixed(1)}ms`;
	if (nanos >= NANOS_PER_MICROSECOND) return `${String(Math.round(Number(nanos) / Number(NANOS_PER_MICROSECOND)))}µs`;
	return `${String(nanos)}ns`;
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
