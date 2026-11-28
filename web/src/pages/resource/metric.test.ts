import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { DeploymentBucketSchema } from "@gen/loco/deployment/v1/deployment_pb";
import { MetricSeriesSchema } from "@gen/loco/observability/v1/observability_pb";

import { msToTimestamp } from "@/lib/time";

import { allocationRows, deploymentMarkers, usageRows } from "./metric";

const RESOURCE_ID = "0199d1c2-0000-7000-8000-000000000001";
const STAGING_ID = "0199d1c2-0000-7000-8000-000000000003";
const NOW_MS = Date.UTC(2026, 9, 11, 12, 0, 0);
const HOUR_MS = 3_600_000;

describe("absolute usage", () => {
	test("filters the requested resource and sorts CPU readings in millicores", () => {
		const series = [
			create(MetricSeriesSchema, {
				resourceId: RESOURCE_ID,
				points: [
					{ timestamp: msToTimestamp(NOW_MS), value: 0.05 },
					{ timestamp: msToTimestamp(NOW_MS - HOUR_MS), value: 0.05 },
				],
			}),
			create(MetricSeriesSchema, { resourceId: STAGING_ID, points: [{ timestamp: msToTimestamp(NOW_MS), value: 9 }] }),
		];
		expect(usageRows([{ region: "east", values: series }], RESOURCE_ID, 1000)).toEqual([
			{ time: NOW_MS - HOUR_MS, east: 50 },
			{ time: NOW_MS, east: 50 },
		]);
	});

	test("converts memory bytes to Mi without a deployment allocation", () => {
		const values = [
			create(MetricSeriesSchema, {
				resourceId: RESOURCE_ID,
				points: [{ timestamp: msToTimestamp(NOW_MS), value: 128 * 1024 * 1024 }],
			}),
		];
		expect(usageRows([{ region: "east", values }], RESOURCE_ID, 1 / (1024 * 1024))).toEqual([
			{ time: NOW_MS, east: 128 },
		]);
	});
});

describe("allocationRows", () => {
	test("leaves unknown history blank and starts the known allocation at its actual start", () => {
		const start = NOW_MS - HOUR_MS;
		const resized = NOW_MS - 600_000;
		expect(allocationRows(new Map([["east", [{ from: resized, limit: 200 }]]]), start, NOW_MS)).toEqual([
			{ time: start, limit: undefined },
			{ time: resized, limit: 200 },
			{ time: NOW_MS, limit: 200 },
		]);
	});

	test("preserves an earlier known allocation and only shares matching regional limits", () => {
		const start = NOW_MS - HOUR_MS;
		const resized = NOW_MS - 600_000;
		const limits = new Map([
			[
				"east",
				[
					{ from: start - HOUR_MS, limit: 100 },
					{ from: resized, limit: 200 },
				],
			],
			["west", [{ from: start - HOUR_MS, limit: 100 }]],
		]);
		expect(allocationRows(limits, start, NOW_MS)).toEqual([
			{ time: start, limit: 100 },
			{ time: resized, limit: 100 },
			{ time: resized, limit: undefined },
			{ time: NOW_MS, limit: undefined },
		]);
	});
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
