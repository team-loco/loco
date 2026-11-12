import { useTransport } from "@connectrpc/connect-query";
import { useQueries } from "@tanstack/react-query";

import { metricsQueryOptions } from "@/hooks/useQueryMetrics";
import { TRAFFIC_METRICS, trafficPoints } from "@/lib/traffic";

export function useTraffic(options: {
	workspaceId: string;
	environmentId: string;
	resourceIds: string[];
	spanMs: number;
	region?: string;
}) {
	const transport = useTransport();
	const results = useQueries({
		queries: TRAFFIC_METRICS.map((metric) => metricsQueryOptions(transport, { ...options, ...metric })),
	});
	return {
		points: trafficPoints(results.map((result) => result.data)),
		isLoading: results.some((result) => result.isLoading),
		error: results.find((result) => result.error !== null)?.error ?? null,
	};
}
