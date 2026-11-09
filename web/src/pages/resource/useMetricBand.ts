import { createClient } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import { ObservabilityProxyService } from "@gen/loco/observability/v1/observability_pb";

import {
	bandKey,
	bandRequest,
	mergeBand,
	percentPoints,
	RANGE_MS,
	type BandAggregation,
	type BandPoint,
	type BandTarget,
} from "./metric";

const METRIC_REFRESH_MS = 60_000;

export function useMetricBand(target: BandTarget) {
	const transport = useTransport();
	const query = useQuery({
		queryKey: [
			...createConnectQueryKey({
				schema: ObservabilityProxyService.method.queryMetrics,
				transport,
				cardinality: "finite",
			}),
			...bandKey(target),
		],
		queryFn: async ({ signal }): Promise<{ points: BandPoint[]; from: number; to: number }> => {
			const to = Date.now();
			const client = createClient(ObservabilityProxyService, transport);
			const fetchPoints = async (aggregation: BandAggregation) => {
				const response = await client.queryMetrics(bandRequest(target, aggregation, to), { signal });
				return percentPoints(response.series, target.resourceId);
			};
			const [min, p50, max] = await Promise.all([fetchPoints("min"), fetchPoints("p50"), fetchPoints("max")]);
			return { points: mergeBand({ min, p50, max }), from: to - RANGE_MS[target.range], to };
		},
		enabled: target.workspaceId !== "" && target.environmentId !== "",
		staleTime: METRIC_REFRESH_MS,
		refetchInterval: METRIC_REFRESH_MS,
	});
	return {
		points: query.data?.points ?? [],
		isLoading: query.isLoading,
		error: query.error,
		from: query.data?.from ?? 0,
		to: query.data?.to ?? 1,
	};
}
