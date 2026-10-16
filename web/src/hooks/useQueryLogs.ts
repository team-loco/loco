import { useQuery, useQueryClient } from "@tanstack/react-query";
import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { ObservabilityProxyService, LogOrder } from "@gen/loco/observability/v1/observability_pb";
import { timeRangeMs, type TimeRange } from "@/lib/obs";
import type { ParsedQuery } from "@/lib/obs-query-parser";
import { msToTimestamp } from "@/lib/time";

const NANOS_PER_SECOND = 1_000_000_000;

interface UseQueryLogsOptions {
	workspaceId: string;
	resourceIds: string[];
	timeRange: TimeRange;
	parsedQuery: ParsedQuery;
	cursor?: string;
	limit?: number;
	order?: LogOrder;
	startTime?: Timestamp;
	endTime?: Timestamp;
	around?: number | undefined;
	enabled?: boolean;
}

export function logsQueryOptions(transport: Transport, {
	workspaceId, resourceIds, timeRange, parsedQuery, cursor = "", limit = 100,
	order = LogOrder.NEWEST_FIRST, startTime, endTime, around, enabled = true,
}: UseQueryLogsOptions) {
	const input = {
		workspaceId, resourceIds, search: parsedQuery.search, levels: parsedQuery.levels,
		labels: parsedQuery.labels, regions: parsedQuery.regions ?? [],
		excludedRegions: parsedQuery.excludedRegions ?? [], limit, cursor, order, startTime, endTime,
	};
	return {
		queryKey: [...createConnectQueryKey({ schema: ObservabilityProxyService.method.queryLogs,
			transport, cardinality: "finite", input }), timeRange, around ?? null],
		queryFn: async ({ signal }: { signal: AbortSignal }) => {
			const now = Date.now();
			const client = createClient(ObservabilityProxyService, transport);
			const request = { ...input, startTime: startTime ?? msToTimestamp(now - timeRangeMs(timeRange)),
				endTime: endTime ?? msToTimestamp(now) };
			if (around !== undefined) {
				const target = msToTimestamp(around);
				const before = { ...target, seconds: target.nanos === 0 ? target.seconds - 1n : target.seconds,
					nanos: target.nanos === 0 ? NANOS_PER_SECOND - 1 : target.nanos - 1 };
				if (cursor === "") {
					const half = Math.max(1, Math.floor(limit / 2));
					const [older, newer] = await Promise.all([
						client.queryLogs({ ...request, endTime: before, limit: half, order: LogOrder.NEWEST_FIRST }, { signal }),
						client.queryLogs({ ...request, startTime: target, limit: half, order: LogOrder.OLDEST_FIRST }, { signal }),
					]);
					return { ...older, entries: [...older.entries, ...newer.entries],
						totalMatched: older.totalMatched + newer.totalMatched,
						olderCursor: older.nextCursor, newerCursor: newer.nextCursor };
				}
				if (order === LogOrder.OLDEST_FIRST) request.startTime = target;
				else request.endTime = before;
			}
			const response = await client.queryLogs(request, { signal });
			return { ...response, olderCursor: order === LogOrder.OLDEST_FIRST ? "" : response.nextCursor,
				newerCursor: order === LogOrder.OLDEST_FIRST ? response.nextCursor : "" };
		},
		enabled: enabled && workspaceId !== "",
		staleTime: 30_000,
	};
}

export function latestLogsQueryOptions(transport: Transport, options: UseQueryLogsOptions, end: number) {
	return { ...logsQueryOptions(transport, {
		...options, cursor: "", order: LogOrder.NEWEST_FIRST, around: undefined,
		startTime: msToTimestamp(end - timeRangeMs(options.timeRange)), endTime: msToTimestamp(end),
	}), staleTime: 0 };
}

export function useQueryLogs(options: UseQueryLogsOptions) {
	const transport = useTransport();
	const client = useQueryClient();
	const query = useQuery(logsQueryOptions(transport, options));
	return { entries: query.data?.entries ?? [],
		olderCursor: query.data?.olderCursor ?? "", newerCursor: query.data?.newerCursor ?? "",
		isLoading: query.isLoading, error: query.error,
		refetchLatest: () => {
			const end = Date.now();
			return { end, result: client.query(latestLogsQueryOptions(transport, options, end)) };
		} };
}
