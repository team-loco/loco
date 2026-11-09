import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";

import { spanError, spanStart, spanTree, type TraceSummary } from "./traces";

const FRACTION_SCALE = 1_000_000n;
const ZOOM_PADDING = 0.04;
const MIN_ZOOM_WIDTH = 0.002;

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
	let depth = 0;
	for (const row of spanTree(trace.spans)) {
		depth = Math.max(depth, row.depth);
		const start = fraction(spanStart(row.span) - trace.start, trace.duration);
		const end = start + fraction(row.span.durationNanos, trace.duration);
		if (end < zoom.a || start > zoom.b) continue;
		const left = Math.max(0, (start - zoom.a) / width);
		const right = Math.min(1, (end - zoom.a) / width);
		blocks.push({ span: row.span, depth: row.depth, left, width: Math.max(0, right - left) });
	}
	return { blocks, depth: depth + 1 };
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
	const covered = (childrenOf(trace).get(span.spanId) ?? []).reduce((sum, child) => sum + child.durationNanos, 0n);
	return covered >= span.durationNanos ? 0n : span.durationNanos - covered;
}

export interface ServiceShare {
	service: string;
	nanos: bigint;
	share: number;
}

export function serviceShares(trace: TraceSummary): ServiceShare[] {
	const totals = new Map<string, bigint>();
	for (const span of trace.spans) {
		totals.set(span.serviceName, (totals.get(span.serviceName) ?? 0n) + selfTime(trace, span));
	}
	const sum = [...totals.values()].reduce((a, b) => a + b, 0n);
	return [...totals].map(([service, nanos]) => ({ service, nanos, share: fraction(nanos, sum) }));
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

export function lineage(trace: TraceSummary, spanId: string): Set<string> {
	const byId = new Map(trace.spans.map((span) => [span.spanId, span]));
	const children = childrenOf(trace);
	const out = new Set<string>();
	for (let id: string | undefined = spanId; id !== undefined && !out.has(id); id = byId.get(id)?.parentSpanId) {
		if (!byId.has(id)) break;
		out.add(id);
	}
	const stack = [...(children.get(spanId) ?? [])];
	while (stack.length > 0) {
		const span = stack.pop();
		if (span === undefined || out.has(span.spanId)) continue;
		out.add(span.spanId);
		stack.push(...(children.get(span.spanId) ?? []));
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
