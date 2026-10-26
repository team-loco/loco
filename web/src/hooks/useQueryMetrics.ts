import { useQuery } from "@tanstack/react-query";
import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { ObservabilityProxyService, type MetricSeries } from "@gen/loco/observability/v1/observability_pb";
import { timeRangeMs, timeRangeIntervalSeconds, type TimeRange } from "@/lib/obs";
import { msToTimestamp } from "@/lib/time";

export interface MetricResult {
	metricName: string;
	series: MetricSeries[];
	isLoading: boolean;
	error: Error | null;
}
interface UseQueryMetricsOptions {
	workspaceId: string;
	resourceIds: string[];
	timeRange: TimeRange;
	metricName: string;
	environmentId: string;
	region?: string;
	aggregation?: string;
	enabled?: boolean;
}
export function metricsQueryOptions(
	transport: Transport,
	{
		workspaceId,
		resourceIds,
		timeRange,
		metricName,
		environmentId,
		region = "",
		aggregation = "avg",
		enabled = true,
	}: UseQueryMetricsOptions,
) {
	const input = {
		workspaceId,
		resourceIds,
		metricName,
		environmentId,
		region,
		aggregation,
		intervalSeconds: timeRangeIntervalSeconds(timeRange),
	};
	return {
		queryKey: [
			...createConnectQueryKey({
				schema: ObservabilityProxyService.method.queryMetrics,
				transport,
				cardinality: "finite",
				input,
			}),
			timeRange,
		],
		queryFn: async ({ signal }: { signal: AbortSignal }) => {
			const now = Date.now();
			const response = await createClient(ObservabilityProxyService, transport).queryMetrics(
				{ ...input, startTime: msToTimestamp(now - timeRangeMs(timeRange)), endTime: msToTimestamp(now) },
				{ signal },
			);
			return response.series;
		},
		enabled: enabled && workspaceId !== "" && environmentId !== "",
		staleTime: 60_000,
		refetchInterval: 60_000,
	};
}

export function useQueryMetrics(options: UseQueryMetricsOptions): MetricResult {
	const query = useQuery(metricsQueryOptions(useTransport(), options));
	return { metricName: options.metricName, series: query.data ?? [], isLoading: query.isLoading, error: query.error };
}
