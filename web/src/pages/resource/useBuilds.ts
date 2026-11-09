import { skipToken, useInfiniteQuery } from "@connectrpc/connect-query";
import { listBuilds } from "@gen/loco/build/v1/build-BuildService_connectquery";
import { LogOrder, type LogEntry } from "@gen/loco/observability/v1/observability_pb";

import { useQueryLogs } from "@/hooks/useQueryLogs";
import { useTailLogs } from "@/hooks/useTailLogs";
import { msToTimestamp } from "@/lib/time";
import { BUILD_ID_LABEL } from "@/pages/observability/query";

import type { BuildLogSource } from "./builds";

export const BUILD_LOG_LIMIT = 1000;
const BUILDS_PAGE_SIZE = 50;
const REFRESH_MS = 15_000;

export interface BuildLog {
	entries: LogEntry[];
	truncated: boolean;
	isLoading: boolean;
	error: Error | null;
}

export function useBuildLog(workspaceId: string, buildId: string, source: BuildLogSource): BuildLog {
	const filter = {
		workspaceId,
		environmentId: "",
		allEnvironments: true,
		resourceIds: [],
		parsedQuery: { search: "", levels: [], labels: { [BUILD_ID_LABEL]: buildId } },
	};
	const range = source.kind === "query" ? source : { startMs: 0, endMs: 0 };
	const query = useQueryLogs({
		...filter,
		spanMs: range.endMs - range.startMs,
		limit: BUILD_LOG_LIMIT,
		order: LogOrder.OLDEST_FIRST,
		startTime: msToTimestamp(range.startMs),
		endTime: msToTimestamp(range.endMs),
		enabled: source.kind === "query",
	});
	const tail = useTailLogs({
		...filter,
		sinceMs: source.kind === "tail" ? source.sinceMs : undefined,
		enabled: source.kind === "tail",
	});
	if (source.kind === "tail") {
		return { entries: tail.entries.toReversed(), truncated: false, isLoading: false, error: tail.error };
	}
	return {
		entries: query.entries,
		truncated: query.newerCursor !== "",
		isLoading: query.isLoading,
		error: query.error,
	};
}

export function useResourceBuilds(resourceId: string) {
	const query = useInfiniteQuery(
		listBuilds,
		resourceId === "" ? skipToken : { resourceId, pageSize: BUILDS_PAGE_SIZE, pageToken: "" },
		{
			pageParamKey: "pageToken",
			getNextPageParam: (last) => (last.nextPageToken === "" ? undefined : last.nextPageToken),
			refetchInterval: REFRESH_MS,
		},
	);
	return {
		builds: query.data?.pages.flatMap((page) => page.builds) ?? [],
		hasMore: query.hasNextPage,
		isLoading: query.isLoading,
		isLoadingMore: query.isFetchingNextPage,
		error: query.error,
		loadMore: async () => {
			const result = await query.fetchNextPage();
			return !result.isError;
		},
		refresh: () => {
			void query.refetch();
		},
	};
}
