import { createClient } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import { ObservabilityProxyService } from "@gen/loco/observability/v1/observability_pb";

import {
	percentPoints,
	regionMetricKey,
	regionMetricRequest,
	RANGE_MS,
	type MetricPointPct,
	type RegionMetricTarget,
} from "./metric";

export function useRegionMetric(target: RegionMetricTarget) {
	const transport = useTransport();
	const query = useQuery({
		queryKey: [
			...createConnectQueryKey({
				schema: ObservabilityProxyService.method.queryMetrics,
				transport,
				cardinality: "finite",
			}),
			...regionMetricKey(target),
		],
		queryFn: async ({ signal }): Promise<{ points: MetricPointPct[]; from: number; to: number }> => {
			const to = Date.now();
			const request = regionMetricRequest(target, to);
			const response = await createClient(ObservabilityProxyService, transport).queryMetrics(request, { signal });
			return {
				points: percentPoints(response.series, target.deployment.resourceId),
				from: to - RANGE_MS[target.range],
				to,
			};
		},
		enabled: target.workspaceId !== "",
		staleTime: 60_000,
		refetchInterval: 60_000,
	});
	return {
		points: query.data?.points ?? [],
		isLoading: query.isLoading,
		error: query.error,
		from: query.data?.from ?? 0,
		to: query.data?.to ?? 1,
	};
}
