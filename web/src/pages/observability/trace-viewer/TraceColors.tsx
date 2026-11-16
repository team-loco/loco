import { createContext, use, useMemo, type ReactNode } from "react";
import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";
import { childrenOf, serviceShares, spanSelfTimes, traceLayout, type ServiceShare } from "../flame";
import { traceColors, traceResourceKey } from "../trace-service";
import { spanTree } from "../traces";
import type { TraceSummary } from "../traces";

interface TraceResources {
	shares: ServiceShare[];
	layout: ReturnType<typeof traceLayout>;
	children: Map<string, TraceSpan[]>;
	tree: ReturnType<typeof spanTree>;
	selfTime: (span: TraceSpan) => bigint;
	color: (span: Pick<TraceSpan, "resourceId" | "serviceName">) => string;
}
const TraceResourcesContext = createContext<TraceResources | null>(null);

export function TraceColors({ trace, children }: { trace: TraceSummary | undefined; children: ReactNode }) {
	const value = useMemo(() => {
		const childrenById = trace === undefined ? new Map<string, TraceSpan[]>() : childrenOf(trace);
		const times = trace === undefined ? new Map<string, bigint>() : spanSelfTimes(trace, childrenById);
		const shares =
			trace === undefined
				? []
				: serviceShares(trace, times).toSorted((a, b) =>
						a.nanos === b.nanos ? a.key.localeCompare(b.key) : a.nanos > b.nanos ? -1 : 1,
					);
		const colors = traceColors(shares.map((share) => share.key));
		return {
			shares,
			children: childrenById,
			tree: trace === undefined ? [] : spanTree(trace.spans),
			layout: trace === undefined ? { blocks: [], depth: 0 } : traceLayout(trace),
			selfTime: (span: TraceSpan) => {
				const time = times.get(span.spanId);
				if (time === undefined) throw new Error("Missing span self time");
				return time;
			},
			color: (span: Pick<TraceSpan, "resourceId" | "serviceName">) => {
				const color = colors.get(traceResourceKey(span));
				if (color === undefined) throw new Error("Span resource is missing from its trace");
				return color;
			},
		};
	}, [trace]);
	return <TraceResourcesContext value={value}>{children}</TraceResourcesContext>;
}

export function useTraceResources(): TraceResources {
	const value = use(TraceResourcesContext);
	if (value === null) throw new Error("Trace colours require a trace viewer");
	return value;
}
