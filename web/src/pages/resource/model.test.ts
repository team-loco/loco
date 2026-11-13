import { create } from "@bufbuild/protobuf";
import { DeploymentPhase, DeploymentSchema, DeploymentSpecSchema } from "@gen/loco/deployment/v1/deployment_pb";
import { skipToken } from "@connectrpc/connect-query";
import { describe, expect, test } from "bun:test";
import {
	EnvironmentStatusSchema,
	RegionStatusSchema,
	ResourceSchema,
	type EnvironmentStatus,
} from "@gen/loco/resource/v1/resource_pb";

import { msToTimestamp } from "@/lib/time";

import {
	activeDeploymentPlaces,
	buildRegions,
	deploymentsRequest,
	focusRegion,
	lastGoodDeployment,
	statusEnvironmentId,
	tookMs,
	type RegionView,
} from "./model";

const RESOURCE_ID = "0199d1c2-0000-7000-8000-000000000001";
const PRODUCTION_ID = "0199d1c2-0000-7000-8000-000000000002";

function environmentStatus(name: string, ...regions: string[]): EnvironmentStatus {
	return create(EnvironmentStatusSchema, {
		environmentName: name,
		regions: regions.map((region) => create(RegionStatusSchema, { region })),
	});
}

describe("deploymentsRequest", () => {
	test("asks only for the deployments of the active environment", () => {
		expect(deploymentsRequest(RESOURCE_ID, PRODUCTION_ID)).toEqual({
			resourceId: RESOURCE_ID,
			environmentId: PRODUCTION_ID,
			pageSize: 200,
			pageToken: "",
		});
	});

	test("waits for an environment instead of listing every environment", () => {
		expect(deploymentsRequest(RESOURCE_ID, undefined)).toBe(skipToken);
	});

	test("waits for a resource id", () => {
		expect(deploymentsRequest("", PRODUCTION_ID)).toBe(skipToken);
	});
});

describe("buildRegions", () => {
	test("keeps a long-lived current deployment outside the history page", () => {
		const current = create(DeploymentSchema, { id: "current-europe", region: "eu-west-1", isActive: true });
		const history = Array.from({ length: 200 }, (_, i) =>
			create(DeploymentSchema, {
				id: `release-${i.toString()}`,
				region: "us-east-1",
			}),
		);
		const environment = create(EnvironmentStatusSchema, {
			regions: [create(RegionStatusSchema, { region: "eu-west-1", deploymentId: current.id })],
		});
		const regions = buildRegions(create(ResourceSchema), history, environment, [current]);
		const europe = regions.find((region) => region.name === "eu-west-1");
		expect(europe?.current).toEqual(current);
		expect(europe?.history).toEqual([]);
		expect(regions.flatMap((region) => region.history)).toHaveLength(200);
	});

	test("does not substitute an old release for the current deployment", () => {
		const old = create(DeploymentSchema, { id: "old", region: "eu-west-1", isActive: true });
		const environment = create(EnvironmentStatusSchema, {
			regions: [create(RegionStatusSchema, { region: "eu-west-1", deploymentId: "current" })],
		});
		const regions = buildRegions(create(ResourceSchema), [old], environment);
		expect(regions[0]?.current).toBeUndefined();
	});
});

describe("activeDeploymentPlaces", () => {
	test("lists regions alone for one environment", () => {
		const resource = create(ResourceSchema, {
			environmentStatuses: [environmentStatus("production", "us-east-1", "eu-west-1")],
		});
		expect(activeDeploymentPlaces(resource)).toEqual(["us-east-1", "eu-west-1"]);
	});

	test("names the environment of each region when several run", () => {
		const resource = create(ResourceSchema, {
			environmentStatuses: [environmentStatus("production", "us-east-1"), environmentStatus("staging", "us-east-1")],
		});
		expect(activeDeploymentPlaces(resource)).toEqual(["us-east-1 (production)", "us-east-1 (staging)"]);
	});
});

describe("lastGoodDeployment", () => {
	const spec = create(DeploymentSpecSchema);
	const failed = create(DeploymentSchema, { id: "failed", status: DeploymentPhase.FAILED, spec });
	const superseded = create(DeploymentSchema, { id: "superseded", status: DeploymentPhase.SUCCEEDED, spec });
	const older = create(DeploymentSchema, { id: "older", status: DeploymentPhase.SUCCEEDED, spec });

	test("skips the current and failed deployments", () => {
		const region: RegionView = {
			name: "us-east-1",
			status: undefined,
			history: [failed, superseded, older],
			current: failed,
		};
		expect(lastGoodDeployment(region)?.id).toBe("superseded");
	});

	test("finds nothing when the region has one deployment", () => {
		const region: RegionView = { name: "us-east-1", status: undefined, history: [failed], current: failed };
		expect(lastGoodDeployment(region)).toBeUndefined();
	});
});

describe("focusRegion", () => {
	test("picks the healthy region that has a release to roll back to", () => {
		const spec = create(DeploymentSpecSchema);
		const europe = create(DeploymentSchema, { id: "europe", isActive: true, status: DeploymentPhase.RUNNING, spec });
		const current = create(DeploymentSchema, { id: "current", isActive: true, status: DeploymentPhase.RUNNING, spec });
		const previous = create(DeploymentSchema, { id: "previous", status: DeploymentPhase.SUCCEEDED, spec });
		const regions: RegionView[] = [
			{ name: "eu-west-1", status: undefined, history: [europe], current: europe },
			{ name: "us-east-1", status: undefined, history: [current, previous], current },
		];
		expect(focusRegion(regions)?.name).toBe("us-east-1");
	});
});

describe("tookMs", () => {
	test("measures from start to completion", () => {
		const dep = create(DeploymentSchema, { startedAt: msToTimestamp(1_000), completedAt: msToTimestamp(91_000) });
		expect(tookMs(dep)).toBe(90_000);
	});

	test("is unknown until the deployment completes", () => {
		expect(tookMs(create(DeploymentSchema, { startedAt: msToTimestamp(1_000) }))).toBeUndefined();
	});
});

describe("statusEnvironmentId", () => {
	const resource = create(ResourceSchema, {
		environmentStatuses: [
			{ environmentId: "e-prod", environmentName: "production" },
			{ environmentId: "e-stg", environmentName: "staging" },
		],
	});

	test("finds the requested environment among the ones the resource runs in", () => {
		expect(statusEnvironmentId(resource, "staging")).toBe("e-stg");
		expect(statusEnvironmentId(resource, "e-prod")).toBe("e-prod");
		expect(statusEnvironmentId(resource, "preview")).toBeUndefined();
	});

	test("takes the first environment when none is requested", () => {
		expect(statusEnvironmentId(resource, null)).toBe("e-prod");
		expect(statusEnvironmentId(create(ResourceSchema, {}), null)).toBeUndefined();
	});
});
