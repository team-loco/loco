import { listBuilds } from "@gen/loco/build/v1/build-BuildService_connectquery";
import { LogOrder, type LogEntry } from "@gen/loco/observability/v1/observability_pb";

import { useQueryLogs } from "@/hooks/useQueryLogs";
import { useTokenPage } from "@/hooks/useTokenPage";
import { useTailLogs } from "@/hooks/useTailLogs";
import { msToTimestamp } from "@/lib/time";

import type { BuildLogSource } from "./builds";

export const BUILD_LOG_LIMIT = 1000;
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
		query: `@build:${buildId}`,
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
		return {
			entries: tail.entries.toReversed(),
			truncated: false,
			isLoading: false,
			error: tail.error,
		};
	}
	return {
		entries: query.entries,
		truncated: query.newerCursor !== "",
		isLoading: query.isLoading,
		error: query.error,
	};
}

/** One page of the resource's builds from originEnvironmentId: the page the last of tokens names. */
export function useResourceBuilds(
	resourceId: string,
	originEnvironmentId: string | undefined,
	pageSize: number,
	tokens: readonly string[],
) {
	const { query, nextPageToken, total } = useTokenPage(
		listBuilds,
		{ resourceId, originEnvironmentId: originEnvironmentId ?? "", pageSize },
		tokens,
		{
			enabled: resourceId !== "" && originEnvironmentId !== undefined && originEnvironmentId !== "",
			refetchInterval: REFRESH_MS,
		},
	);
	return {
		builds: query.data?.builds ?? [],
		nextPageToken,
		total,
		isLoading: query.isLoading,
		isFetching: query.isFetching,
		error: query.error,
		refresh: () => {
			void query.refetch();
		},
	};
}
