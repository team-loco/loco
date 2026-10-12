import { create } from "@bufbuild/protobuf";
import { skipToken } from "@connectrpc/connect-query";
import { describe, expect, test } from "bun:test";
import {
	EnvironmentStatusSchema,
	RegionStatusSchema,
	ResourceSchema,
	type EnvironmentStatus,
} from "@gen/loco/resource/v1/resource_pb";

import { activeDeploymentPlaces, deploymentsRequest } from "./model";

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
		});
	});

	test("waits for an environment instead of listing every environment", () => {
		expect(deploymentsRequest(RESOURCE_ID, undefined)).toBe(skipToken);
	});

	test("waits for a resource id", () => {
		expect(deploymentsRequest("", PRODUCTION_ID)).toBe(skipToken);
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
