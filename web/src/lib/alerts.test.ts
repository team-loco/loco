import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { PlacementStatus } from "@gen/loco/deployment/v1/deployment_pb";
import { ResourceSchema, ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import { alertedResourceCount, buildAlerts } from "./alerts";

const PROD = "env-prod";
const STAGING = "env-staging";
const SINCE_MS = 1_700_000_000_000;

const api = create(ResourceSchema, {
	id: "r-api",
	slug: "api",
	name: "Public API service",
	description: "Public API",
	environmentStatuses: [
		{
			environmentId: PROD,
			status: ResourceStatus.DEGRADED,
			regions: [
				{ region: "us-va-1", status: ResourceStatus.HEALTHY },
				{
					region: "us-ca-1",
					status: ResourceStatus.DEGRADED,
					message: "back-off restarting failed container",
					statusSince: timestampFromMs(SINCE_MS),
				},
				{ region: "de-he-1", status: ResourceStatus.UNAVAILABLE },
			],
		},
		{
			environmentId: STAGING,
			regions: [
				{ region: "us-va-1", status: ResourceStatus.DEPLOYING },
				{ region: "us-ca-1", status: ResourceStatus.HEALTHY, placementStatus: PlacementStatus.FAILED },
			],
		},
	],
});

const worker = create(ResourceSchema, {
	id: "r-worker",
	slug: "worker",
	name: "worker",
	environmentStatuses: [
		{
			environmentId: PROD,
			regions: [
				{ region: "us-va-1", status: ResourceStatus.UNKNOWN },
				{ region: "us-ca-1", status: ResourceStatus.UNSPECIFIED },
			],
		},
	],
});

describe("buildAlerts", () => {
	test("lists each unhealthy region of the selected environment", () => {
		const alerts = buildAlerts([api, worker], PROD);
		expect(alerts.map((a) => a.key)).toEqual(["r-api/us-ca-1", "r-api/de-he-1", "r-worker/us-va-1"]);
	});

	test("carries the region's message and the time its status began", () => {
		const [first, second] = buildAlerts([api], PROD);
		expect(first).toEqual({
			key: "r-api/us-ca-1",
			resourceId: "r-api",
			name: "Public API service",
			slug: "api",
			description: "Public API",
			region: "us-ca-1",
			status: ResourceStatus.DEGRADED,
			rollout: undefined,
			message: "back-off restarting failed container",
			sinceMs: SINCE_MS,
		});
		expect(second?.message).toBe("");
		expect(second?.sinceMs).toBeUndefined();
	});

	test("ignores other environments and resources without a status there", () => {
		expect(buildAlerts([api, worker], "env-other")).toEqual([]);
	});

	test("lists rollouts and failed deployments", () => {
		expect(buildAlerts([api, worker], STAGING).map((a) => a.key)).toEqual(["r-api/us-va-1", "r-api/us-ca-1"]);
	});
});

describe("alertedResourceCount", () => {
	test("counts resources, not regions", () => {
		expect(alertedResourceCount(buildAlerts([api, worker], PROD))).toBe(2);
	});
});
