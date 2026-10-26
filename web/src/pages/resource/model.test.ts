import { create } from "@bufbuild/protobuf";
import { DeploymentSchema } from "@gen/loco/deployment/v1/deployment_pb";
import { skipToken } from "@connectrpc/connect-query";
import { describe, expect, test } from "bun:test";
import {
	EnvironmentStatusSchema,
	RegionStatusSchema,
	ResourceSchema,
	type EnvironmentStatus,
} from "@gen/loco/resource/v1/resource_pb";

import { activeDeploymentPlaces, buildRegions, deploymentsRequest } from "./model";

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
