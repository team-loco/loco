import { createContext, use, type ReactNode } from "react";
import type { TraceSpan } from "@gen/loco/observability/v1/observability_pb";
import { serviceShares, type ServiceShare } from "../flame";
import { traceColors, traceResourceKey } from "../trace-service";
import type { TraceSummary } from "../traces";

interface TraceResources {
	shares: ServiceShare[];
	color: (span: Pick<TraceSpan, "resourceId" | "serviceName">) => string;
}
const TraceResourcesContext = createContext<TraceResources | null>(null);

export function TraceColors({ trace, children }: { trace: TraceSummary | undefined; children: ReactNode }) {
	const shares =
		trace === undefined
			? []
			: serviceShares(trace).toSorted((a, b) =>
					a.nanos === b.nanos ? a.key.localeCompare(b.key) : a.nanos > b.nanos ? -1 : 1,
				);
	const colors = traceColors(shares.map((share) => share.key));
	const color = (span: Pick<TraceSpan, "resourceId" | "serviceName">) => {
		const value = colors.get(traceResourceKey(span));
		if (value === undefined) throw new Error("Span resource is missing from its trace");
		return value;
	};
	return <TraceResourcesContext value={{ shares, color }}>{children}</TraceResourcesContext>;
}

export function useTraceResources(): TraceResources {
	const value = use(TraceResourcesContext);
	if (value === null) throw new Error("Trace colours require a trace viewer");
	return value;
}
