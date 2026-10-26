import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";

const NANOS_PER_SECOND = 1_000_000_000n;
const NANOS_PER_MS = 1_000_000n;
const NANOS_PER_MICROSECOND = 1_000n;

export const TRACE_ID_PATTERN = /^[0-9a-f]{32}$/;

export function spanStart(span: TraceSpan): bigint {
	return (span.startTime?.seconds ?? 0n) * NANOS_PER_SECOND + BigInt(span.startTime?.nanos ?? 0);
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
	children: number;
	missingParent: boolean;
}

export function spanTree(spans: TraceSpan[], collapsed: Set<string>): SpanRow[] {
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
			const descendants = children.get(row.span.spanId) ?? [];
			result.push({
				...row,
				children: descendants.length,
				missingParent: row.span.parentSpanId !== "" && !ids.has(row.span.parentSpanId),
			});
			if (!collapsed.has(row.span.spanId)) {
				for (const child of descendants.toReversed()) stack.push({ span: child, depth: row.depth + 1 });
			} else {
				const hidden = [...descendants];
				while (hidden.length > 0) {
					const child = hidden.pop();
					if (child === undefined || visited.has(child.spanId)) continue;
					visited.add(child.spanId);
					hidden.push(...(children.get(child.spanId) ?? []));
				}
			}
		}
	};
	for (const span of ordered.filter((candidate) => !ids.has(candidate.parentSpanId))) walk(span);
	for (const span of ordered) if (!visited.has(span.spanId)) walk(span);
	return result;
}

export function spanPosition(span: TraceSpan, start: bigint, duration: bigint): { left: string; width: string } {
	const total = duration > 0n ? duration : 1n;
	return {
		left: `${Number(((spanStart(span) - start) * 10000n) / total) / 100}%`,
		width: `${Number((span.durationNanos * 10000n) / total) / 100}%`,
	};
}
