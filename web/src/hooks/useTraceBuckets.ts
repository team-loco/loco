import { useQuery } from "@connectrpc/connect-query";
import { queryTraceBuckets } from "@gen/loco/observability/v1/observability-ObservabilityService_connectquery";
import { msToTimestamp } from "@/lib/time";
import type { TraceScopeOptions } from "./useGetTrace";

interface TraceBucketsOptions extends TraceScopeOptions {
	start: number;
	end: number;
	query: string;
	intervalSeconds: number;
}

export function useTraceBuckets(options: TraceBucketsOptions) {
	const { enabled = true, start, end, workspaceId, environmentId, resourceIds, ...filters } = options;
	return useQuery(
		queryTraceBuckets,
		{
			...filters,
			scope: { workspaceId, environmentId, resourceIds },
			startTime: msToTimestamp(start),
			endTime: msToTimestamp(end),
		},
		{ enabled: enabled && workspaceId !== "" && environmentId !== "", staleTime: Infinity },
	);
}
