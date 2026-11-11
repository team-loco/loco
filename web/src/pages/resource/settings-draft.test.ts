import { DefaultServiceConfigSchema } from "@gen/loco/config/v1/config_pb";
import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import {
	DeploymentPhase,
	DeploymentSchema,
	DeploymentSpecSchema,
	HealthCheckConfigSchema,
	ServiceDeploymentSpecSchema,
	ScalersSchema,
	type Deployment,
} from "@gen/loco/deployment/v1/deployment_pb";

import { getServiceSpec } from "@/lib/deployment-utils";

import type { RegionView } from "./model";
import { baseline, changeCount, planSave, savedScaling, validateDraft, type SettingsDraft } from "./settings-draft";

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

const defaults = create(DefaultServiceConfigSchema, { cpuTarget: 65, minReplicas: 1, maxReplicas: 1 });
const scaling = (replicas: number, maxReplicas = replicas, cpuTarget = defaults.cpuTarget) => ({
	replicas,
	maxReplicas,
	cpuTarget,
});

const regions = [region(EAST, deployment("east", EAST)), region(WEST, deployment("west", WEST, 3))];
const base = baseline("shop", "", regions, true, defaults);

function plan(draft: SettingsDraft) {
	return planSave(regions, draft, base);
}

describe("planSave", () => {
	test("changing one region's image clears its own source commit", () => {
		const first = deployment("one", EAST);
		const second = deployment("two", WEST);
		getServiceSpec(second)!.build!.image = "example.test/other:release";
		const mixed = [region(EAST, first), region(WEST, second)];
		const result = planSave(mixed, { image: IMAGE }, baseline("shop", "", mixed, true, defaults));
		expect(result.deploy).toHaveLength(1);
		expect(result.deploy[0]?.region).toBe(WEST);
		expect(result.deploy[0]?.commit).toBeUndefined();
	});

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
		const result = plan({ scaling: { [WEST]: scaling(5) } });
		expect(result.deploy.map((d) => d.region)).toEqual([WEST]);
		const svc = getServiceSpec(create(DeploymentSchema, { spec: result.deploy[0]?.spec }));
		expect(svc?.minReplicas).toBe(5);
		expect(svc?.maxReplicas).toBe(5);
	});

	test("skips a value typed back to what runs", () => {
		expect(plan({ cpu: "250m", scaling: { [EAST]: scaling(2) } })).toEqual({ deploy: [], retire: [] });
	});

	test("retires a region switched off and adds one switched on from the first region's spec", () => {
		const withSpare = [...regions, region("ap-south-1", undefined)];
		const spareBase = baseline("shop", "", withSpare, true, defaults);
		const result = planSave(withSpare, { regions: { [WEST]: false, "ap-south-1": true } }, spareBase);
		expect(result.retire).toEqual([{ region: WEST, deploymentId: "west" }]);
		expect(result.deploy.map((d) => [d.region, d.added])).toEqual([["ap-south-1", true]]);
		expect(result.deploy[0]?.commit).toEqual(regions[0]?.current?.commit);
	});

	test("redeploys every region when the resource goes private", () => {
		expect(plan({ isPublic: false }).deploy.map((d) => d.region)).toEqual([EAST, WEST]);
	});
});

test("settings preserve autoscaling on sizing edits and disable it on fixed replica edits", () => {
	const current = deployment("autoscaled", EAST);
	const svc = getServiceSpec(current);
	if (svc === undefined) throw new Error("service missing");
	svc.scalers = create(ScalersSchema, { enabled: true, cpuTarget: 70 });
	svc.maxReplicas = 5;
	const views = [region(EAST, current)];
	const original = baseline("shop", "", views, true, defaults);
	const sized = planSave(views, { cpu: "500m" }, original);
	const sizedService = getServiceSpec(create(DeploymentSchema, { spec: sized.deploy[0]?.spec }));
	expect(sizedService?.scalers?.enabled).toBe(true);
	const fixed = planSave(views, { scaling: { [EAST]: scaling(3) } }, original);
	const fixedService = getServiceSpec(create(DeploymentSchema, { spec: fixed.deploy[0]?.spec }));
	expect(fixedService?.minReplicas).toBe(3);
	expect(fixedService?.maxReplicas).toBe(3);
	expect(fixedService?.scalers?.enabled).toBe(false);
});

describe("changeCount", () => {
	test("counts fields that differ from what runs", () => {
		expect(changeCount({ cpu: "250m" }, base)).toBe(0);
		expect(changeCount({ cpu: "1", scaling: { [EAST]: scaling(4) }, secrets: { API_KEY: "x" } }, base)).toBe(3);
	});
});

describe("validateDraft", () => {
	test("reports only the fields the draft touched", () => {
		expect(validateDraft({}, base, SERVED, 10)).toEqual({});
		expect(validateDraft({ port: "http", healthPath: "ready" }, base, SERVED, 10)).toEqual({
			port: "Enter a port number",
			healthPath: "Start the path with /",
		});
	});

	test("keeps at least one region", () => {
		expect(validateDraft({ regions: { [EAST]: false, [WEST]: false } }, base, SERVED, 10).regions).toBe(
			"Keep at least one region",
		);
	});

	test("needs a served platform domain and a valid subdomain to go public", () => {
		const privateBase = baseline("shop", "", regions, false, defaults);
		expect(
			validateDraft({ isPublic: true, subdomain: "Shop!", platformDomain: "other.dev" }, privateBase, SERVED, 10),
		).toEqual({
			platformDomain: "No platform domain is served in every region",
			subdomain: "Use lowercase letters, digits and hyphens",
		});
	});
});

test("returning to fixed bounds discards an inactive CPU target edit", () => {
	const draft = { scaling: { [EAST]: scaling(2, 2, defaults.cpuTarget + 1) } };
	expect(changeCount(draft, base)).toBe(0);
	expect(plan(draft)).toEqual({ deploy: [], retire: [] });
});

test("settings send enabled, changed and disabled autoscaling through the shared request builder", () => {
	for (const value of [scaling(1, 5, 60), scaling(2, 4, 80), scaling(3)]) {
		const result = plan({ scaling: { [EAST]: value } });
		const svc = getServiceSpec(create(DeploymentSchema, { spec: result.deploy[0]?.spec }));
		expect(svc?.minReplicas).toBe(value.replicas);
		expect(svc?.maxReplicas).toBe(value.maxReplicas);
		expect(svc?.scalers?.enabled).toBe(value.maxReplicas !== value.replicas);
		expect(result.deploy[0]?.commit).toEqual(regions[0]?.current?.commit);
		if (value.maxReplicas !== value.replicas) expect(svc?.scalers?.cpuTarget).toBe(value.cpuTarget);
	}
	for (const value of [scaling(0), scaling(3, 2), scaling(1, 11), scaling(1, 3, 0), scaling(1, 3, 101)]) {
		expect(validateDraft({ scaling: { [EAST]: value } }, base, SERVED, 10)[`scaling.${EAST}`]).toBeDefined();
	}
});

test("settings baseline follows configured bounds while the HPA changes the live replica count", () => {
	const current = deployment("scaled", EAST, 4);
	const svc = getServiceSpec(current);
	if (svc === undefined) throw new Error("missing service");
	svc.minReplicas = 1;
	svc.maxReplicas = 5;
	svc.scalers = create(ScalersSchema, { enabled: true, cpuTarget: 80 });
	const initial = baseline("shop", "", [region(EAST, current)], true, defaults);
	expect(initial.scaling[EAST]).toEqual(scaling(1, 5, 80));
	expect(changeCount({}, initial)).toBe(0);
});

test("scaling recovery retains failed regions and clears only confirmed regions", () => {
	const draft = { scaling: { [EAST]: scaling(1, 3), [WEST]: scaling(2, 4) } };
	expect(savedScaling(draft, [])).toEqual(draft);
	expect(savedScaling(draft, [EAST])).toEqual({ scaling: { [WEST]: scaling(2, 4) } });
	expect(savedScaling(draft, [EAST, WEST])).toEqual({ scaling: {} });
});
