import { useMemo } from "react";
import { useQueries } from "@tanstack/react-query";
import { createClient } from "@connectrpc/connect";
import { ObservabilityProxyService } from "@/gen/loco/observability/v1/observability_pb";
import type { MetricSeries } from "@/gen/loco/observability/v1/observability_pb";
import { create } from "@bufbuild/protobuf";
import { TimestampSchema } from "@bufbuild/protobuf/wkt";
import type { ClusterTransport } from "@/components/observability/ObsProvider";
import {
	timeRangeMs,
	timeRangeIntervalSeconds,
	type TimeRange,
} from "@/components/observability/ObsProvider";

function dateToTimestamp(date: Date) {
	return create(TimestampSchema, {
		seconds: BigInt(Math.floor(date.getTime() / 1000)),
		nanos: 0,
	});
}

export interface MetricResult {
	metricName: string;
	series: MetricSeries[];
	isLoading: boolean;
	error: Error | null;
}

interface UseQueryMetricsOptions {
	clusterTransports: ClusterTransport[];
	workspaceId: string;
	resourceIds: string[];
	timeRange: TimeRange;
	metricName: string;
	aggregation?: string;
	enabled?: boolean;
}

export function useQueryMetrics({
	clusterTransports,
	workspaceId,
	resourceIds,
	timeRange,
	metricName,
	aggregation = "avg",
	enabled = true,
}: UseQueryMetricsOptions): MetricResult {
	const now = useMemo(() => Date.now(), [timeRange]); // eslint-disable-line react-hooks/exhaustive-deps
	const startTime = dateToTimestamp(new Date(now - timeRangeMs(timeRange)));
	const endTime = dateToTimestamp(new Date(now));
	const intervalSeconds = timeRangeIntervalSeconds(timeRange);

	const queries = useQueries({
		queries: clusterTransports.map(({ cluster, transport }) => ({
			queryKey: [
				"obs-metrics",
				cluster.clusterId.toString(),
				workspaceId,
				resourceIds,
				timeRange,
				metricName,
				aggregation,
			],
			queryFn: async () => {
				const client = createClient(ObservabilityProxyService, transport);
				const resp = await client.queryMetrics({
					workspaceId,
					resourceIds,
					startTime,
					endTime,
					metricName,
					intervalSeconds,
					aggregation,
				});
				return resp.series;
			},
			enabled: enabled && !!workspaceId && resourceIds.length > 0 && clusterTransports.length > 0,
			staleTime: 60_000,
			refetchInterval: 60_000,
		})),
	});

	const series = useMemo(() => {
		const all: MetricSeries[] = [];
		for (const q of queries) {
			if (q.data) all.push(...q.data);
		}
		return all;
	}, [queries]);

	return {
		metricName,
		series,
		isLoading: queries.some((q) => q.isLoading),
		error: (queries.find((q) => q.error)?.error as Error | null) ?? null,
	};
}
