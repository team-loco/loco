import type { MessageInitShape } from "@bufbuild/protobuf";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import type { ClusterAccess } from "@gen/loco/observability/v1/observability_access_pb";
import type { MetricSeries, QueryMetricsRequestSchema } from "@gen/loco/observability/v1/observability_pb";

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

const PERCENT = 100;

export interface MetricPointPct {
	time: number;
	pct: number;
}

export interface RegionMetricTarget {
	workspaceId: string;
	deployment: Deployment;
	range: MetricRange;
	metricName: string;
}

export function metricProxyUrl(clusters: ClusterAccess[], deployment: Deployment): string {
	return clusters.find((c) => c.clusterId === deployment.clusterId)?.proxyUrl ?? "";
}

export function regionMetricKey(target: RegionMetricTarget, proxyUrl: string) {
	const { workspaceId, deployment, range, metricName } = target;
	return ["resource-metric", proxyUrl, workspaceId, deployment.environmentId, deployment.resourceId, range, metricName];
}

export function regionMetricRequest(
	target: RegionMetricTarget,
	nowMs: number,
): MessageInitShape<typeof QueryMetricsRequestSchema> {
	const { workspaceId, deployment, range, metricName } = target;
	return {
		workspaceId,
		environmentId: deployment.environmentId,
		resourceIds: [deployment.resourceId],
		startTime: msToTimestamp(nowMs - RANGE_MS[range]),
		endTime: msToTimestamp(nowMs),
		metricName,
		intervalSeconds: RANGE_INTERVAL[range],
		aggregation: "avg",
	};
}

export function percentPoints(series: MetricSeries[], resourceId: string): MetricPointPct[] {
	const points: MetricPointPct[] = [];
	for (const s of series) {
		if (s.resourceId !== "" && s.resourceId !== resourceId) continue;
		for (const p of s.points) {
			const time = maybeTsMs(p.timestamp);
			if (time !== undefined) points.push({ time, pct: p.value * PERCENT });
		}
	}
	return points.sort((a, b) => a.time - b.time);
}
