import { useMemo } from "react";
import { useQueries } from "@tanstack/react-query";
import { createClient } from "@connectrpc/connect";
import { ObservabilityProxyService } from "@/gen/loco/observability/v1/observability_pb";
import { LogOrder, type LogEntry } from "@/gen/loco/observability/v1/observability_pb";
import { create } from "@bufbuild/protobuf";
import { TimestampSchema } from "@bufbuild/protobuf/wkt";
import type { ClusterTransport } from "@/components/observability/ObsProvider";
import { timeRangeMs, type TimeRange } from "@/components/observability/ObsProvider";
import type { ParsedQuery } from "@/lib/obs-query-parser";

export interface LogsPage {
	entries: LogEntry[];
	nextCursor: string;
	totalMatched: bigint;
}

export interface ClusterLogs {
	clusterId: string;
	region: string;
	data: LogsPage | undefined;
	isLoading: boolean;
	error: Error | null;
}

function dateToTimestamp(date: Date) {
	return create(TimestampSchema, {
		seconds: BigInt(Math.floor(date.getTime() / 1000)),
		nanos: 0,
	});
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
	const now = useMemo(() => Date.now(), [timeRange]); // eslint-disable-line react-hooks/exhaustive-deps
	const startTime = dateToTimestamp(new Date(now - timeRangeMs(timeRange)));
	const endTime = dateToTimestamp(new Date(now));

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
				const client = createClient(ObservabilityProxyService, transport);
				const resp = await client.queryLogs({
					workspaceId,
					resourceIds,
					startTime,
					endTime,
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
			queries.map((q, i) => ({
				clusterId: clusterTransports[i].cluster.clusterId.toString(),
				region: clusterTransports[i].cluster.region,
				data: q.data as LogsPage | undefined,
				isLoading: q.isLoading,
				error: q.error,
			})),
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
		.filter((e): e is Error => e !== undefined);

	return { clusterLogs, mergedEntries, isLoading, errors };
}
