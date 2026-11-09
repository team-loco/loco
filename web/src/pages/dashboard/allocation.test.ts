import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { DeploymentSchema, type Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { EnvironmentStatusSchema, ResourceSchema } from "@gen/loco/resource/v1/resource_pb";

import { allocation } from "./allocation";

function deployment(id: string, cpu: string, memory: string): Deployment {
	return create(DeploymentSchema, {
		id,
		spec: { spec: { case: "service", value: { cpu, memory } } },
	});
}

function item(name: string, regions: { deploymentId: string; desiredReplicas: number }[], deployments: Deployment[]) {
	return {
		resource: create(ResourceSchema, { id: name, name }),
		environment: create(EnvironmentStatusSchema, { regions }),
		envDeployments: deployments,
	};
}

describe("allocation", () => {
	test("multiplies each region's active spec by its desired replicas and sums across resources", () => {
		const web = item(
			"web",
			[
				{ deploymentId: "web-us", desiredReplicas: 2 },
				{ deploymentId: "web-eu", desiredReplicas: 3 },
			],
			[deployment("web-us", "500m", "512Mi"), deployment("web-eu", "1", "1Gi"), deployment("web-old", "4", "8Gi")],
		);
		const api = item("api", [{ deploymentId: "api-us", desiredReplicas: 1 }], [deployment("api-us", "250m", "256Mi")]);

		const got = allocation([api, web]);

		expect(got.cpuMilli).toBe(2 * 500 + 3 * 1000 + 250);
		expect(got.memMi).toBe(2 * 512 + 3 * 1024 + 256);
		expect(got.resources.map((r) => [r.name, r.cpuMilli, r.memMi])).toEqual([
			["web", 4000, 4096],
			["api", 250, 256],
		]);
	});

	test("resources with no desired replicas are left out of the breakdown", () => {
		const idle = item("idle", [{ deploymentId: "idle-us", desiredReplicas: 0 }], [deployment("idle-us", "1", "1Gi")]);
		const never = { ...item("never", [], []), environment: undefined };

		const got = allocation([idle, never]);

		expect(got.cpuMilli).toBe(0);
		expect(got.resources).toEqual([]);
	});
});
