import { useQuery } from "@connectrpc/connect-query";
import { queryLogBuckets } from "@gen/loco/observability/v1/observability-ObservabilityService_connectquery";
import { LogBucketGroup } from "@gen/loco/observability/v1/observability_pb";
import { msToTimestamp } from "@/lib/time";

export function useLogBuckets(options: {
	workspaceId: string;
	environmentId: string;
	allEnvironments: boolean;
	resourceIds: string[];
	query: string;
	start: number;
	end: number;
	intervalSeconds: number;
	groupBy?: LogBucketGroup;
	enabled: boolean;
}) {
	const { start, end, enabled, allEnvironments, environmentId, groupBy = LogBucketGroup.LEVEL, ...filters } = options;
	return useQuery(
		queryLogBuckets,
		{
			...filters,
			environmentId: allEnvironments ? "" : environmentId,
			startTime: msToTimestamp(start),
			endTime: msToTimestamp(end),
			groupBy,
		},
		{
			enabled: enabled && filters.workspaceId !== "" && (allEnvironments || environmentId !== ""),
			staleTime: Infinity,
		},
	);
}
