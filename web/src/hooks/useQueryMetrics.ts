import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey } from "@connectrpc/connect-query";
import { ObservabilityProxyService } from "@gen/loco/observability/v1/observability_pb";
import { stepSeconds } from "@/lib/obs-time";
import { msToTimestamp } from "@/lib/time";

const REFRESH_MS = 60_000;

interface UseQueryMetricsOptions {
	workspaceId: string;
	resourceIds: string[];
	spanMs: number;
	endMs?: number | undefined;
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
		spanMs,
		endMs,
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
		intervalSeconds: stepSeconds(spanMs),
	};
	return {
		queryKey: [
			...createConnectQueryKey({
				schema: ObservabilityProxyService.method.queryMetrics,
				transport,
				cardinality: "finite",
				input,
			}),
			spanMs,
			endMs ?? null,
		],
		queryFn: async ({ signal }: { signal: AbortSignal }) => {
			const end = endMs ?? Date.now();
			const response = await createClient(ObservabilityProxyService, transport).queryMetrics(
				{ ...input, startTime: msToTimestamp(end - spanMs), endTime: msToTimestamp(end) },
				{ signal },
			);
			return response.series;
		},
		enabled: enabled && workspaceId !== "" && environmentId !== "",
		staleTime: REFRESH_MS,
		refetchInterval: endMs === undefined ? REFRESH_MS : (false as const),
	};
}
