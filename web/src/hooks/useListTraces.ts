import { useQuery } from "@tanstack/react-query";
import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import {
	ObservabilityProxyService,
	type TraceStatus,
	type TraceSummarySort,
} from "@gen/loco/observability/v1/observability_pb";
import { msToTimestamp } from "@/lib/time";
import type { TraceScopeOptions } from "./useGetTrace";

interface ListTracesOptions extends TraceScopeOptions {
	start: number;
	end: number;
	traceId: string;
	limit: number;
	offset: number;
	rootNameTerms: string[];
	status: TraceStatus;
	sort: TraceSummarySort;
	minDurationNanos?: bigint;
	maxDurationNanos?: bigint;
	ascending?: boolean;
}

export function listTracesQueryOptions(transport: Transport, options: ListTracesOptions) {
	const { enabled = true, start, end, workspaceId, environmentId, resourceIds, ...filters } = options;
	const input = {
		...filters,
		scope: { workspaceId, environmentId, resourceIds },
		startTime: msToTimestamp(start),
		endTime: msToTimestamp(end),
	};
	return {
		queryKey: createConnectQueryKey({
			schema: ObservabilityProxyService.method.listTraces,
			transport,
			cardinality: "finite",
			input,
		}),
		queryFn: async ({ signal }: { signal: AbortSignal }) =>
			await createClient(ObservabilityProxyService, transport).listTraces(input, { signal }),
		enabled: enabled && options.workspaceId !== "" && options.environmentId !== "",
		staleTime: Infinity,
	};
}

export function useListTraces(options: ListTracesOptions) {
	return useQuery(listTracesQueryOptions(useTransport(), options));
}
