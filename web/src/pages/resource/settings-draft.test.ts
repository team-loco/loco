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

import { RegionTargetSchema, type RegionTarget } from "@gen/loco/resource/v1/resource_pb";

import { getServiceSpec } from "@/lib/deployment-utils";

import type { RegionView } from "./model";
import {
	baseline,
	changeCount,
	planSave,
	regionChanges,
	regionTargets,
	validateDraft,
	type SettingsDraft,
} from "./settings-draft";

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
const base = baseline("shop", "", regions, [EAST, WEST], true, defaults);
const SPARE = "ap-south-1";

function target(cpu: string, replicas: number, enabled = true): RegionTarget {
	return create(RegionTargetSchema, {
		enabled,
		cpu,
		memory: "512Mi",
		minReplicas: replicas,
		maxReplicas: replicas,
		scalers: { enabled: false },
	});
}

const stored = { [EAST]: target("250m", 2), [WEST]: target("250m", 3), [SPARE]: target("1", 4, false) };

function plan(draft: SettingsDraft) {
	return planSave(regions, draft, base);
}

describe("planSave", () => {
	test("changing one region's image clears its own source commit", () => {
		const first = deployment("one", EAST);
		const second = deployment("two", WEST);
		getServiceSpec(second)!.build!.image = "example.test/other:release";
		const mixed = [region(EAST, first), region(WEST, second)];
		const result = planSave(mixed, { image: IMAGE }, baseline("shop", "", mixed, [], true, defaults));
		expect(result.map((d) => [d.region, d.commit])).toEqual([[WEST, undefined]]);
	});

	test("preserves the source commit for health changes and clears it for another image", () => {
		expect(plan({ healthPath: "/ready" })[0]?.commit).toEqual(regions[0]?.current?.commit);
		expect(plan({ image: "example.test/another:release" })[0]?.commit).toBeUndefined();
	});

	test("does nothing for an empty draft or region map edits", () => {
		expect(plan({})).toEqual([]);
		expect(plan({ cpu: "1", scaling: { [WEST]: scaling(5) }, regions: { [WEST]: false } })).toEqual([]);
	});

	test("carries every deployment change into one deployment per running region", () => {
		const result = plan({ image: "ghcr.io/acme/shop:1.5.0", port: "9090", healthPath: "/ready" });
		expect(result.map((d) => d.region)).toEqual([EAST, WEST]);
		const svc = getServiceSpec(create(DeploymentSchema, { spec: result[0]?.spec }));
		expect(svc?.build?.image).toBe("ghcr.io/acme/shop:1.5.0");
		expect(svc?.port).toBe(9090);
		expect(svc?.cpu).toBe("250m");
		expect(svc?.env).toEqual({ LOG_LEVEL: "info" });
		expect(svc?.healthCheck?.path).toBe("/ready");
		expect(svc?.healthCheck?.intervalSeconds).toBe(10);
	});

	test("redeploys every region when the resource goes private", () => {
		expect(plan({ isPublic: false }).map((d) => d.region)).toEqual([EAST, WEST]);
	});
});

describe("regionTargets", () => {
	test("leaves the region map alone when the draft does not change it", () => {
		expect(regionTargets(stored, {}, base)).toBeUndefined();
		expect(regionTargets(stored, { image: IMAGE, cpu: "250m", scaling: { [EAST]: scaling(2) } }, base)).toBeUndefined();
	});

	test("adds a region with the first region's scaling and keeps the rest of the map", () => {
		const targets = regionTargets({ [EAST]: stored[EAST], [WEST]: stored[WEST] }, { regions: { [SPARE]: true } }, base);
		expect(targets?.[SPARE]).toEqual(target("250m", 2));
		expect(targets?.[WEST]).toEqual(target("250m", 3));
	});

	test("turns a region off without forgetting its sizing", () => {
		const targets = regionTargets(stored, { regions: { [WEST]: false } }, base);
		expect(targets?.[WEST]).toEqual(target("250m", 3, false));
		expect(targets?.[SPARE]).toEqual(stored[SPARE]);
	});

	test("turns a disabled region back on with its stored scaling and the current size", () => {
		expect(regionTargets(stored, { regions: { [SPARE]: true } }, base)?.[SPARE]).toEqual(target("250m", 4));
	});

	test("applies a size change in every enabled region", () => {
		const targets = regionTargets(stored, { cpu: "1" }, base);
		expect([targets?.[EAST]?.cpu, targets?.[WEST]?.cpu, targets?.[SPARE]?.cpu]).toEqual(["1", "1", "1"]);
		expect(targets?.[SPARE]?.enabled).toBe(false);
	});

	test("sends enabled, changed and disabled autoscaling per region", () => {
		for (const value of [scaling(1, 5, 60), scaling(2, 4, 80), scaling(3)]) {
			const east = regionTargets(stored, { scaling: { [EAST]: value } }, base)?.[EAST];
			expect([east?.minReplicas, east?.maxReplicas]).toEqual([value.replicas, value.maxReplicas]);
			const autoscaled = value.maxReplicas !== value.replicas;
			expect(east?.scalers?.enabled).toBe(autoscaled);
			expect(east?.scalers?.cpuTarget).toBe(autoscaled ? value.cpuTarget : undefined);
		}
	});

	test("needs a running region to size the map from", () => {
		const idle = baseline("shop", "", [region(EAST, undefined)], [EAST], true, defaults);
		expect(regionTargets(stored, { regions: { [WEST]: true } }, idle)).toBeUndefined();
	});
});

test("regionChanges names the regions a region map save rolls out and stops", () => {
	expect(regionChanges({ regions: { [WEST]: false, [SPARE]: true } }, base)).toEqual({
		rolling: [SPARE],
		stopping: [WEST],
	});
	expect(regionChanges({ scaling: { [WEST]: scaling(5) } }, base).rolling).toEqual([WEST]);
	expect(regionChanges({ memory: "1Gi" }, base).rolling).toEqual([WEST, EAST]);
});

test("the baseline shows a configured region that does not run here as on", () => {
	const views = [region(EAST, deployment("east", EAST)), region(WEST, undefined)];
	expect(baseline("shop", "", views, [EAST, WEST], true, defaults).regions).toEqual({ [EAST]: true, [WEST]: true });
	expect(baseline("shop", "", views, [EAST], true, defaults).regions).toEqual({ [EAST]: true, [WEST]: false });
});

describe("changeCount", () => {
	test("counts fields that differ from what runs", () => {
		expect(changeCount({ cpu: "250m" }, base)).toBe(0);
		expect(changeCount({ cpu: "1", scaling: { [EAST]: scaling(4) }, secrets: { API_KEY: "x" } }, base)).toBe(3);
	});
});

describe("validateDraft", () => {
	test("reports only the fields the draft touched", () => {
		expect(validateDraft({}, base, regions, SERVED, 10)).toEqual({});
		expect(validateDraft({ port: "http", healthPath: "ready" }, base, regions, SERVED, 10)).toEqual({
			port: "Enter a port number",
			healthPath: "Start the path with /",
		});
	});

	test("keeps at least one region", () => {
		expect(validateDraft({ regions: { [EAST]: false, [WEST]: false } }, base, regions, SERVED, 10).regions).toBe(
			"Keep at least one region",
		);
	});

	test("saves region map changes apart from deployment changes", () => {
		expect(validateDraft({ cpu: "1", image: "ghcr.io/acme/shop:1.5.0" }, base, regions, SERVED, 10).regions).toBe(
			"Save region, size and scaling changes separately from image, port, health and domain changes",
		);
		expect(validateDraft({ cpu: "1", scaling: { [EAST]: scaling(4) } }, base, regions, SERVED, 10)).toEqual({});
	});

	test("needs a served platform domain and a valid subdomain to go public", () => {
		const privateBase = baseline("shop", "", regions, [], false, defaults);
		expect(
			validateDraft(
				{ isPublic: true, subdomain: "Shop!", platformDomain: "other.dev" },
				privateBase,
				regions,
				SERVED,
				10,
			),
		).toEqual({
			platformDomain: "No platform domain is served in every region",
			subdomain: "Use lowercase letters, digits and hyphens",
		});
	});

	test("rejects scaling outside the replica limit and CPU target range", () => {
		for (const value of [scaling(0), scaling(3, 2), scaling(1, 11), scaling(1, 3, 0), scaling(1, 3, 101)]) {
			expect(validateDraft({ scaling: { [EAST]: value } }, base, regions, SERVED, 10)[`scaling.${EAST}`]).toBeDefined();
		}
	});
});

test("returning to fixed bounds discards an inactive CPU target edit", () => {
	const draft = { scaling: { [EAST]: scaling(2, 2, defaults.cpuTarget + 1) } };
	expect(changeCount(draft, base)).toBe(0);
	expect(regionTargets(stored, draft, base)).toBeUndefined();
});

test("settings baseline follows configured bounds while the HPA changes the live replica count", () => {
	const current = deployment("scaled", EAST, 4);
	const svc = getServiceSpec(current);
	if (svc === undefined) throw new Error("missing service");
	svc.minReplicas = 1;
	svc.maxReplicas = 5;
	svc.scalers = create(ScalersSchema, { enabled: true, cpuTarget: 80 });
	const initial = baseline("shop", "", [region(EAST, current)], [EAST], true, defaults);
	expect(initial.scaling[EAST]).toEqual(scaling(1, 5, 80));
	expect(changeCount({}, initial)).toBe(0);
});
