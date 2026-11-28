import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { DeploymentSchema } from "@gen/loco/deployment/v1/deployment_pb";
import { ResourceSchema } from "@gen/loco/resource/v1/resource_pb";

import { configuredRegions, isWorker } from "./resource-regions";

const resource = (port?: number) =>
	create(ResourceSchema, {
		environments: [{ environmentId: "production", spec: { spec: { case: "service", value: { routing: { port } } } } }],
	});
const deployment = (port?: number) =>
	create(DeploymentSchema, { spec: { spec: { case: "service", value: { port } } } });

describe("isWorker", () => {
	test("uses the selected environment's deployments instead of the environment's desired port", () => {
		expect(isWorker(resource(), [deployment(9090)], "production")).toBe(false);
		expect(isWorker(resource(8080), [deployment()], "production")).toBe(true);
	});
	test("keeps mixed-region services visible regardless of deployment order or port differences", () => {
		expect(isWorker(resource(), [deployment(), deployment(8080), deployment(9090)], "production")).toBe(false);
		expect(isWorker(resource(), [deployment(9090), deployment(8080), deployment()], "production")).toBe(false);
	});
	test("uses only the selected environment configuration before the first deployment", () => {
		expect(isWorker(resource(8080), [], "production")).toBe(false);
		expect(isWorker(resource(), [], "production")).toBe(true);
		expect(isWorker(resource(), [], "staging")).toBe(false);
	});
});

test("configured regions stay with their environment before any deployment", () => {
	const service = create(ResourceSchema, {
		environments: [
			{
				environmentId: "production",
				spec: {
					spec: { case: "service", value: { regions: { "us-ca-1": { enabled: true }, "us-va-1": { enabled: true } } } },
				},
			},
			{
				environmentId: "staging",
				spec: {
					spec: {
						case: "service",
						value: { regions: { "de-he-1": { enabled: true }, "us-ca-1": { enabled: false } } },
					},
				},
			},
		],
	});
	expect(configuredRegions(service, "production")).toEqual(["us-ca-1", "us-va-1"]);
	expect(configuredRegions(service, "staging")).toEqual(["de-he-1"]);
	expect(configuredRegions(service, "preview")).toEqual([]);
	expect(configuredRegions(service)).toEqual(["de-he-1", "us-ca-1", "us-va-1"]);
});
