import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { DeploymentBucketSchema } from "@gen/loco/deployment/v1/deployment_pb";
import { MetricPointSchema, MetricSeriesSchema } from "@gen/loco/observability/v1/observability_pb";

import { msToTimestamp } from "@/lib/time";

import { deploymentMarkers, percentPoints, sharedLimit } from "./metric";

const RESOURCE_ID = "0199d1c2-0000-7000-8000-000000000001";
const STAGING_ID = "0199d1c2-0000-7000-8000-000000000003";
const NOW_MS = Date.UTC(2026, 9, 11, 12, 0, 0);
const HOUR_MS = 3_600_000;

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

test("uses absolute units only when every charted region has the same size", () => {
	expect(sharedLimit([500, 500])).toBe(500);
	expect(sharedLimit([500, 1000])).toBeUndefined();
	expect(sharedLimit([500, 0])).toBeUndefined();
	expect(sharedLimit([])).toBeUndefined();
});

describe("deploymentMarkers", () => {
	test("marks each bucket at its mean time, naming the version of a lone deployment", () => {
		const buckets = [
			create(DeploymentBucketSchema, {
				deploymentCount: 1,
				meanCreatedAt: msToTimestamp(NOW_MS - 60_000),
				onlyDeployment: { image: "example.test/app:v2" },
			}),
			create(DeploymentBucketSchema, { deploymentCount: 3, meanCreatedAt: msToTimestamp(NOW_MS) }),
			create(DeploymentBucketSchema, { deploymentCount: 1, meanCreatedAt: msToTimestamp(NOW_MS), onlyDeployment: {} }),
		];
		expect(deploymentMarkers(buckets)).toEqual([
			{ ms: NOW_MS - 60_000, label: "deployed v2" },
			{ ms: NOW_MS, label: "3 deployments" },
			{ ms: NOW_MS, label: "deployed —" },
		]);
	});
});
