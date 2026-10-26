import { useInfiniteQuery } from "@tanstack/react-query";
import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { ObservabilityProxyService } from "@gen/loco/observability/v1/observability_pb";
import { msToTimestamp } from "@/lib/time";

export interface TraceQueryOptions {
	workspaceId: string;
	environmentId: string;
	resourceIds: string[];
	traceId: string;
	start: number;
	end: number;
	enabled?: boolean;
}

const TRACE_PAGE_SIZE = 500;
const NANOS_PER_MILLISECOND = 1_000_000;
const MILLIS_PER_SECOND = 1000;

export function tracesQueryOptions(transport: Transport, options: TraceQueryOptions) {
	const { enabled = true, start, end, ...scope } = options;
	const startTime = { ...msToTimestamp(start), nanos: (start % MILLIS_PER_SECOND) * NANOS_PER_MILLISECOND };
	const endTime = { ...msToTimestamp(end), nanos: (end % MILLIS_PER_SECOND) * NANOS_PER_MILLISECOND };
	const input = { ...scope, startTime, endTime, limit: TRACE_PAGE_SIZE };
	return {
		queryKey: createConnectQueryKey({
			schema: ObservabilityProxyService.method.queryTraces,
			transport,
			cardinality: "infinite",
			input,
		}),
		initialPageParam: "",
		queryFn: async ({ pageParam, signal }: { pageParam: string; signal: AbortSignal }) =>
			await createClient(ObservabilityProxyService, transport).queryTraces({ ...input, cursor: pageParam }, { signal }),
		getNextPageParam: (last: { nextCursor: string }) => last.nextCursor || undefined,
		enabled: enabled && options.workspaceId !== "" && options.environmentId !== "",
		staleTime: Infinity,
	};
}

export function useQueryTraces(options: TraceQueryOptions) {
	return useInfiniteQuery(tracesQueryOptions(useTransport(), options));
}
