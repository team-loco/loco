import { useQuery } from "@connectrpc/connect-query";
import { keepPreviousData } from "@tanstack/react-query";
import { queryDeploymentBuckets } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";

import { msToTimestamp } from "@/lib/time";

import { deploymentMarkers, RANGE_MS, type ChartMarker, type MetricRange } from "./metric";
import { REFRESH_MS } from "./model";

const MARKER_BUCKETS = 200;
// nowMs ticks once a minute; the window runs a minute past it to cover the charts' newer end.
const MARKER_LEAD_MS = 60_000;

/** The resource's deployments in the charts' window, as chart markers. */
export function useDeploymentMarkers(target: {
	workspaceId: string;
	environmentId: string;
	resourceId: string;
	range: MetricRange;
	nowMs: number;
}): ChartMarker[] {
	const buckets = useQuery(
		queryDeploymentBuckets,
		{
			workspaceId: target.workspaceId,
			environmentId: target.environmentId,
			resourceIds: [target.resourceId],
			startTime: msToTimestamp(target.nowMs - RANGE_MS[target.range]),
			endTime: msToTimestamp(target.nowMs + MARKER_LEAD_MS),
			bucketCount: MARKER_BUCKETS,
		},
		{ placeholderData: keepPreviousData, refetchInterval: REFRESH_MS },
	);
	return deploymentMarkers(buckets.data?.buckets ?? []);
}
