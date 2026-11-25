import { createClient, type Transport } from "@connectrpc/connect";
import { keepPreviousData } from "@tanstack/react-query";
import { createConnectQueryKey } from "@connectrpc/connect-query";
import { ObservabilityService } from "@gen/loco/observability/v1/observability_pb";
import { stepSeconds } from "@/lib/obs-time";
import { msToTimestamp } from "@/lib/time";

import { scopeReady } from "./observabilityScope";

const REFRESH_MS = 60_000;

interface UseQueryMetricsOptions {
	workspaceId: string;
	installation?: boolean;
	resourceIds: string[];
	spanMs: number;
	endMs?: number | undefined;
	metricName: string;
	environmentId: string;
	region?: string;
	aggregation?: string;
	attributes?: Record<string, string>;
	byPod?: boolean;
	enabled?: boolean;
}
export function metricsQueryOptions(
	transport: Transport,
	{
		workspaceId,
		installation = false,
		resourceIds,
		spanMs,
		endMs,
		metricName,
		environmentId,
		region = "",
		aggregation = "avg",
		attributes = {},
		byPod = false,
		enabled = true,
	}: UseQueryMetricsOptions,
) {
	const input = {
		workspaceId,
		installation,
		resourceIds,
		metricName,
		environmentId,
		region,
		aggregation,
		attributes,
		byPod,
		intervalSeconds: stepSeconds(spanMs),
	};
	return {
		queryKey: [
			...createConnectQueryKey({
				schema: ObservabilityService.method.queryMetrics,
				transport,
				cardinality: "finite",
				input,
			}),
			spanMs,
			endMs ?? null,
		],
		queryFn: async ({ signal }: { signal: AbortSignal }) => {
			const end = endMs ?? Date.now();
			const response = await createClient(ObservabilityService, transport).queryMetrics(
				{
					...input,
					startTime: msToTimestamp(end - spanMs),
					endTime: msToTimestamp(end),
				},
				{ signal },
			);
			return response.series;
		},
		enabled: enabled && scopeReady({ workspaceId, environmentId, installation }),
		staleTime: REFRESH_MS,
		placeholderData: keepPreviousData,
		refetchInterval: endMs === undefined ? REFRESH_MS : (false as const),
	};
}
