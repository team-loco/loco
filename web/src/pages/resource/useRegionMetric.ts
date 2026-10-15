import { createClient } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import { ObservabilityProxyService } from "@gen/loco/observability/v1/observability_pb";

import { percentPoints, regionMetricKey, regionMetricRequest,
	type MetricPointPct, type RegionMetricTarget } from "./metric";

export function useRegionMetric(target: RegionMetricTarget) {
	const transport = useTransport();
	const query = useQuery({
		queryKey: [...createConnectQueryKey({ schema: ObservabilityProxyService.method.queryMetrics,
			transport, cardinality: "finite" }), ...regionMetricKey(target)],
		queryFn: async ({ signal }): Promise<MetricPointPct[]> => {
			const response = await createClient(ObservabilityProxyService, transport).queryMetrics(
				regionMetricRequest(target, Date.now()), { signal });
			return percentPoints(response.series, target.deployment.resourceId);
		},
		enabled: target.workspaceId !== "", staleTime: 60_000, refetchInterval: 60_000,
	});
	return { points: query.data ?? [], isLoading: query.isLoading, unavailable: false, error: query.error };
}
