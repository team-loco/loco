import { DefaultServiceConfigSchema } from "@gen/loco/config/v1/config_pb";
import { textOf } from "@/lib/node-text";
import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import {
	DeploymentStatus,
	DeploymentPlacementSchema,
	DeploymentSchema,
	DeploymentSpecSchema,
	FailurePolicySchema,
	HealthCheckConfigSchema,
	RestartPolicy,
	ObservabilityConfigSchema,
	ServiceDeploymentSpecSchema,
	RegionDeploymentTargetSchema,
	ScalersSchema,
	type Deployment,
} from "@gen/loco/deployment/v1/deployment_pb";

import { RegionTargetSchema, type RegionTarget } from "@gen/loco/resource/v1/resource_pb";

import { getServiceSpec } from "@/lib/deployment-utils";
import type { HealthForm } from "@/lib/new-resource";

import type { RegionView } from "./model";
import {
	addedProblems,
	baseline,
	changeCount,
	changeNames,
	failurePolicyFrom,
	editHealth,
	switchHealth,
	plainEnv,
	planSave,
	regionChanges,
	regionMapChanged,
	regionTargets,
	resolved,
	scalingDiffers,
	validateDraft,
	type Baseline,
	type SettingsDraft,
} from "./settings-draft";

const EAST = "us-va-1";
const WEST = "us-ca-1";
const IMAGE = "ghcr.io/acme/shop:1.4.0";
const SERVED = ["loco.example.com"];
const OBSERVABILITY = { injectOtel: true };

function deployment(id: string, regionName: string, replicas = 2): Deployment {
	return create(DeploymentSchema, {
		id,
		placements: [{ region: regionName, desiredReplicas: replicas, live: true }],
		commit: { sha: "a".repeat(40), dirty: true },
		status: DeploymentStatus.RUNNING,
		spec: create(DeploymentSpecSchema, {
			spec: {
				case: "service",
				value: create(ServiceDeploymentSpecSchema, {
					observability: OBSERVABILITY,
					build: { type: "image", image: IMAGE },
					regions: { [regionName]: { cpu: "250m", memory: "512Mi", minReplicas: replicas, maxReplicas: replicas } },
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
	return {
		name,
		status: undefined,
		history: current === undefined ? [] : [current],
		current,
		serving: current,
		lastGood: undefined,
	};
}

const POLICY = create(FailurePolicySchema, {
	restartPolicy: RestartPolicy.ON_FAILURE,
	maxRestarts: 2,
	deployTimeoutSeconds: 600,
});
const defaults = create(DefaultServiceConfigSchema, {
	cpuTarget: 65,
	minReplicas: 1,
	maxReplicas: 1,
	observability: OBSERVABILITY,
	failurePolicy: POLICY,
});
const scaling = (replicas: number, maxReplicas = replicas, cpuTarget = defaults.cpuTarget) => ({
	replicas,
	maxReplicas,
	cpuTarget,
});

const regions = [region(EAST, deployment("east", EAST)), region(WEST, deployment("west", WEST, 3))];
const base = baseline("shop", "shop", "", regions, [EAST, WEST], true, defaults, POLICY);
const hc = (patch: Partial<HealthForm>): SettingsDraft => ({
	health: { kind: "edit", form: { ...base.health, ...patch } },
});
const ENV = { LOG_LEVEL: "info" };
const planWith = (views: RegionView[], draft: SettingsDraft, b: Baseline) =>
	planSave(views, draft, b, plainEnv(ENV, draft));
const SPARE = "in-mh-1";

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

const stored = {
	[EAST]: target("250m", 2),
	[WEST]: target("250m", 3),
	[SPARE]: target("1", 4, false),
};

function plan(draft: SettingsDraft) {
	return planWith(regions, draft, base);
}

describe("planSave", () => {
	test("failure policy changes update the service without planning deployments", () => {
		expect([base.restartPolicy, base.maxRestarts, base.deployTimeout]).toEqual(["on-failure", "2", "600"]);
		expect(changeCount({ restartPolicy: "never" }, base)).toBe(1);
		expect(changeCount({ restartPolicy: "never", maxRestarts: "3" }, base)).toBe(1);
		expect(changeCount({ maxRestarts: "3", deployTimeout: "300" }, base)).toBe(2);
		expect(plan({ restartPolicy: "never", deployTimeout: "300" })).toEqual([]);
		expect(
			failurePolicyFrom(resolved({ restartPolicy: "never", maxRestarts: "3", deployTimeout: "300" }, base)),
		).toEqual(create(FailurePolicySchema, { restartPolicy: RestartPolicy.NEVER, deployTimeoutSeconds: 300 }));
		expect(failurePolicyFrom(resolved({ maxRestarts: "3" }, base))).toEqual(
			create(FailurePolicySchema, {
				restartPolicy: RestartPolicy.ON_FAILURE,
				maxRestarts: 3,
				deployTimeoutSeconds: 600,
			}),
		);
	});

	test("a policy without a budget offers the default restart limit", () => {
		const always = create(FailurePolicySchema, { restartPolicy: RestartPolicy.ALWAYS, deployTimeoutSeconds: 120 });
		const alwaysBase = baseline("shop", "shop", "", regions, [EAST, WEST], true, defaults, always);
		expect([alwaysBase.restartPolicy, alwaysBase.maxRestarts, alwaysBase.deployTimeout]).toEqual([
			"always",
			"2",
			"120",
		]);
		expect(changeCount({ restartPolicy: "on-failure" }, alwaysBase)).toBe(1);
	});

	test("a timeout in seconds that is not whole minutes stays editable", () => {
		const odd = create(FailurePolicySchema, { restartPolicy: RestartPolicy.ALWAYS, deployTimeoutSeconds: 90 });
		const oddBase = baseline("shop", "shop", "", regions, [EAST, WEST], true, defaults, odd);
		expect(oddBase.deployTimeout).toBe("90");
		expect(validateDraft({ restartPolicy: "never" }, oddBase, regions, SERVED, 10, ENV).deployTimeout).toBeUndefined();
		expect(failurePolicyFrom(resolved({ restartPolicy: "never" }, oddBase)).deployTimeoutSeconds).toBe(90);
	});

	test("a missing failure policy marks the section instead of throwing", () => {
		const missing = baseline("shop", "shop", "", regions, [EAST, WEST], true, defaults, undefined);
		expect(missing.failurePolicyMissing).toBe(true);
		expect(base.failurePolicyMissing).toBe(false);
		expect(changeCount({}, missing)).toBe(0);
	});

	test("failure policy fields reject values that are not whole numbers", () => {
		const errors = (draft: SettingsDraft) => validateDraft(draft, base, regions, SERVED, 10, ENV);
		expect(errors({ maxRestarts: "0" }).maxRestarts).toBe("A whole number, at least 1");
		expect(errors({ maxRestarts: "21" }).maxRestarts).toBeUndefined();
		expect(errors({ restartPolicy: "never", maxRestarts: "x" }).maxRestarts).toBeUndefined();
		expect(errors({ deployTimeout: "0" }).deployTimeout).toBe("Whole seconds, at least 1");
		expect(errors({ deployTimeout: "1.5" }).deployTimeout).toBe("Whole seconds, at least 1");
		expect(errors({ deployTimeout: "90" }).deployTimeout).toBeUndefined();
	});

	test("SDK environment configuration redeploys every running region and preserves application variables", () => {
		expect(base.observability).toEqual(OBSERVABILITY);
		const observability = { injectOtel: false };
		expect(changeCount({ observability }, base)).toBe(1);
		const changed = plan({ observability });
		expect(changed.flatMap((d) => d.regions)).toEqual([EAST, WEST]);
		for (const d of changed) {
			const svc = getServiceSpec(create(DeploymentSchema, { spec: d.spec }));
			expect(svc?.observability).toEqual(create(ObservabilityConfigSchema, observability));
			expect(svc?.env).toEqual({ LOG_LEVEL: "info" });
		}
		expect(changeCount({ observability: OBSERVABILITY }, base)).toBe(0);
	});

	test("an undeployed resource uses the API observability defaults", () => {
		const configured = create(DefaultServiceConfigSchema, {
			...defaults,
			observability: create(ObservabilityConfigSchema, { injectOtel: false }),
		});
		expect(baseline("shop", "shop", "", [], [EAST], true, configured, POLICY).observability.injectOtel).toBe(false);
	});

	test("changing one region's image clears its own source commit", () => {
		const first = deployment("one", EAST);
		const second = deployment("two", WEST);
		getServiceSpec(second)!.build!.image = "example.test/other:release";
		const mixed = [region(EAST, first), region(WEST, second)];
		const result = planWith(mixed, { image: IMAGE }, baseline("shop", "shop", "", mixed, [], true, defaults, POLICY));
		expect(result.map((d) => [d.regions, d.commit])).toEqual([[[WEST], undefined]]);
	});

	test("preserves the source commit for health changes and clears it for another image", () => {
		expect(plan(hc({ path: "/ready" }))[0]?.commit).toEqual(regions[0]?.current?.commit);
		expect(plan({ image: "example.test/another:release" })[0]?.commit).toBeUndefined();
	});

	test("variable changes redeploy every running region", () => {
		expect(plan({ env: { FEATURE_FLAGS: "beta" } }).flatMap((d) => d.regions)).toEqual([EAST, WEST]);
		expect(plan({ removedEnv: ["LOG_LEVEL"] }).flatMap((d) => d.regions)).toEqual([EAST, WEST]);
	});

	test("does nothing for an empty draft or region map edits", () => {
		expect(plan({})).toEqual([]);
		expect(plan({ cpu: "1", scaling: { [WEST]: scaling(5) }, regions: { [WEST]: false } })).toEqual([]);
	});

	test("carries every deployment change into one deployment per running region", () => {
		const result = plan({ image: "ghcr.io/acme/shop:1.5.0", port: "9090", ...hc({ path: "/ready" }) });
		expect(result.flatMap((d) => d.regions)).toEqual([EAST, WEST]);
		const svc = getServiceSpec(create(DeploymentSchema, { spec: result[0]?.spec }));
		expect(svc?.build?.image).toBe("ghcr.io/acme/shop:1.5.0");
		expect(svc?.port).toBe(9090);
		expect(svc?.regions[EAST]?.cpu).toBe("250m");
		expect(svc?.env).toEqual({ LOG_LEVEL: "info" });
		expect(svc?.healthCheck?.path).toBe("/ready");
		expect(svc?.healthCheck?.intervalSeconds).toBe(10);
	});

	test("sets and clears the command and args in every running region", () => {
		const set = plan({ command: "/http-echo", args: "-text=hello world\n\n-listen=:8080\n" });
		expect(set.flatMap((d) => d.regions)).toEqual([EAST, WEST]);
		for (const d of set) {
			const svc = getServiceSpec(create(DeploymentSchema, { spec: d.spec }));
			expect(svc?.command).toEqual(["/http-echo"]);
			expect(svc?.args).toEqual(["-text=hello world", "-listen=:8080"]);
		}
		expect(changeCount({ command: "/http-echo", args: "-text=hello world" }, base)).toBe(2);
		expect(changeCount({ args: "\n  \n" }, base)).toBe(0);
		expect(plan({ args: "\n" })).toEqual([]);

		const withArgs = regions.map((r) => {
			const current = deployment(r.name, r.name);
			const svc = getServiceSpec(current);
			if (svc !== undefined) svc.args = ["-text=hello"];
			return region(r.name, current);
		});
		const argsBase = baseline("shop", "shop", "", withArgs, [EAST, WEST], true, defaults, POLICY);
		expect(argsBase.args).toBe("-text=hello");
		const cleared = planWith(withArgs, { args: "" }, argsBase);
		expect(cleared.map((d) => getServiceSpec(create(DeploymentSchema, { spec: d.spec }))?.args)).toEqual([[], []]);
	});

	test("redeploys every region when the resource goes private", () => {
		expect(plan({ isPublic: false }).flatMap((d) => d.regions)).toEqual([EAST, WEST]);
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

	test("adds a region with the scaling the draft sets for every region", () => {
		const all = { [EAST]: scaling(5), [WEST]: scaling(5) };
		const running = { [EAST]: stored[EAST], [WEST]: stored[WEST] };
		const targets = regionTargets(running, { regions: { [SPARE]: true }, scaling: all }, base);
		expect([targets?.[EAST]?.minReplicas, targets?.[WEST]?.minReplicas, targets?.[SPARE]?.minReplicas]).toEqual([
			5, 5, 5,
		]);
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
		const idle = baseline("shop", "shop", "", [region(EAST, undefined)], [EAST], true, defaults, POLICY);
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
	expect(baseline("shop", "shop", "", views, [EAST, WEST], true, defaults, POLICY).regions).toEqual({
		[EAST]: true,
		[WEST]: true,
	});
	expect(baseline("shop", "shop", "", views, [EAST], true, defaults, POLICY).regions).toEqual({
		[EAST]: true,
		[WEST]: false,
	});
});

describe("changeCount", () => {
	test("counts fields that differ from what runs", () => {
		expect(changeCount({ cpu: "250m" }, base)).toBe(0);
		expect(changeCount({ cpu: "1", scaling: { [EAST]: scaling(4) }, env: { API_KEY: "x" } }, base)).toBe(3);
	});

	test("counts changed and removed plain variables", () => {
		expect(changeCount({ env: { MODE: "debug" }, removedEnv: ["LOG_LEVEL"] }, base)).toBe(2);
	});
});

describe("plainEnv", () => {
	test("applies changed, added and removed plain variables to what runs", () => {
		const current = { LOG_LEVEL: "info", MODE: "fast", OLD: "x" };
		const draft = { env: { MODE: "", NEW: "1" }, removedEnv: ["OLD"] };
		expect(plainEnv(current, draft)).toEqual({ LOG_LEVEL: "info", MODE: "", NEW: "1" });
	});

	test("removes every plain variable", () => {
		expect(plainEnv({ LOG_LEVEL: "info" }, { removedEnv: ["LOG_LEVEL"] })).toEqual({});
	});
});

describe("addedProblems", () => {
	test("refuses an invalid or taken name and accepts any other", () => {
		const added = [
			{ id: "a", key: "featureFlags", value: "1" },
			{ id: "b", key: "LOG_LEVEL", value: "debug" },
			{ id: "c", key: "1X", value: "1" },
		];
		expect(
			new Map([...addedProblems({ added }, { LOG_LEVEL: "info" })].map(([id, message]) => [id, textOf(message)])),
		).toEqual(
			new Map([
				["b", "LOG_LEVEL is already a variable."],
				["c", "Use letters, digits and underscores, not starting with a digit."],
			]),
		);
	});

	test("accepts a variable that already runs with the same value, as after a partly saved retry", () => {
		expect(addedProblems({ added: [{ id: "a", key: "LOG_LEVEL", value: "info" }] }, { LOG_LEVEL: "info" }).size).toBe(
			0,
		);
	});
});

describe("validateDraft", () => {
	test("limits the name to 63 characters, counting an emoji as one", () => {
		expect(validateDraft({ name: "  " }, base, regions, SERVED, 10, ENV).name).toBe("Enter a name");
		expect(validateDraft({ name: "🚀".repeat(63) }, base, regions, SERVED, 10, ENV).name).toBeUndefined();
		expect(validateDraft({ name: "a".repeat(64) }, base, regions, SERVED, 10, ENV).name).toBe(
			"Use at most 63 characters",
		);
	});
	test("reports only the fields the draft touched", () => {
		expect(validateDraft({}, base, regions, SERVED, 10, ENV)).toEqual({});
		expect(validateDraft({ port: "http", ...hc({ path: "ready" }) }, base, regions, SERVED, 10, ENV)).toEqual({
			port: "Use a port from 1 to 65535",
			healthPath: "Start the path with /",
		});
	});

	test("keeps the health timeout below the interval", () => {
		expect(validateDraft(hc({ timeout: "9" }), base, regions, SERVED, 10, ENV).healthTimeout).toBeUndefined();
		expect(validateDraft(hc({ timeout: "10" }), base, regions, SERVED, 10, ENV).healthTimeout).toBe(
			"Must be below the interval",
		);
		expect(validateDraft(hc({ interval: "5", timeout: "5" }), base, regions, SERVED, 10, ENV).healthTimeout).toBe(
			"Must be below the interval",
		);
		expect(validateDraft(hc({ interval: "0", timeout: "0" }), base, regions, SERVED, 10, ENV)).toEqual({
			healthInterval: "Whole seconds, at least 5",
			healthTimeout: "Whole seconds, 1 to 60",
		});
		expect(validateDraft(hc({ interval: "4" }), base, regions, SERVED, 10, ENV).healthInterval).toBe(
			"Whole seconds, at least 5",
		);
		expect(validateDraft(hc({ interval: "5", timeout: "2" }), base, regions, SERVED, 10, ENV)).toEqual({});
		expect(validateDraft(hc({ interval: "90", timeout: "61" }), base, regions, SERVED, 10, ENV).healthTimeout).toBe(
			"Whole seconds, 1 to 60",
		);
	});

	test("asks for a port when the field is cleared", () => {
		expect(validateDraft({ port: "" }, base, regions, SERVED, 10, ENV).port).toBe("Enter a port number");
	});

	test("keeps the port within 1 through 65535", () => {
		for (const port of ["0", "70000", "65536"])
			expect(validateDraft({ port }, base, regions, SERVED, 10, ENV).port).toBe("Use a port from 1 to 65535");
		for (const port of ["1", "65535"])
			expect(validateDraft({ port }, base, regions, SERVED, 10, ENV).port).toBeUndefined();
	});

	test("keeps at least one region", () => {
		expect(validateDraft({ regions: { [EAST]: false, [WEST]: false } }, base, regions, SERVED, 10, ENV).regions).toBe(
			"Keep at least one region",
		);
	});

	test("saves region map changes apart from deployment changes", () => {
		expect(validateDraft({ cpu: "1", image: "ghcr.io/acme/shop:1.5.0" }, base, regions, SERVED, 10, ENV).regions).toBe(
			"Save region, size and scaling changes separately from image, port, health, observability and domain changes",
		);
		expect(validateDraft({ cpu: "1", scaling: { [EAST]: scaling(4) } }, base, regions, SERVED, 10, ENV)).toEqual({});
	});

	test("needs a served app domain and a valid subdomain to go public", () => {
		const privateBase = baseline("shop", "shop", "", regions, [], false, defaults, POLICY);
		expect(
			validateDraft(
				{ isPublic: true, subdomain: "Shop!", appDomain: "other.dev" },
				privateBase,
				regions,
				SERVED,
				10,
				ENV,
			),
		).toEqual({
			appDomain: "No app domain is served in every region",
			subdomain: "Use lowercase letters, digits and hyphens",
		});
	});

	test("rejects scaling outside the replica limit and CPU target range", () => {
		for (const value of [scaling(0), scaling(3, 2), scaling(1, 11), scaling(1, 3, 0), scaling(1, 3, 101)]) {
			expect(
				validateDraft({ scaling: { [EAST]: value } }, base, regions, SERVED, 10, ENV)[`scaling.${EAST}`],
			).toBeDefined();
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
	const size = svc.regions[EAST];
	if (size === undefined) throw new Error("missing region");
	size.minReplicas = 1;
	size.maxReplicas = 5;
	size.scalers = create(ScalersSchema, { enabled: true, cpuTarget: 80 });
	const initial = baseline("shop", "shop", "", [region(EAST, current)], [EAST], true, defaults, POLICY);
	expect(initial.scaling[EAST]).toEqual(scaling(1, 5, 80));
	expect(changeCount({}, initial)).toBe(0);
});

describe("scalingDiffers", () => {
	test("is true when the running regions scale differently", () => {
		expect(scalingDiffers(base)).toBe(true);
		const same = baseline(
			"shop",
			"shop",
			"",
			[region(EAST, deployment("east", EAST))],
			[EAST],
			true,
			defaults,
			POLICY,
			undefined,
		);
		expect(scalingDiffers(same)).toBe(false);
	});
});

describe("health check switch", () => {
	const checked = create(DefaultServiceConfigSchema, {
		...defaults,
		healthCheck: create(HealthCheckConfigSchema, {
			path: "/healthz",
			initialDelaySeconds: 17,
			intervalSeconds: 10,
			timeoutSeconds: 2,
			failureThreshold: 3,
		}),
	});
	const withDelay = (id: string, name: string, initialDelaySeconds: number | undefined) => {
		const d = deployment(id, name);
		const svc = getServiceSpec(d)!;
		if (initialDelaySeconds === undefined) svc.healthCheck = undefined;
		else svc.healthCheck!.initialDelaySeconds = initialDelaySeconds;
		return region(name, d);
	};
	const health = (d: { spec: Deployment["spec"] }) =>
		getServiceSpec(create(DeploymentSchema, { spec: d.spec }))?.healthCheck;
	const planned = (views: RegionView[], draft: SettingsDraft, b: typeof base) =>
		planWith(views, draft, b).map((d) => [d.regions, health(d)?.path, health(d)?.initialDelaySeconds]);
	const allOff = [withDelay("east", EAST, undefined), withDelay("west", WEST, undefined)];
	const allOffBase = baseline("shop", "shop", "", allOff, [EAST, WEST], true, checked, POLICY);
	const mixed = [withDelay("east", EAST, undefined), withDelay("west", WEST, 0)];
	const mixedBase = baseline("shop", "shop", "", mixed, [EAST, WEST], true, checked, POLICY);

	const worker = (id: string, name: string) => {
		const d = deployment(id, name);
		getServiceSpec(d)!.port = undefined;
		getServiceSpec(d)!.healthCheck = undefined;
		return region(name, d);
	};

	test("turning the switch back undoes the pending edit, and only that", () => {
		const on = switchHealth(undefined, allOffBase, true);
		expect(on?.kind).toBe("enable");
		expect(switchHealth(on, allOffBase, false)).toBeUndefined();
		const off = switchHealth(undefined, mixedBase, false);
		expect(off?.kind).toBe("disable");
		expect(switchHealth(off, mixedBase, true)).toBeUndefined();
		const edited = editHealth(undefined, mixedBase, { path: "/ready" });
		expect(switchHealth(edited, mixedBase, false)?.kind).toBe("disable");
	});

	test("a field typed back to its value keeps the edit pending", () => {
		const edited = editHealth(editHealth(undefined, base, { timeout: "5" }), base, { timeout: base.health.timeout });
		expect(edited).toEqual({ kind: "edit", form: base.health });
		expect(changeNames({ health: edited }, base)).toEqual(["Health check"]);
	});

	test("turning it on adds the default check, with its startup delay, where none runs", () => {
		const draft = { health: switchHealth(undefined, allOffBase, true) };
		expect(changeNames(draft, allOffBase)).toEqual(["Add health check"]);
		expect(planned(allOff, draft, allOffBase)).toEqual([
			[[EAST], "/healthz", 17],
			[[WEST], "/healthz", 17],
		]);
	});

	test("with a check in one region the switch is on, and turning it off removes that check only", () => {
		expect(mixedBase.health).toMatchObject({ enabled: true, path: "/healthz" });
		const draft = { health: switchHealth(undefined, mixedBase, false) };
		expect(changeNames(draft, mixedBase)).toEqual(["Remove health check"]);
		expect(planned(mixed, draft, mixedBase)).toEqual([[[WEST], undefined, undefined]]);
		expect(planWith(mixed, { port: "9090" }, mixedBase).map((d) => health(d)?.path)).toEqual([undefined, "/healthz"]);
	});

	test("turning it off removes a check only the resource stores", () => {
		const storedCheck = create(HealthCheckConfigSchema, {
			path: "/stored",
			intervalSeconds: 9,
			timeoutSeconds: 1,
			failureThreshold: 2,
		});
		const b = baseline("shop", "shop", "", allOff, [EAST, WEST], true, checked, POLICY, storedCheck);
		expect(b.health).toMatchObject({ enabled: true, path: "/stored" });
		const draft = { health: switchHealth(undefined, b, false) };
		expect(planned(allOff, draft, b)).toEqual([
			[[EAST], undefined, undefined],
			[[WEST], undefined, undefined],
		]);
	});

	test("turned on, a deployment that runs a check keeps it", () => {
		const draft = { health: switchHealth(undefined, allOffBase, true) };
		const refreshed = [withDelay("east", EAST, 60), withDelay("west", WEST, undefined)];
		expect(planned(refreshed, draft, allOffBase)).toEqual([[[WEST], "/healthz", 17]]);
	});

	test("an edit gives every deployment the form, each keeping its own startup delay", () => {
		const views = [withDelay("east", EAST, 60), withDelay("west", WEST, undefined)];
		const b = baseline("shop", "shop", "", views, [EAST, WEST], true, checked, POLICY);
		const draft = { health: editHealth(undefined, b, { path: "/ready" }) };
		expect(planned(views, draft, b)).toEqual([
			[[EAST], "/ready", 60],
			[[WEST], "/ready", 17],
		]);
	});

	test("turning it off ignores its fields", () => {
		const draft = { health: { kind: "disable" as const, form: { ...base.health, enabled: false, path: "nope" } } };
		expect(changeNames(draft, base)).toEqual(["Remove health check"]);
		expect(validateDraft(draft, base, regions, SERVED, 10, ENV).healthPath).toBeUndefined();
	});

	test("a health-only edit sends nothing to a worker deployment", () => {
		const views = [withDelay("east", EAST, undefined), worker("worker", WEST)];
		const b = baseline("shop", "shop", "", views, [EAST, WEST], true, checked, POLICY);
		for (const edit of [switchHealth(undefined, b, true), editHealth(undefined, b, { path: "/ready" })])
			expect(planned(views, { health: edit }, b).map(([names]) => names)).toEqual([[EAST]]);
	});

	test("the switch reads any deployment's check, in either region order", () => {
		const service = deployment("service", WEST);
		for (const views of [
			[worker("worker", EAST), region(WEST, service)],
			[region(WEST, service), worker("worker", EAST)],
		])
			expect(baseline("shop", "shop", "", views, [EAST, WEST], true, checked, POLICY).health.enabled).toBe(true);
	});

	test("a pending edit needs a running deployment with a port", () => {
		for (const views of [[worker("worker", EAST)], [region(EAST, undefined)]]) {
			const b = baseline("shop", "shop", "", views, [EAST], true, checked, POLICY);
			const draft = { health: switchHealth(undefined, b, true) };
			expect(validateDraft(draft, b, views, SERVED, 10, ENV).health).toBeDefined();
		}
	});

	test("a numerically equal edit plans nothing", () => {
		const draft = { health: editHealth(undefined, base, { interval: "010" }) };
		expect(validateDraft(draft, base, regions, SERVED, 10, ENV)).toEqual({});
		expect(plan(draft)).toEqual([]);
	});

	test("an undeployed service shows its stored check", () => {
		const idle = [region(EAST, undefined)];
		expect(baseline("shop", "shop", "", idle, [EAST], true, checked, POLICY).health.enabled).toBe(false);
		const storedCheck = create(HealthCheckConfigSchema, {
			path: "/ready",
			intervalSeconds: 9,
			timeoutSeconds: 1,
			failureThreshold: 2,
		});
		const idleBase = baseline("shop", "shop", "", idle, [EAST], true, checked, POLICY, storedCheck);
		expect(idleBase.health).toEqual({ enabled: true, path: "/ready", interval: "9", timeout: "1", failAfter: "2" });
	});
});

test("saving plans one deployment per running deployment, to the regions it serves", () => {
	const shared = deployment("shared", EAST);
	shared.placements.push(create(DeploymentPlacementSchema, { region: WEST, desiredReplicas: 2, live: true }));
	const svc = getServiceSpec(shared)!;
	svc.regions[WEST] = create(RegionDeploymentTargetSchema, {
		cpu: "250m",
		memory: "512Mi",
		minReplicas: 2,
		maxReplicas: 2,
	});
	const views = [region(EAST, shared), region(WEST, shared), region(SPARE, deployment("spare", SPARE))];
	const viewsBase = baseline("shop", "shop", "", views, [EAST, WEST, SPARE], true, defaults, POLICY);
	const result = planWith(views, { port: "9090" }, viewsBase);
	expect(
		result.map((d) => [d.regions, Object.keys(getServiceSpec(create(DeploymentSchema, { spec: d.spec }))!.regions)]),
	).toEqual([
		[
			[EAST, WEST],
			[EAST, WEST],
		],
		[[SPARE], [SPARE]],
	]);
	expect(result.map((d) => getServiceSpec(create(DeploymentSchema, { spec: d.spec }))?.port)).toEqual([9090, 9090]);
});

describe("after a partly failed save", () => {
	const refreshedWith = (change: (svc: NonNullable<ReturnType<typeof getServiceSpec>>) => void) => {
		const east = deployment("east-2", EAST);
		change(getServiceSpec(east)!);
		const views = [region(EAST, east), region(WEST, deployment("west", WEST, 3))];
		return { views, live: baseline("shop", "shop", "", views, [EAST, WEST], true, defaults, POLICY) };
	};

	test("a change stays named against the draft's origin and a retry sends only what still differs", () => {
		const draft: SettingsDraft = { port: "9090", origin: base };
		const { views, live } = refreshedWith((svc) => {
			svc.port = 9090;
		});
		expect(changeNames(draft, draft.origin!)).toEqual(["Port"]);
		expect(planWith(views, draft, live).map((d) => d.regions)).toEqual([[WEST]]);
	});

	test("an enabled check reaches the region that missed it, after a field is typed back", () => {
		const unchecked = (id: string, name: string) => {
			const d = deployment(id, name);
			getServiceSpec(d)!.healthCheck = undefined;
			return d;
		};
		const before = [region(EAST, unchecked("east", EAST)), region(WEST, unchecked("west", WEST))];
		const origin = baseline("shop", "shop", "", before, [EAST, WEST], true, defaults, POLICY);
		const enabled = switchHealth(undefined, origin, true)!;
		const { views, live } = refreshedWith((svc) => {
			svc.port = 9090;
		});
		views[1] = region(WEST, unchecked("west", WEST));
		const typed = editHealth(enabled, live, { timeout: "7" });
		const health = editHealth(typed, live, { timeout: enabled.form.timeout });
		expect(health).toEqual(enabled);
		const [west, ...rest] = planWith(views, { port: "9090", health, origin }, live);
		expect([west?.regions, rest]).toEqual([[WEST], []]);
		expect(getServiceSpec(create(DeploymentSchema, { spec: west!.spec }))?.healthCheck).toBeDefined();
	});
});

describe("the save plan", () => {
	test("validation, the Save label and the save use one plan against what runs now", () => {
		for (const draft of [
			{ port: "9090" },
			{ port: "9090", regions: { [WEST]: false } },
			{ regions: { [WEST]: false } },
		]) {
			const planned = planWith(regions, draft, base).length > 0;
			const blocked = validateDraft(draft, base, regions, SERVED, 10, ENV).regions !== undefined;
			expect(blocked).toBe(regionMapChanged(draft, base) && planned);
		}
	});
});

describe("variables", () => {
	test("a variable removed and added again runs with its new value", () => {
		const draft = { removedEnv: ["LOG_LEVEL"], added: [{ id: "a", key: "LOG_LEVEL", value: "debug" }] };
		expect(addedProblems(draft, ENV).size).toBe(0);
		expect(plan(draft).map((d) => getServiceSpec(create(DeploymentSchema, { spec: d.spec }))?.env)).toEqual([
			{ LOG_LEVEL: "debug" },
			{ LOG_LEVEL: "debug" },
		]);
	});
});
