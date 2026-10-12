import { useQuery } from "@tanstack/react-query";

import { useObsAccess } from "@/hooks/useObsAccess";

import {
	metricProxyUrl,
	percentPoints,
	regionMetricKey,
	regionMetricRequest,
	type MetricPointPct,
	type RegionMetricTarget,
} from "./metric";
import { proxyClient } from "./proxy";

export function useRegionMetric(target: RegionMetricTarget) {
	const { workspaceId, deployment } = target;
	const { data: access, isLoading: accessLoading } = useObsAccess(workspaceId);
	const proxyUrl = metricProxyUrl(access?.clusters ?? [], deployment);

	const query = useQuery({
		queryKey: regionMetricKey(target, proxyUrl),
		queryFn: async (): Promise<MetricPointPct[]> => {
			const client = proxyClient(proxyUrl);
			const request = regionMetricRequest(target, Date.now());
			const resp = await client.queryMetrics(request);
			return percentPoints(resp.series, deployment.resourceId);
		},
		enabled: proxyUrl !== "" && workspaceId !== "",
		staleTime: 60_000,
		refetchInterval: 60_000,
	});

	return {
		points: query.data ?? [],
		isLoading: accessLoading || query.isLoading,
		unavailable: !accessLoading && proxyUrl === "",
		error: query.error,
	};
}
