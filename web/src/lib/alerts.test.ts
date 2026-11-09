import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { timestampFromMs } from "@bufbuild/protobuf/wkt";
import { DeploymentPhase } from "@gen/loco/deployment/v1/deployment_pb";
import { ResourceSchema, ResourceStatus } from "@gen/loco/resource/v1/resource_pb";

import { alertedResourceCount, buildAlerts } from "./alerts";

const PROD = "env-prod";
const STAGING = "env-staging";
const REPORTED_MS = 1_700_000_000_000;

const api = create(ResourceSchema, {
	id: "r-api",
	name: "api",
	description: "Public API",
	environmentStatuses: [
		{
			environmentId: PROD,
			status: ResourceStatus.DEGRADED,
			regions: [
				{ region: "us-east", status: ResourceStatus.HEALTHY },
				{
					region: "eu-west",
					status: ResourceStatus.DEGRADED,
					message: "back-off restarting failed container",
					reportedAt: timestampFromMs(REPORTED_MS),
				},
				{ region: "ap-south", status: ResourceStatus.UNAVAILABLE },
			],
		},
		{
			environmentId: STAGING,
			regions: [
				{ region: "us-east", status: ResourceStatus.DEPLOYING },
				{ region: "eu-west", status: ResourceStatus.HEALTHY, deploymentStatus: DeploymentPhase.FAILED },
			],
		},
	],
});

const worker = create(ResourceSchema, {
	id: "r-worker",
	name: "worker",
	environmentStatuses: [
		{
			environmentId: PROD,
			regions: [
				{ region: "us-east", status: ResourceStatus.UNKNOWN },
				{ region: "eu-west", status: ResourceStatus.UNSPECIFIED },
			],
		},
	],
});

describe("buildAlerts", () => {
	test("lists each unhealthy region of the selected environment", () => {
		const alerts = buildAlerts([api, worker], PROD);
		expect(alerts.map((a) => a.key)).toEqual(["r-api/eu-west", "r-api/ap-south", "r-worker/us-east"]);
	});

	test("carries the region's message and report time", () => {
		const [first, second] = buildAlerts([api], PROD);
		expect(first).toEqual({
			key: "r-api/eu-west",
			resourceId: "r-api",
			name: "api",
			description: "Public API",
			region: "eu-west",
			status: ResourceStatus.DEGRADED,
			message: "back-off restarting failed container",
			reportedMs: REPORTED_MS,
		});
		expect(second?.message).toBe("");
		expect(second?.reportedMs).toBeUndefined();
	});

	test("ignores other environments and resources without a status there", () => {
		expect(buildAlerts([api, worker], "env-other")).toEqual([]);
	});

	test("alerts on a failed deployment but not on a rollout alone", () => {
		expect(buildAlerts([api, worker], STAGING).map((a) => a.key)).toEqual(["r-api/eu-west"]);
	});
});

describe("alertedResourceCount", () => {
	test("counts resources, not regions", () => {
		expect(alertedResourceCount(buildAlerts([api, worker], PROD))).toBe(2);
	});
});
