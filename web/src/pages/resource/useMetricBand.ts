import { useTransport } from "@connectrpc/connect-query";
import { useQueries } from "@tanstack/react-query";

import { metricsQueryOptions } from "@/hooks/useQueryMetrics";

import { BAND_AGGREGATIONS, mergeBand, percentPoints, RANGE_MS, type MetricRange } from "./metric";

export function useMetricBand({
	workspaceId,
	environmentId,
	resourceId,
	region,
	range,
	metricName,
}: {
	workspaceId: string;
	environmentId: string;
	resourceId: string;
	region: string;
	range: MetricRange;
	metricName: string;
}) {
	const transport = useTransport();
	const results = useQueries({
		queries: BAND_AGGREGATIONS.map((aggregation) =>
			metricsQueryOptions(transport, {
				workspaceId,
				resourceIds: [resourceId],
				spanMs: RANGE_MS[range],
				metricName,
				environmentId,
				region,
				aggregation,
			}),
		),
	});
	const [min = [], p50 = [], max = []] = results.map((r) => percentPoints(r.data ?? [], resourceId));
	const loaded = results.every((r) => r.data !== undefined);
	const to = Math.max(...results.map((r) => r.dataUpdatedAt));
	return {
		points: loaded ? mergeBand({ min, p50, max }) : [],
		isLoading: results.some((r) => r.isLoading),
		error: results.find((r) => r.error !== null)?.error ?? null,
		from: loaded ? to - RANGE_MS[range] : 0,
		to: loaded ? to : 1,
	};
}
