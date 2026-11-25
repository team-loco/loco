import type { ReactNode } from "react";
import type { DefaultServiceConfig } from "@gen/loco/config/v1/config_pb";
import { withCode } from "@/components/design/InlineCode";
import { clone, create, equals } from "@bufbuild/protobuf";
import {
	BuildSourceSchema,
	DeploymentSpecSchema,
	FailurePolicySchema,
	HealthCheckConfigSchema,
	RestartPolicy,
	type FailurePolicy,
	ObservabilityConfigSchema,
	type ObservabilityConfig,
	type DeploymentSpec,
	type HealthCheckConfig,
	type ServiceDeploymentSpec,
	type SourceCommit,
} from "@gen/loco/deployment/v1/deployment_pb";
import { RegionTargetSchema, type RegionTarget } from "@gen/loco/resource/v1/resource_pb";

import { getServiceSpec, regionTarget, specForRegions } from "@/lib/deployment-utils";
import { isEnvKey } from "@/lib/dotenv";
import {
	IMAGE_RE,
	type HealthForm,
	healthCheckSpec,
	commandEntries,
	commandText,
	healthTimeoutError,
	portRangeError,
	scalingError,
	scalingSpec,
	type Scaling,
} from "@/lib/new-resource";
import { SUBDOMAIN_RE } from "@/lib/app-domain";
import { nameError } from "@/lib/slug";

import { isDeployed, type RegionView } from "./model";
import { currentSources } from "./redeploy";

// These fields make up the resource's region map, which UpdateResource replaces in one request
// for every environment; the other spec fields redeploy this environment's regions.
export const REGION_MAP_KEYS = ["regions", "scaling", "cpu", "memory"] as const;

const IMAGE_BUILD = "image";
const WHOLE = /^\d+$/;
// The bounds the API enforces on a health check.
const MIN_HEALTH_INTERVAL = 5;
const MAX_HEALTH_TIMEOUT = 60;

export const RESTART_CHOICES = [
	{ value: "on-failure", label: "On failure", policy: RestartPolicy.ON_FAILURE },
	{ value: "always", label: "Always", policy: RestartPolicy.ALWAYS },
	{ value: "never", label: "Never", policy: RestartPolicy.NEVER },
] as const;

export type RestartChoice = (typeof RESTART_CHOICES)[number]["value"];

export const FAILURE_POLICY_KEYS = ["restartPolicy", "maxRestarts", "deployTimeout"] as const;

interface FailurePolicySettings {
	restartPolicy: RestartChoice;
	maxRestarts: string;
	/** Seconds. */
	deployTimeout: string;
	/** The API returned no usable failure policy, so the section cannot show or edit it. */
	failurePolicyMissing: boolean;
}

function failurePolicySettings(
	policy: FailurePolicy | undefined,
	defaults: DefaultServiceConfig,
): FailurePolicySettings {
	const choice = RESTART_CHOICES.find((c) => c.policy === policy?.restartPolicy);
	if (policy === undefined || choice === undefined) {
		return { restartPolicy: "on-failure", maxRestarts: "", deployTimeout: "", failurePolicyMissing: true };
	}
	const restarts = policy.maxRestarts > 0 ? policy.maxRestarts : (defaults.failurePolicy?.maxRestarts ?? 0);
	return {
		restartPolicy: choice.value,
		maxRestarts: restarts.toString(),
		deployTimeout: policy.deployTimeoutSeconds.toString(),
		failurePolicyMissing: false,
	};
}

export type ObservabilitySettings = Required<Pick<ObservabilityConfig, "injectOtel">>;

function observabilitySettings(config: ObservabilityConfig | undefined): ObservabilitySettings {
	if (config?.injectOtel === undefined) {
		throw new Error("The API returned incomplete observability settings");
	}
	return { injectOtel: config.injectOtel };
}

export interface SettingsDraft {
	name?: string;
	description?: string;
	image?: string;
	restartPolicy?: RestartChoice;
	maxRestarts?: string;
	/** Seconds. */
	deployTimeout?: string;
	observability?: ObservabilitySettings;
	scaling?: Record<string, Scaling>;
	cpu?: string;
	memory?: string;
	port?: string;
	/** The health check every deployment should run, while an edit to it is pending. */
	health?: HealthForm | undefined;
	command?: string;
	args?: string;
	regions?: Record<string, boolean>;
	/** New variables in the order they were added. */
	added?: AddedVariable[];
	/** Variables to change in every deployment, by name. */
	env?: Record<string, string>;
	/** Variables to remove from every deployment. */
	removedEnv?: string[];
	isPublic?: boolean;
	subdomain?: string;
	appDomain?: string;
}

export interface Baseline {
	slug: string;
	name: string;
	description: string;
	image: string;
	restartPolicy: RestartChoice;
	maxRestarts: string;
	deployTimeout: string;
	failurePolicyMissing: boolean;
	observability: ObservabilitySettings;
	cpu: string;
	memory: string;
	port: string;
	/** The check the service runs; its fields hold the API defaults when it runs none. */
	health: HealthForm;
	/** The API's startup delay for a new check, in seconds; settings show no field for it. */
	healthDefaultDelay: number;
	command: string;
	args: string;
	scaling: Record<string, Scaling>;
	regions: Record<string, boolean>;
	isPublic: boolean;
	/** The variables the deployments run with, as written. */
	env: Record<string, string>;
}

/** A variable added in the settings, before it has a name the deployments know. */
export interface AddedVariable {
	id: string;
	key: string;
	value: string;
}

function deployedRegions(regions: RegionView[]): RegionView[] {
	return regions.filter(isDeployed);
}

function templateRegion(regions: RegionView[]): RegionView | undefined {
	return deployedRegions(regions)[0];
}

export function baseline(
	slug: string,
	name: string,
	description: string,
	regions: RegionView[],
	configured: string[],
	isPublic: boolean,
	defaults: DefaultServiceConfig,
	failurePolicy: FailurePolicy | undefined,
	storedHealth?: HealthCheckConfig,
): Baseline {
	const templateView = templateRegion(regions);
	const template = templateView?.current;
	const svc = template === undefined ? undefined : getServiceSpec(template);
	const size = regionTarget(svc, templateView?.name);
	const deployed = deployedRegions(regions);
	// The health check comes from the first deployment with a port; a worker runs none.
	const served = deployed
		.map((r) => (r.current === undefined ? undefined : getServiceSpec(r.current)))
		.find((spec) => spec?.port !== undefined);
	const check = deployed.length === 0 ? storedHealth : served?.healthCheck;
	const health = check ?? defaults.healthCheck;
	return {
		slug,
		name,
		description,
		image: svc?.build?.image ?? "",
		...failurePolicySettings(failurePolicy, defaults),
		observability: observabilitySettings(svc === undefined ? defaults.observability : svc.observability),
		cpu: size?.cpu ?? "",
		memory: size?.memory ?? "",
		port: svc?.port?.toString() ?? "",
		health: {
			enabled: check !== undefined,
			path: health?.path ?? "",
			interval: health?.intervalSeconds.toString() ?? "",
			timeout: health?.timeoutSeconds.toString() ?? "",
			failAfter: health?.failureThreshold.toString() ?? "",
		},
		healthDefaultDelay: defaults.healthCheck?.initialDelaySeconds ?? 0,
		command: commandText(svc?.command ?? []),
		args: commandText(svc?.args ?? []),
		scaling: Object.fromEntries(
			deployed.map((r) => {
				const spec = regionTarget(r.current === undefined ? undefined : getServiceSpec(r.current), r.name);
				const replicas = spec?.minReplicas ?? defaults.minReplicas;
				const cpuTarget = spec?.scalers?.cpuTarget;
				return [
					r.name,
					{
						replicas,
						maxReplicas: spec?.scalers?.enabled ? (spec.maxReplicas ?? defaults.maxReplicas) : replicas,
						cpuTarget: cpuTarget === undefined || cpuTarget === 0 ? defaults.cpuTarget : cpuTarget,
					},
				];
			}),
		),
		regions: Object.fromEntries(regions.map((r) => [r.name, isDeployed(r) || configured.includes(r.name)])),
		isPublic,
		env: { ...svc?.env },
	};
}

export function resolved(draft: SettingsDraft, base: Baseline): Baseline {
	return {
		slug: base.slug,
		name: draft.name ?? base.name,
		description: draft.description ?? base.description,
		image: draft.image ?? base.image,
		restartPolicy: draft.restartPolicy ?? base.restartPolicy,
		maxRestarts: draft.maxRestarts ?? base.maxRestarts,
		deployTimeout: draft.deployTimeout ?? base.deployTimeout,
		failurePolicyMissing: base.failurePolicyMissing,
		observability: draft.observability ?? base.observability,
		cpu: draft.cpu ?? base.cpu,
		memory: draft.memory ?? base.memory,
		port: draft.port ?? base.port,
		health: draft.health ?? base.health,
		healthDefaultDelay: base.healthDefaultDelay,
		command: draft.command ?? base.command,
		args: draft.args ?? base.args,
		scaling: { ...base.scaling, ...draft.scaling },
		regions: { ...base.regions, ...draft.regions },
		isPublic: draft.isPublic ?? base.isPublic,
		env: plainEnv(base.env, draft),
	};
}

const SCALAR_KEYS = ["name", "description", "image", "cpu", "memory", "port"] as const;

/** Each health check field, the key its error shows under, and its change label. */
const HEALTH_FIELDS = [
	["path", "healthPath", "Health check path"],
	["interval", "healthInterval", "Health check interval"],
	["timeout", "healthTimeout", "Health check timeout"],
	["failAfter", "healthFailures", "Health check failure threshold"],
] as const;

type HealthErrorKey = (typeof HEALTH_FIELDS)[number][1];

const COMMAND_KEYS = ["command", "args"] as const;

function differs(a: string, b: string): boolean {
	return a.trim() !== b.trim();
}

const SCALAR_LABELS: Record<(typeof SCALAR_KEYS)[number], string> = {
	name: "Name",
	description: "Description",
	image: "Image",
	cpu: "CPU",
	memory: "Memory",
	port: "Port",
};

/**
 * One name per pending change, in the order the sections appear. A deployment setting stays
 * pending while any running deployment does not run what the draft intends.
 */
export function changeNames(draft: SettingsDraft, base: Baseline, regions: RegionView[]): string[] {
	const next = resolved(draft, base);
	const pending = pendingSettings(regions, draft, base);
	const changed = (k: (typeof SCALAR_KEYS)[number]) =>
		k === "image" || k === "port" ? pending.has(k) : differs(next[k], base[k]);
	const names: string[] = SCALAR_KEYS.filter(changed).map((k) => SCALAR_LABELS[k]);
	names.push(
		...(pending.has("health") ? healthChanges(draft, base) : []),
		...(Object.keys(draft.scaling ?? {}).some((r) => !sameScaling(next.scaling[r], base.scaling[r]))
			? ["Scaling"]
			: []),
		...Object.keys(next.regions)
			.filter((r) => next.regions[r] !== base.regions[r])
			.map((r) => (next.regions[r] === true ? `Add region ${r}` : `Remove region ${r}`)),
		...(pending.has("env") ? variableChanges(draft) : []),
		...COMMAND_KEYS.filter((k) => pending.has(k)).map((k) => (k === "command" ? "Start command" : "Arguments")),
	);
	if (next.isPublic !== base.isPublic) names.push(next.isPublic ? "Public access" : "Internal only");
	names.push(...failurePolicyChanges(next, base));
	if (pending.has("observability")) names.push("Configure SDK environment");
	return names;
}

/** The pending health check edit, named against the baseline. */
function healthChanges(draft: SettingsDraft, base: Baseline): string[] {
	const health = draft.health;
	if (health === undefined) return [];
	if (!health.enabled) return ["Remove health check"];
	if (!base.health.enabled) return ["Add health check"];
	const fields = HEALTH_FIELDS.filter(([k]) => differs(health[k], base.health[k])).map(([, , label]) => label);
	// A check some deployments still run differently after a partial save.
	return fields.length > 0 ? fields : ["Health check"];
}

/** The health edit to keep in the draft for the form the user sees, or none once it matches the baseline. */
export function healthEdit(base: Baseline, form: HealthForm): HealthForm | undefined {
	const fieldsKept = !form.enabled || HEALTH_FIELDS.every(([k]) => !differs(form[k], base.health[k]));
	return form.enabled === base.health.enabled && fieldsKept ? undefined : form;
}

export function changeCount(draft: SettingsDraft, base: Baseline, regions: RegionView[]): number {
	return changeNames(draft, base, regions).length;
}

function variableChanges(draft: SettingsDraft): string[] {
	return [
		...namedAdded(draft).map((row) => `Add ${row.key.trim()}`),
		...Object.keys(draft.env ?? {}).map((n) => `Variable ${n}`),
		...(draft.removedEnv ?? []).map((n) => `Remove variable ${n}`),
	];
}

type SpecSetting = "image" | "port" | "command" | "args" | "observability" | "health" | "env";

/** Whether two deployment specs run the same value of each setting the settings page edits. */
const SAME_SETTING: Record<SpecSetting, (a: ServiceDeploymentSpec, b: ServiceDeploymentSpec) => boolean> = {
	image: (a, b) => a.build?.image === b.build?.image,
	port: (a, b) => a.port === b.port,
	command: (a, b) => a.command.join("\0") === b.command.join("\0"),
	args: (a, b) => a.args.join("\0") === b.args.join("\0"),
	observability: (a, b) => a.observability?.injectOtel === b.observability?.injectOtel,
	health: (a, b) => sameHealth(a.healthCheck, b.healthCheck),
	env: (a, b) => sameEnv(a.env, b.env),
};

/** Which settings the draft edits, for a service no deployment runs. */
const DRAFTED: Record<SpecSetting, (draft: SettingsDraft) => boolean> = {
	image: (d) => d.image !== undefined,
	port: (d) => d.port !== undefined,
	command: (d) => d.command !== undefined,
	args: (d) => d.args !== undefined,
	observability: (d) => d.observability !== undefined,
	health: (d) => d.health !== undefined,
	env: variablesChanged,
};

const SPEC_SETTINGS = Object.keys(SAME_SETTING) as SpecSetting[];

function sameHealth(a: HealthCheckConfig | undefined, b: HealthCheckConfig | undefined): boolean {
	if (a === undefined || b === undefined) return a === b;
	return equals(HealthCheckConfigSchema, a, b);
}

function sameEnv(a: Record<string, string>, b: Record<string, string>): boolean {
	const keys = Object.keys(a);
	return keys.length === Object.keys(b).length && keys.every((k) => a[k] === b[k]);
}

/** The deployment settings some running deployment does not yet run as the draft intends. */
function pendingSettings(regions: RegionView[], draft: SettingsDraft, base: Baseline): Set<SpecSetting> {
	const sources = currentSources(regions);
	if (sources.length === 0) return new Set(SPEC_SETTINGS.filter((k) => DRAFTED[k](draft)));
	const env = plainEnv(base.env, draft);
	const pending = new Set<SpecSetting>();
	for (const { deployment } of sources) {
		const current = getServiceSpec(deployment);
		if (deployment.spec === undefined || current === undefined) continue;
		const intended = applyDraft(deployment.spec, draft, base, env).spec;
		if (intended.case !== "service") continue;
		for (const k of SPEC_SETTINGS) if (!SAME_SETTING[k](intended.value, current)) pending.add(k);
	}
	return pending;
}

export function domainsChange(draft: SettingsDraft, base: Baseline): "add" | "remove" | null {
	const isPublic = draft.isPublic ?? base.isPublic;
	if (isPublic === base.isPublic) return null;
	return isPublic ? "add" : "remove";
}

export type DraftErrors = Partial<Record<keyof SettingsDraft | HealthErrorKey | `scaling.${string}`, ReactNode>>;

/** Fields whose errors show while the user types, before the first save attempt. */
export const LIVE_ERROR_KEYS = [...HEALTH_FIELDS.map(([, key]) => key), "maxRestarts", "deployTimeout"] as const;

function positive(value: string): boolean {
	return WHOLE.test(value.trim()) && Number(value.trim()) > 0;
}

export function validateDraft(
	draft: SettingsDraft,
	base: Baseline,
	regions: RegionView[],
	served: string[],
	replicaLimit: number,
	takenNames: ReadonlySet<string> = new Set(),
): DraftErrors {
	const next = resolved(draft, base);
	const errors: DraftErrors = {};
	for (const [region, scaling] of Object.entries(draft.scaling ?? {})) {
		const error = scalingError(scaling, replicaLimit);
		if (error !== null) errors[`scaling.${region}`] = error;
	}
	if (draft.name !== undefined) {
		const error = nameError(next.name, takenNames);
		if (error !== null) errors.name = error;
	}
	if (draft.image !== undefined && !IMAGE_RE.test(next.image.trim())) errors.image = "Use registry/repository:tag";
	if (draft.port !== undefined) {
		const portInvalid = next.port.trim() === "" ? "Enter a port number" : portRangeError(next.port);
		if (portInvalid !== null) errors.port = portInvalid;
	}
	if (draft.health?.enabled === true) Object.assign(errors, healthErrors(draft.health));
	if (draft.health !== undefined && currentSources(regions).length === 0)
		errors.health = "The health check can be changed once the service is deployed.";
	const [variableProblem] = addedProblems(draft, base.env).values();
	if (variableProblem !== undefined) errors.added = variableProblem;
	// The API enforces the bounds and its message names them.
	if (draft.maxRestarts !== undefined && next.restartPolicy === "on-failure" && !positive(next.maxRestarts))
		errors.maxRestarts = "A whole number, at least 1";
	if (draft.deployTimeout !== undefined && !positive(next.deployTimeout))
		errors.deployTimeout = "Whole seconds, at least 1";
	if (!Object.values(next.regions).some(Boolean)) errors.regions = "Keep at least one region";
	else if (regionMapChanged(draft, base) && planSave(regions, draft, base).length > 0)
		errors.regions =
			"Save region, size and scaling changes separately from image, port, health, observability and domain changes";
	if (domainsChange(draft, base) === "add") {
		if (draft.appDomain === undefined || !served.includes(draft.appDomain))
			errors.appDomain = "No app domain is served in every region";
		if (!SUBDOMAIN_RE.test(draft.subdomain ?? "")) errors.subdomain = "Use lowercase letters, digits and hyphens";
	}
	return errors;
}

function healthErrors(health: HealthForm): DraftErrors {
	const errors: DraftErrors = {};
	if (!health.path.startsWith("/")) errors.healthPath = "Start the path with /";
	const intervalValid = positive(health.interval) && Number(health.interval) >= MIN_HEALTH_INTERVAL;
	if (!intervalValid) errors.healthInterval = `Whole seconds, at least ${MIN_HEALTH_INTERVAL.toString()}`;
	const timeoutValid = positive(health.timeout) && Number(health.timeout) <= MAX_HEALTH_TIMEOUT;
	if (!timeoutValid) errors.healthTimeout = `Whole seconds, 1 to ${MAX_HEALTH_TIMEOUT.toString()}`;
	const order = intervalValid && timeoutValid ? healthTimeoutError(health.interval, health.timeout) : null;
	if (order !== null) errors.healthTimeout = order;
	if (!positive(health.failAfter)) errors.healthFailures = "A whole number, at least 1";
	return errors;
}

function failurePolicyChanges(next: Baseline, base: Baseline): string[] {
	const names: string[] = [];
	if (next.restartPolicy !== base.restartPolicy) names.push("Restart policy");
	if (next.restartPolicy === "on-failure" && differs(next.maxRestarts, base.maxRestarts)) names.push("Restart limit");
	if (differs(next.deployTimeout, base.deployTimeout)) names.push("Deploy timeout");
	return names;
}

export function failurePolicyChanged(draft: SettingsDraft, base: Baseline): boolean {
	return failurePolicyChanges(resolved(draft, base), base).length > 0;
}

/** The failure policy to store, from the resolved settings. */
export function failurePolicyFrom(next: Baseline): FailurePolicy {
	const choice = RESTART_CHOICES.find((c) => c.value === next.restartPolicy);
	return create(FailurePolicySchema, {
		restartPolicy: choice?.policy ?? RestartPolicy.UNSPECIFIED,
		maxRestarts: next.restartPolicy === "on-failure" ? Number(next.maxRestarts.trim()) : 0,
		deployTimeoutSeconds: Number(next.deployTimeout.trim()),
	});
}

function healthFrom(health: HealthForm, current: HealthCheckConfig | undefined, delay: number): HealthCheckConfig {
	const spec = healthCheckSpec({ ...health, path: health.path.trim() }, current?.initialDelaySeconds ?? delay);
	return create(HealthCheckConfigSchema, spec);
}

/** Whether a deployment's image, pinned by digest, is the image the draft names. */
function runsImage(current: string, want: string): boolean {
	return current === want || current.split("@")[0] === want;
}

function applyDraft(
	spec: DeploymentSpec,
	draft: SettingsDraft,
	base: Baseline,
	env: Record<string, string>,
): DeploymentSpec {
	const next = clone(DeploymentSpecSchema, spec);
	if (next.spec.case !== "service") return next;
	const svc = next.spec.value;
	const want = resolved(draft, base);
	if (draft.image !== undefined && !runsImage(svc.build?.image ?? "", want.image.trim())) {
		svc.build = create(BuildSourceSchema, { type: IMAGE_BUILD, image: want.image.trim() });
	}
	if (draft.port !== undefined) svc.port = Number(want.port.trim());
	if (draft.observability !== undefined) svc.observability = create(ObservabilityConfigSchema, want.observability);
	// A deployment without a port runs no health check.
	const health = draft.health;
	if (health !== undefined)
		svc.healthCheck =
			health.enabled && svc.port !== undefined
				? healthFrom(health, svc.healthCheck, base.healthDefaultDelay)
				: undefined;
	if (variablesChanged(draft)) svc.env = env;
	if (draft.command !== undefined) svc.command = commandEntries(want.command);
	if (draft.args !== undefined) svc.args = commandEntries(want.args);
	return next;
}

interface PlannedDeploy {
	commit: SourceCommit | undefined;
	regions: string[];
	spec: DeploymentSpec;
}

/** The draft to retry after some deployments saved: added variables now exist there, so they become edits. */
export function retryDraft(draft: SettingsDraft): SettingsDraft {
	const { added, ...rest } = draft;
	const edits = Object.fromEntries(namedAdded({ added: added ?? [] }).map((row) => [row.key.trim(), row.value]));
	if (Object.keys(edits).length === 0) return draft;
	const removedEnv = (draft.removedEnv ?? []).filter((name) => !Object.hasOwn(edits, name));
	return { ...rest, env: { ...draft.env, ...edits }, removedEnv };
}

export function variablesChanged(draft: SettingsDraft): boolean {
	return namedAdded(draft).length > 0 || Object.keys(draft.env ?? {}).length > 0 || (draft.removedEnv ?? []).length > 0;
}

function namedAdded(draft: SettingsDraft): AddedVariable[] {
	return (draft.added ?? []).filter((row) => row.key.trim() !== "");
}

/** The variables deployments run with after the draft's changes. */
export function plainEnv(current: Record<string, string>, draft: SettingsDraft): Record<string, string> {
	const removed = new Set(draft.removedEnv ?? []);
	const kept = Object.entries({ ...current, ...draft.env }).filter(([name]) => !removed.has(name));
	const fresh = namedAdded(draft).map((row): [string, string] => [row.key.trim(), row.value]);
	return Object.fromEntries([...kept, ...fresh]);
}

/** Why each added variable cannot be saved, by its id. */
export function addedProblems(draft: SettingsDraft, current: Record<string, string>): Map<string, ReactNode> {
	const problems = new Map<string, ReactNode>();
	const added = draft.added ?? [];
	const counts = new Map<string, number>();
	for (const row of added) counts.set(row.key.trim(), (counts.get(row.key.trim()) ?? 0) + 1);
	const removed = new Set(draft.removedEnv ?? []);
	for (const row of added) {
		const key = row.key.trim();
		if (key === "") {
			if (row.value !== "") problems.set(row.id, "Enter a name for this value.");
		} else if (!isEnvKey(key)) problems.set(row.id, "Use letters, digits and underscores, not starting with a digit.");
		else if ((Object.hasOwn(current, key) && !removed.has(key)) || (counts.get(key) ?? 0) > 1)
			problems.set(row.id, withCode`${key} is already a variable.`);
	}
	return problems;
}

/**
 * One deployment per running deployment that does not yet run what the draft intends, to the
 * regions it serves. `env` is the full set of variables every deployment runs.
 */
export function planSave(
	regions: RegionView[],
	draft: SettingsDraft,
	base: Baseline,
	env = plainEnv(base.env, draft),
): PlannedDeploy[] {
	const redeployAll = domainsChange(draft, base) !== null;
	return currentSources(regions).flatMap(({ deployment, regions: names }) => {
		if (deployment.spec === undefined) return [];
		const spec = applyDraft(deployment.spec, draft, base, env);
		if (!redeployAll && equals(DeploymentSpecSchema, spec, deployment.spec)) return [];
		const sameImage =
			draft.image === undefined || runsImage(getServiceSpec(deployment)?.build?.image ?? "", draft.image.trim());
		return [{ regions: names, spec: specForRegions(spec, names), commit: sameImage ? deployment.commit : undefined }];
	});
}

export function regionMapChanged(draft: SettingsDraft, base: Baseline): boolean {
	const next = resolved(draft, base);
	return (
		differs(next.cpu, base.cpu) ||
		differs(next.memory, base.memory) ||
		Object.keys(next.regions).some((r) => next.regions[r] !== (base.regions[r] ?? false)) ||
		Object.keys(draft.scaling ?? {}).some((r) => !sameScaling(next.scaling[r], base.scaling[r]))
	);
}

export function regionChanges(draft: SettingsDraft, base: Baseline): { rolling: string[]; stopping: string[] } {
	const want = resolved(draft, base);
	const resized = differs(want.cpu, base.cpu) || differs(want.memory, base.memory);
	const names = Object.keys(want.regions).toSorted();
	return {
		rolling: names.filter(
			(r) =>
				want.regions[r] === true &&
				(base.regions[r] !== true || resized || !sameScaling(want.scaling[r], base.scaling[r])),
		),
		stopping: names.filter((r) => want.regions[r] === false && base.regions[r] === true),
	};
}

// regionTargets is the full region map to store, or undefined when the draft leaves it alone or
// no region runs here to size a new one from.
export function regionTargets(
	stored: Record<string, RegionTarget>,
	draft: SettingsDraft,
	base: Baseline,
): Record<string, RegionTarget> | undefined {
	const want = resolved(draft, base);
	const fallback = Object.values(want.scaling)[0];
	if (fallback === undefined || !regionMapChanged(draft, base)) return undefined;
	const targets = { ...stored };
	for (const [name, on] of Object.entries(want.regions)) {
		const kept = stored[name];
		if (!on) {
			if (kept !== undefined) targets[name] = create(RegionTargetSchema, { ...kept, enabled: false });
			continue;
		}
		const scaling =
			want.scaling[name] ??
			(kept === undefined || kept.minReplicas === 0
				? fallback
				: {
						replicas: kept.minReplicas,
						maxReplicas: kept.maxReplicas,
						cpuTarget: kept.scalers?.cpuTarget ?? fallback.cpuTarget,
					});
		targets[name] = create(RegionTargetSchema, {
			enabled: true,
			cpu: want.cpu,
			memory: want.memory,
			...scalingSpec(scaling),
		});
	}
	return targets;
}

/** Whether the regions a resource runs in scale differently today. */
export function scalingDiffers(base: Baseline): boolean {
	const [first, ...rest] = Object.values(base.scaling);
	return rest.some((scaling) => !sameScaling(scaling, first));
}

function sameScaling(a: Scaling | undefined, b: Scaling | undefined): boolean {
	return (
		a?.replicas === b?.replicas &&
		a?.maxReplicas === b?.maxReplicas &&
		(a?.replicas === a?.maxReplicas || a?.cpuTarget === b?.cpuTarget)
	);
}
