import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import {
	DeploymentPhase,
	DeploymentSchema,
	DeploymentSpecSchema,
	HealthCheckConfigSchema,
	ServiceDeploymentSpecSchema,
	type Deployment,
} from "@gen/loco/deployment/v1/deployment_pb";

import { getServiceSpec } from "@/lib/deployment-utils";

import type { RegionView } from "./model";
import { baseline, changeCount, planSave, validateDraft, type SettingsDraft } from "./settings-draft";

const EAST = "us-east-1";
const WEST = "eu-west-1";
const IMAGE = "ghcr.io/acme/shop:1.4.0";
const SERVED = ["loco.example.com"];

function deployment(id: string, regionName: string, replicas = 2): Deployment {
	return create(DeploymentSchema, {
		id,
		region: regionName,
		replicas,
		isActive: true,
		commit: { sha: "a".repeat(40), dirty: true },
		status: DeploymentPhase.RUNNING,
		spec: create(DeploymentSpecSchema, {
			spec: {
				case: "service",
				value: create(ServiceDeploymentSpecSchema, {
					build: { type: "image", image: IMAGE },
					cpu: "250m",
					memory: "512Mi",
					minReplicas: replicas,
					maxReplicas: replicas,
					port: 8080,
					env: { LOG_LEVEL: "info" },
					healthCheck: create(HealthCheckConfigSchema, {
						path: "/healthz",
						intervalSeconds: 10,
						timeoutSeconds: 2,
						failureThreshold: 3,
					}),
				}),
			},
		}),
	});
}

function region(name: string, current: Deployment | undefined): RegionView {
	return { name, status: undefined, history: current === undefined ? [] : [current], current };
}

const regions = [region(EAST, deployment("east", EAST)), region(WEST, deployment("west", WEST, 3))];
const base = baseline("shop", regions, true, undefined);

function plan(draft: SettingsDraft) {
	return planSave(regions, draft, base);
}

describe("planSave", () => {
	test("preserves the source commit for sizing changes and clears it for another image", () => {
		expect(plan({ cpu: "1" }).deploy[0]?.commit).toEqual(regions[0]?.current?.commit);
		expect(plan({ cpu: "1", image: IMAGE }).deploy[0]?.commit).toEqual(regions[0]?.current?.commit);
		expect(plan({ image: "example.test/another:release" }).deploy[0]?.commit).toBeUndefined();
	});

	test("does nothing for an empty draft", () => {
		expect(plan({})).toEqual({ deploy: [], retire: [] });
	});

	test("carries every spec change into one deployment per region", () => {
		const result = plan({ image: "ghcr.io/acme/shop:1.5.0", cpu: "500m", healthPath: "/ready" });
		expect(result.deploy.map((d) => d.region)).toEqual([EAST, WEST]);
		const east = result.deploy[0];
		const svc = east === undefined ? undefined : getServiceSpec(create(DeploymentSchema, { spec: east.spec }));
		expect(svc?.build?.image).toBe("ghcr.io/acme/shop:1.5.0");
		expect(svc?.cpu).toBe("500m");
		expect(svc?.memory).toBe("512Mi");
		expect(svc?.env).toEqual({ LOG_LEVEL: "info" });
		expect(svc?.healthCheck?.path).toBe("/ready");
		expect(svc?.healthCheck?.intervalSeconds).toBe(10);
	});

	test("redeploys only the region whose replicas changed", () => {
		const result = plan({ replicas: { [WEST]: 5 } });
		expect(result.deploy.map((d) => d.region)).toEqual([WEST]);
		const svc = getServiceSpec(create(DeploymentSchema, { spec: result.deploy[0]?.spec }));
		expect(svc?.minReplicas).toBe(5);
		expect(svc?.maxReplicas).toBe(5);
	});

	test("skips a value typed back to what runs", () => {
		expect(plan({ cpu: "250m", replicas: { [EAST]: 2 } })).toEqual({ deploy: [], retire: [] });
	});

	test("retires a region switched off and adds one switched on from the first region's spec", () => {
		const withSpare = [...regions, region("ap-south-1", undefined)];
		const spareBase = baseline("shop", withSpare, true, undefined);
		const result = planSave(withSpare, { regions: { [WEST]: false, "ap-south-1": true } }, spareBase);
		expect(result.retire).toEqual([{ region: WEST, deploymentId: "west" }]);
		expect(result.deploy.map((d) => [d.region, d.added])).toEqual([["ap-south-1", true]]);
	});

	test("redeploys every region when the resource goes private", () => {
		expect(plan({ isPublic: false }).deploy.map((d) => d.region)).toEqual([EAST, WEST]);
	});
});

describe("changeCount", () => {
	test("counts fields that differ from what runs", () => {
		expect(changeCount({ cpu: "250m" }, base)).toBe(0);
		expect(changeCount({ cpu: "1", replicas: { [EAST]: 4 }, secrets: { API_KEY: "x" } }, base)).toBe(3);
	});
});

describe("validateDraft", () => {
	test("reports only the fields the draft touched", () => {
		expect(validateDraft({}, base, SERVED)).toEqual({});
		expect(validateDraft({ port: "http", healthPath: "ready" }, base, SERVED)).toEqual({
			port: "Enter a port number",
			healthPath: "Start the path with /",
		});
	});

	test("keeps at least one region", () => {
		expect(validateDraft({ regions: { [EAST]: false, [WEST]: false } }, base, SERVED).regions).toBe(
			"Keep at least one region",
		);
	});

	test("needs a served platform domain and a valid subdomain to go public", () => {
		const privateBase = baseline("shop", regions, false, undefined);
		expect(
			validateDraft({ isPublic: true, subdomain: "Shop!", platformDomain: "other.dev" }, privateBase, SERVED),
		).toEqual({
			platformDomain: "No platform domain is served in every region",
			subdomain: "Use lowercase letters, digits and hyphens",
		});
	});
});
