import { useMemo } from "react";
import { useQueries } from "@tanstack/react-query";
import { createClient } from "@connectrpc/connect";
import { ObservabilityProxyService } from "@gen/loco/observability/v1/observability_pb";
import { LogOrder, type LogEntry } from "@gen/loco/observability/v1/observability_pb";
import { timeRangeMs, type ClusterTransport, type TimeRange } from "@/lib/obs";
import type { ParsedQuery } from "@/lib/obs-query-parser";
import { msToTimestamp } from "@/lib/time";

interface LogsPage {
	entries: LogEntry[];
	nextCursor: string;
	totalMatched: bigint;
}

interface ClusterLogs {
	clusterId: string;
	region: string;
	data: LogsPage | undefined;
	isLoading: boolean;
	error: Error | null;
}

interface UseQueryLogsOptions {
	clusterTransports: ClusterTransport[];
	workspaceId: string;
	resourceIds: string[];
	timeRange: TimeRange;
	parsedQuery: ParsedQuery;
	cursor?: string;
	limit?: number;
	order?: LogOrder;
	enabled?: boolean;
}

export function useQueryLogs({
	clusterTransports,
	workspaceId,
	resourceIds,
	timeRange,
	parsedQuery,
	cursor = "",
	limit = 100,
	order = LogOrder.NEWEST_FIRST,
	enabled = true,
}: UseQueryLogsOptions) {
	const queries = useQueries({
		queries: clusterTransports.map(({ cluster, transport }) => ({
			queryKey: [
				"obs-logs",
				cluster.clusterId.toString(),
				workspaceId,
				resourceIds,
				timeRange,
				parsedQuery,
				cursor,
				limit,
				order,
			],
			queryFn: async () => {
				// Resolved per fetch, not per render: a window pinned during render
				// would freeze and every refetch would re-query the same stale span.
				const now = Date.now();
				const client = createClient(ObservabilityProxyService, transport);
				const resp = await client.queryLogs({
					workspaceId,
					resourceIds,
					startTime: msToTimestamp(now - timeRangeMs(timeRange)),
					endTime: msToTimestamp(now),
					search: parsedQuery.search,
					levels: parsedQuery.levels,
					labels: parsedQuery.labels,
					limit,
					cursor,
					order,
				});
				return {
					entries: resp.entries,
					nextCursor: resp.nextCursor,
					totalMatched: resp.totalMatched,
				} satisfies LogsPage;
			},
			enabled: enabled && !!workspaceId && clusterTransports.length > 0,
			staleTime: 30_000,
		})),
	});

	const clusterLogs: ClusterLogs[] = useMemo(
		() =>
			clusterTransports.map((ct, i) => {
				const q = queries[i];
				return {
					clusterId: ct.cluster.clusterId.toString(),
					region: ct.cluster.region,
					data: q?.data,
					isLoading: q?.isLoading ?? false,
					error: q?.error ?? null,
				};
			}),
		[queries, clusterTransports],
	);

	// Merge entries from all clusters, sorted newest first
	const mergedEntries = useMemo(() => {
		const all: LogEntry[] = [];
		for (const cl of clusterLogs) {
			if (cl.data?.entries) all.push(...cl.data.entries);
		}
		if (order === LogOrder.NEWEST_FIRST) {
			all.sort((a, b) => {
				const ta = a.timestamp ? Number(a.timestamp.seconds) : 0;
				const tb = b.timestamp ? Number(b.timestamp.seconds) : 0;
				return tb - ta;
			});
		} else {
			all.sort((a, b) => {
				const ta = a.timestamp ? Number(a.timestamp.seconds) : 0;
				const tb = b.timestamp ? Number(b.timestamp.seconds) : 0;
				return ta - tb;
			});
		}
		return all;
	}, [clusterLogs, order]);

	const isLoading = clusterLogs.some((c) => c.isLoading);
	const errors = clusterLogs
		.filter((c) => c.error)
		.map((c) => c.error)
		.filter((e): e is Error => e !== null);

	return { clusterLogs, mergedEntries, isLoading, errors };
}
