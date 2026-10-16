import { useQuery } from "@tanstack/react-query";
import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { ObservabilityProxyService, LogOrder } from "@gen/loco/observability/v1/observability_pb";
import { timeRangeMs, type TimeRange } from "@/lib/obs";
import type { ParsedQuery } from "@/lib/obs-query-parser";
import { msToTimestamp } from "@/lib/time";

interface UseQueryLogsOptions {
	workspaceId: string;
	resourceIds: string[];
	timeRange: TimeRange;
	parsedQuery: ParsedQuery;
	cursor?: string;
	limit?: number;
	order?: LogOrder;
	enabled?: boolean;
}

export function logsQueryOptions(transport: Transport, {
	workspaceId, resourceIds, timeRange, parsedQuery, cursor = "", limit = 100,
	order = LogOrder.NEWEST_FIRST, enabled = true,
}: UseQueryLogsOptions) {
	const input = {
		workspaceId, resourceIds, search: parsedQuery.search, levels: parsedQuery.levels,
		labels: parsedQuery.labels, regions: parsedQuery.regions ?? [],
		excludedRegions: parsedQuery.excludedRegions ?? [], limit, cursor, order,
	};
	return {
		queryKey: [...createConnectQueryKey({ schema: ObservabilityProxyService.method.queryLogs,
			transport, cardinality: "finite", input }), timeRange],
		queryFn: async ({ signal }: { signal: AbortSignal }) => {
			const now = Date.now();
			return await createClient(ObservabilityProxyService, transport).queryLogs({ ...input,
				startTime: msToTimestamp(now - timeRangeMs(timeRange)), endTime: msToTimestamp(now),
			}, { signal });
		},
		enabled: enabled && workspaceId !== "",
		staleTime: 30_000,
	};
}

export function useQueryLogs(options: UseQueryLogsOptions) {
	const query = useQuery(logsQueryOptions(useTransport(), options));
	return { entries: query.data?.entries ?? [], nextCursor: query.data?.nextCursor ?? "",
		isLoading: query.isLoading, error: query.error, refetch: query.refetch };
}
