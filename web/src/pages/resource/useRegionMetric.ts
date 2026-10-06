import { createClient, type Transport } from "@connectrpc/connect";
import { useQuery } from "@tanstack/react-query";
import { ObservabilityProxyService } from "@gen/loco/observability/v1/observability_pb";

import { createTransport } from "@/auth/connect-transport";
import { useObsAccess } from "@/hooks/useObsAccess";
import { maybeTsMs, msToTimestamp } from "@/lib/time";

export type MetricRange = "1h" | "6h" | "24h";

export const METRIC_RANGES: MetricRange[] = ["1h", "6h", "24h"];

const RANGE_MS: Record<MetricRange, number> = {
	"1h": 3_600_000,
	"6h": 21_600_000,
	"24h": 86_400_000,
};

const RANGE_INTERVAL: Record<MetricRange, number> = {
	"1h": 60,
	"6h": 300,
	"24h": 900,
};

const transports = new Map<string, Transport>();

function transportFor(url: string): Transport {
	const cached = transports.get(url);
	if (cached !== undefined) return cached;
	const t = createTransport(url);
	transports.set(url, t);
	return t;
}

export interface MetricPointPct {
	time: number;
	pct: number;
}

export function useRegionMetric({
	workspaceId,
	resourceId,
	region,
	range,
	metricName,
}: {
	workspaceId: string;
	resourceId: string;
	region: string;
	range: MetricRange;
	metricName: string;
}) {
	const { data: access, isLoading: accessLoading } = useObsAccess(workspaceId);
	const clusters = access?.clusters ?? [];
	const cluster = clusters.find((c) => c.region === region) ?? (clusters.length === 1 ? clusters[0] : undefined);
	const proxyUrl = cluster?.proxyUrl ?? "";

	const query = useQuery({
		queryKey: ["resource-metric", proxyUrl, workspaceId, resourceId, range, metricName],
		queryFn: async (): Promise<MetricPointPct[]> => {
			const now = Date.now();
			const client = createClient(ObservabilityProxyService, transportFor(proxyUrl));
			const resp = await client.queryMetrics({
				workspaceId,
				resourceIds: [resourceId],
				startTime: msToTimestamp(now - RANGE_MS[range]),
				endTime: msToTimestamp(now),
				metricName,
				intervalSeconds: RANGE_INTERVAL[range],
				aggregation: "avg",
			});
			const points: MetricPointPct[] = [];
			for (const s of resp.series) {
				if (s.resourceId !== "" && s.resourceId !== resourceId) continue;
				for (const p of s.points) {
					const time = maybeTsMs(p.timestamp);
					if (time !== undefined) points.push({ time, pct: p.value * 100 });
				}
			}
			return points.sort((a, b) => a.time - b.time);
		},
		enabled: proxyUrl !== "" && workspaceId !== "" && resourceId !== "",
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
