import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";

import { traceResourceKey } from "./trace-service";
import { spanError, spanStart, spanTree, type TraceSummary } from "./traces";

export const MIN_BLOCK = 0.0025;

const FRACTION_SCALE = 1_000_000n;
const ZOOM_PADDING = 0.04;
export const MIN_ZOOM_WIDTH = 0.002;

export interface Zoom {
	a: number;
	b: number;
}

export const FULL_ZOOM: Zoom = { a: 0, b: 1 };

export interface FlameBlock {
	span: TraceSpan;
	depth: number;
	left: number;
	width: number;
}

function fraction(part: bigint, total: bigint): number {
	if (total <= 0n) return 0;
	return Number((part * FRACTION_SCALE) / total) / Number(FRACTION_SCALE);
}

export function flameLayout(trace: TraceSummary, zoom: Zoom): { blocks: FlameBlock[]; depth: number } {
	const width = zoom.b - zoom.a;
	const blocks: FlameBlock[] = [];
	const rows: { start: bigint; end: bigint }[][] = [];
	const placed = new Map<string, number>();
	for (const { span } of spanTree(trace.spans)) {
		const start = spanStart(span);
		const end = start + span.durationNanos;
		const minimum = BigInt(Math.max(1, Math.ceil(Number(trace.duration) * MIN_BLOCK)));
		const occupiedEnd = start + (span.durationNanos > minimum ? span.durationNanos : minimum);
		let row = (placed.get(span.parentSpanId) ?? -1) + 1;
		while (rows[row]?.some((other) => start < other.end && other.start < occupiedEnd)) row++;
		(rows[row] ??= []).push({ start, end: occupiedEnd });
		placed.set(span.spanId, row);
		const from = fraction(start - trace.start, trace.duration);
		const to = fraction(end - trace.start, trace.duration);
		if (to < zoom.a || from > zoom.b) continue;
		const left = Math.max(0, (from - zoom.a) / width);
		const right = Math.min(1, (to - zoom.a) / width);
		blocks.push({ span, depth: row, left, width: Math.max(0, right - left) });
	}
	return { blocks, depth: rows.length };
}

function childrenOf(trace: TraceSummary): Map<string, TraceSpan[]> {
	const map = new Map<string, TraceSpan[]>();
	for (const span of trace.spans) {
		if (span.parentSpanId === span.spanId) continue;
		const group = map.get(span.parentSpanId) ?? [];
		group.push(span);
		map.set(span.parentSpanId, group);
	}
	return map;
}

export function selfTime(trace: TraceSummary, span: TraceSpan): bigint {
	const start = spanStart(span);
	const end = start + span.durationNanos;
	const intervals = (childrenOf(trace).get(span.spanId) ?? [])
		.map((child) => ({
			start: spanStart(child) < start ? start : spanStart(child),
			end: spanStart(child) + child.durationNanos > end ? end : spanStart(child) + child.durationNanos,
		}))
		.filter((interval) => interval.end > interval.start)
		.toSorted((a, b) => (a.start < b.start ? -1 : a.start > b.start ? 1 : 0));
	let covered = 0n;
	let until = start;
	for (const interval of intervals) {
		const from = interval.start > until ? interval.start : until;
		if (interval.end > from) covered += interval.end - from;
		if (interval.end > until) until = interval.end;
	}
	return span.durationNanos - covered;
}

export interface ServiceShare {
	key: string;
	nanos: bigint;
	share: number;
}

export function serviceShares(trace: TraceSummary): ServiceShare[] {
	const totals = new Map<string, bigint>();
	for (const span of trace.spans) {
		const key = traceResourceKey(span);
		totals.set(key, (totals.get(key) ?? 0n) + selfTime(trace, span));
	}
	const sum = [...totals.values()].reduce((a, b) => a + b, 0n);
	return [...totals].map(([key, nanos]) => ({ key, nanos, share: fraction(nanos, sum) }));
}

export function failingSpan(trace: TraceSummary, selected: TraceSpan): TraceSpan | undefined {
	const byId = new Map(trace.spans.map((span) => [span.spanId, span]));
	const errors = trace.spans.filter(spanError);
	const covering = new Set<string>();
	for (const span of errors) {
		const seen = new Set<string>([span.spanId]);
		for (let p = byId.get(span.parentSpanId); p !== undefined && !seen.has(p.spanId); p = byId.get(p.parentSpanId)) {
			seen.add(p.spanId);
			covering.add(p.spanId);
		}
	}
	const failing = errors.filter((span) => !covering.has(span.spanId));
	if (failing.some((span) => span.spanId === selected.spanId)) return undefined;
	return failing.toSorted((a, b) => (spanStart(a) < spanStart(b) ? -1 : 1))[0];
}

export function descendantIds(trace: TraceSummary, spanId: string): Set<string> {
	const children = childrenOf(trace);
	const out = new Set<string>();
	const stack = [spanId];
	while (stack.length > 0) {
		const id = stack.pop();
		if (id === undefined || out.has(id)) continue;
		out.add(id);
		stack.push(...(children.get(id) ?? []).map((span) => span.spanId));
	}
	return out;
}

export function lineage(trace: TraceSummary, spanId: string): Set<string> {
	const byId = new Map(trace.spans.map((span) => [span.spanId, span]));
	const out = descendantIds(trace, spanId);
	let id = byId.get(spanId)?.parentSpanId;
	while (id !== undefined && byId.has(id) && !out.has(id)) {
		out.add(id);
		id = byId.get(id)?.parentSpanId;
	}
	return out;
}

export function zoomTo(trace: TraceSummary, span: TraceSpan): Zoom {
	const start = fraction(spanStart(span) - trace.start, trace.duration);
	const end = start + fraction(span.durationNanos, trace.duration);
	const pad = (end - start) * ZOOM_PADDING;
	const a = Math.max(0, start - pad);
	const b = Math.min(1, Math.max(end + pad, a + MIN_ZOOM_WIDTH));
	return b - a >= 1 ? FULL_ZOOM : { a, b };
}
