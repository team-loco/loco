import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { DeploymentSchema, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { ClusterAccessSchema, type ClusterAccess } from "@gen/loco/observability/v1/observability_access_pb";
import { MetricPointSchema, MetricSeriesSchema } from "@gen/loco/observability/v1/observability_pb";

import { msToTimestamp } from "@/lib/time";

import { metricProxyUrl, percentPoints, regionMetricKey, regionMetricRequest, type RegionMetricTarget } from "./metric";

const WORKSPACE_ID = "0199d1c2-0000-7000-8000-000000000010";
const RESOURCE_ID = "0199d1c2-0000-7000-8000-000000000001";
const PRODUCTION_ID = "0199d1c2-0000-7000-8000-000000000002";
const STAGING_ID = "0199d1c2-0000-7000-8000-000000000003";
const PRODUCTION_CLUSTER_ID = "0199d1c2-0000-7000-8000-000000000004";
const STAGING_CLUSTER_ID = "0199d1c2-0000-7000-8000-000000000005";
const OTHER_REGION_CLUSTER_ID = "0199d1c2-0000-7000-8000-000000000006";
const REGION = "us-east-1";
const OTHER_REGION = "eu-west-1";
const PRODUCTION_PROXY = "https://obs-prod.us-east-1.example";
const STAGING_PROXY = "https://obs-staging.us-east-1.example";
const OTHER_REGION_PROXY = "https://obs.eu-west-1.example";
const METRIC_NAME = "k8s.pod.cpu_request_utilization";
const NOW_MS = Date.UTC(2026, 9, 11, 12, 0, 0);
const HOUR_MS = 3_600_000;
const MINUTE_S = 60;

function deployment(environmentId: string, clusterId: string): Deployment {
	return create(DeploymentSchema, { resourceId: RESOURCE_ID, environmentId, clusterId, region: REGION });
}

function cluster(clusterId: string, region: string, proxyUrl: string): ClusterAccess {
	return create(ClusterAccessSchema, { clusterId, region, proxyUrl });
}

function target(dep: Deployment): RegionMetricTarget {
	return { workspaceId: WORKSPACE_ID, deployment: dep, range: "1h", metricName: METRIC_NAME };
}

describe("regionMetricRequest", () => {
	test("scopes the query to the deployment's environment", () => {
		const request = regionMetricRequest(target(deployment(PRODUCTION_ID, PRODUCTION_CLUSTER_ID)), NOW_MS);
		expect(request).toEqual({
			workspaceId: WORKSPACE_ID,
			environmentId: PRODUCTION_ID,
			resourceIds: [RESOURCE_ID],
			startTime: msToTimestamp(NOW_MS - HOUR_MS),
			endTime: msToTimestamp(NOW_MS),
			metricName: METRIC_NAME,
			intervalSeconds: MINUTE_S,
			aggregation: "avg",
		});
	});
});

describe("regionMetricKey", () => {
	test("caches each environment separately", () => {
		const production = regionMetricKey(target(deployment(PRODUCTION_ID, PRODUCTION_CLUSTER_ID)), PRODUCTION_PROXY);
		const staging = regionMetricKey(target(deployment(STAGING_ID, PRODUCTION_CLUSTER_ID)), PRODUCTION_PROXY);
		expect(production).toContain(PRODUCTION_ID);
		expect(production).not.toEqual(staging);
	});
});

describe("metricProxyUrl", () => {
	const clusters = [
		cluster(STAGING_CLUSTER_ID, REGION, STAGING_PROXY),
		cluster(PRODUCTION_CLUSTER_ID, REGION, PRODUCTION_PROXY),
	];

	test("picks the cluster running the deployment when a region has several", () => {
		expect(metricProxyUrl(clusters, deployment(PRODUCTION_ID, PRODUCTION_CLUSTER_ID))).toBe(PRODUCTION_PROXY);
		expect(metricProxyUrl(clusters, deployment(STAGING_ID, STAGING_CLUSTER_ID))).toBe(STAGING_PROXY);
	});

	test("does not fall back to the only cluster when it runs elsewhere", () => {
		const onlyOther = [cluster(OTHER_REGION_CLUSTER_ID, OTHER_REGION, OTHER_REGION_PROXY)];
		expect(metricProxyUrl(onlyOther, deployment(PRODUCTION_ID, PRODUCTION_CLUSTER_ID))).toBe("");
	});
});

describe("percentPoints", () => {
	test("keeps the resource's points as percentages in time order", () => {
		const later = create(MetricPointSchema, { timestamp: msToTimestamp(NOW_MS), value: 0.5 });
		const earlier = create(MetricPointSchema, { timestamp: msToTimestamp(NOW_MS - HOUR_MS), value: 0.25 });
		const series = [
			create(MetricSeriesSchema, { resourceId: RESOURCE_ID, points: [later, earlier] }),
			create(MetricSeriesSchema, { resourceId: STAGING_ID, points: [later] }),
		];
		expect(percentPoints(series, RESOURCE_ID)).toEqual([
			{ time: NOW_MS - HOUR_MS, pct: 25 },
			{ time: NOW_MS, pct: 50 },
		]);
	});
});
