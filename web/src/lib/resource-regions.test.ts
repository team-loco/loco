import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { DeploymentSchema } from "@gen/loco/deployment/v1/deployment_pb";
import { ResourceSchema } from "@gen/loco/resource/v1/resource_pb";

import { isWorker } from "./resource-regions";

const resource = (port?: number) =>
	create(ResourceSchema, { spec: { spec: { case: "service", value: { routing: { port } } } } });
const deployment = (port?: number) =>
	create(DeploymentSchema, { spec: { spec: { case: "service", value: { port } } } });

describe("isWorker", () => {
	test("uses the selected environment's deployments instead of the shared resource port", () => {
		expect(isWorker(resource(), [deployment(9090)])).toBe(false);
		expect(isWorker(resource(8080), [deployment()])).toBe(true);
	});
	test("keeps mixed-region services visible regardless of deployment order or port differences", () => {
		expect(isWorker(resource(), [deployment(), deployment(8080), deployment(9090)])).toBe(false);
		expect(isWorker(resource(), [deployment(9090), deployment(8080), deployment()])).toBe(false);
	});
	test("uses resource configuration before the first deployment", () => {
		expect(isWorker(resource(8080), [])).toBe(false);
		expect(isWorker(resource(), [])).toBe(true);
	});
});
