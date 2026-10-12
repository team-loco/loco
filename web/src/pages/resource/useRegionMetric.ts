import { createClient, type Transport } from "@connectrpc/connect";
import { useQuery } from "@tanstack/react-query";
import { ObservabilityProxyService } from "@gen/loco/observability/v1/observability_pb";

import { createTransport } from "@/auth/connect-transport";
import { useObsAccess } from "@/hooks/useObsAccess";

import {
	metricProxyUrl,
	percentPoints,
	regionMetricKey,
	regionMetricRequest,
	type MetricPointPct,
	type RegionMetricTarget,
} from "./metric";

const transports = new Map<string, Transport>();

function transportFor(url: string): Transport {
	const cached = transports.get(url);
	if (cached !== undefined) return cached;
	const t = createTransport(url);
	transports.set(url, t);
	return t;
}

export function useRegionMetric(target: RegionMetricTarget) {
	const { workspaceId, deployment } = target;
	const { data: access, isLoading: accessLoading } = useObsAccess(workspaceId);
	const proxyUrl = metricProxyUrl(access?.clusters ?? [], deployment);

	const query = useQuery({
		queryKey: regionMetricKey(target, proxyUrl),
		queryFn: async (): Promise<MetricPointPct[]> => {
			const client = createClient(ObservabilityProxyService, transportFor(proxyUrl));
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
