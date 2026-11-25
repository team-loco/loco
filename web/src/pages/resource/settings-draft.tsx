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
	type SourceCommit,
} from "@gen/loco/deployment/v1/deployment_pb";
import { RegionTargetSchema, type RegionTarget } from "@gen/loco/resource/v1/resource_pb";

import { getServiceSpec, regionTarget } from "@/lib/deployment-utils";
import { isEnvKey } from "@/lib/dotenv";
import {
	IMAGE_RE,
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
	healthEnabled?: boolean;
	healthPath?: string;
	healthInterval?: string;
	healthTimeout?: string;
	healthFailures?: string;
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
	/** Whether the service runs a health check; the health fields hold the API defaults when it does not. */
	healthEnabled: boolean;
	/** Seconds; settings show no field for it. */
	healthDelay: number;
	healthPath: string;
	healthInterval: string;
	healthTimeout: string;
	healthFailures: string;
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
	storedHealth: HealthCheckConfig | undefined,
): Baseline {
	const templateView = templateRegion(regions);
	const template = templateView?.current;
	const svc = template === undefined ? undefined : getServiceSpec(template);
	const size = regionTarget(svc, templateView?.name);
	const check = svc === undefined ? storedHealth : svc.healthCheck;
	const health = check ?? defaults.healthCheck;
	const deployed = deployedRegions(regions);
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
		healthEnabled: check !== undefined,
		healthDelay: health?.initialDelaySeconds ?? 0,
		healthPath: health?.path ?? "",
		healthInterval: health === undefined ? "" : health.intervalSeconds.toString(),
		healthTimeout: health === undefined ? "" : health.timeoutSeconds.toString(),
		healthFailures: health === undefined ? "" : health.failureThreshold.toString(),
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
		healthEnabled: draft.healthEnabled ?? base.healthEnabled,
		healthDelay: base.healthDelay,
		healthPath: draft.healthPath ?? base.healthPath,
		healthInterval: draft.healthInterval ?? base.healthInterval,
		healthTimeout: draft.healthTimeout ?? base.healthTimeout,
		healthFailures: draft.healthFailures ?? base.healthFailures,
		command: draft.command ?? base.command,
		args: draft.args ?? base.args,
		scaling: { ...base.scaling, ...draft.scaling },
		regions: { ...base.regions, ...draft.regions },
		isPublic: draft.isPublic ?? base.isPublic,
		env: plainEnv(base.env, draft),
	};
}

const SCALAR_KEYS = ["name", "description", "image", "cpu", "memory", "port"] as const;

export const HEALTH_KEYS = ["healthPath", "healthInterval", "healthTimeout", "healthFailures"] as const;

const COMMAND_KEYS = ["command", "args"] as const;

function sameEntries(a: string, b: string): boolean {
	const left = commandEntries(a);
	const right = commandEntries(b);
	return left.length === right.length && left.every((entry, i) => entry === right[i]);
}

function differs(a: string, b: string): boolean {
	return a.trim() !== b.trim();
}

const SCALAR_LABELS: Record<(typeof SCALAR_KEYS)[number] | (typeof HEALTH_KEYS)[number], string> = {
	name: "Name",
	description: "Description",
	image: "Image",
	cpu: "CPU",
	memory: "Memory",
	port: "Port",
	healthPath: "Health check path",
	healthInterval: "Health check interval",
	healthTimeout: "Health check timeout",
	healthFailures: "Health check failure threshold",
};

/** One name per pending change, in the order the sections appear. */
export function changeNames(draft: SettingsDraft, base: Baseline): string[] {
	const next = resolved(draft, base);
	const names: string[] = SCALAR_KEYS.filter((k) => differs(next[k], base[k])).map((k) => SCALAR_LABELS[k]);
	names.push(
		...healthChanges(next, base),
		...(Object.keys(draft.scaling ?? {}).some((r) => !sameScaling(next.scaling[r], base.scaling[r]))
			? ["Scaling"]
			: []),
		...Object.keys(next.regions)
			.filter((r) => next.regions[r] !== base.regions[r])
			.map((r) => (next.regions[r] === true ? `Add region ${r}` : `Remove region ${r}`)),
		...namedAdded(draft).map((row) => `Add ${row.key.trim()}`),
		...Object.keys(draft.env ?? {}).map((n) => `Variable ${n}`),
		...(draft.removedEnv ?? []).map((n) => `Remove variable ${n}`),
		...COMMAND_KEYS.filter((k) => !sameEntries(next[k], base[k])).map((k) =>
			k === "command" ? "Start command" : "Arguments",
		),
	);
	if (next.isPublic !== base.isPublic) names.push(next.isPublic ? "Public access" : "Internal only");
	names.push(...failurePolicyChanges(next, base));
	if (next.observability.injectOtel !== base.observability.injectOtel) names.push("Configure SDK environment");
	return names;
}

/** The health check changes; field edits count only while the check stays on. */
function healthChanges(next: Baseline, base: Baseline): string[] {
	if (next.healthEnabled !== base.healthEnabled)
		return [next.healthEnabled ? "Add health check" : "Remove health check"];
	if (!next.healthEnabled) return [];
	return HEALTH_KEYS.filter((k) => differs(next[k], base[k])).map((k) => SCALAR_LABELS[k]);
}

/** Whether saving the draft removes the service's health check. */
export function healthRemoved(draft: SettingsDraft, base: Baseline): boolean {
	return base.healthEnabled && !resolved(draft, base).healthEnabled;
}

export function changeCount(draft: SettingsDraft, base: Baseline): number {
	return changeNames(draft, base).length;
}

export function domainsChange(draft: SettingsDraft, base: Baseline): "add" | "remove" | null {
	const isPublic = draft.isPublic ?? base.isPublic;
	if (isPublic === base.isPublic) return null;
	return isPublic ? "add" : "remove";
}

export type DraftErrors = Partial<Record<keyof SettingsDraft | `scaling.${string}`, ReactNode>>;

/** Fields whose errors show while the user types, before the first save attempt. */
export const LIVE_ERROR_KEYS = [...HEALTH_KEYS, "maxRestarts", "deployTimeout"] as const;

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
	if (next.healthEnabled) Object.assign(errors, healthErrors(draft, next));
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

function healthErrors(draft: SettingsDraft, next: Baseline): DraftErrors {
	const errors: DraftErrors = {};
	if (draft.healthPath !== undefined && !next.healthPath.startsWith("/")) errors.healthPath = "Start the path with /";
	const interval = Number(next.healthInterval);
	if (draft.healthInterval !== undefined && !(positive(next.healthInterval) && interval >= MIN_HEALTH_INTERVAL))
		errors.healthInterval = `Whole seconds, at least ${MIN_HEALTH_INTERVAL.toString()}`;
	const timeout = Number(next.healthTimeout);
	if (draft.healthTimeout !== undefined && !(positive(next.healthTimeout) && timeout <= MAX_HEALTH_TIMEOUT))
		errors.healthTimeout = `Whole seconds, 1 to ${MAX_HEALTH_TIMEOUT.toString()}`;
	const timingEdited = draft.healthInterval !== undefined || draft.healthTimeout !== undefined;
	const timingValid = errors.healthInterval === undefined && errors.healthTimeout === undefined;
	const order = timingEdited && timingValid ? healthTimeoutError(next.healthInterval, next.healthTimeout) : null;
	if (order !== null) errors.healthTimeout = order;
	if (draft.healthFailures !== undefined && !positive(next.healthFailures))
		errors.healthFailures = "A whole number, at least 1";
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

function healthFrom(next: Baseline, current: HealthCheckConfig | undefined): HealthCheckConfig {
	return create(HealthCheckConfigSchema, {
		initialDelaySeconds: current?.initialDelaySeconds ?? next.healthDelay,
		path: next.healthPath.trim(),
		intervalSeconds: Number(next.healthInterval.trim()),
		timeoutSeconds: Number(next.healthTimeout.trim()),
		failureThreshold: Number(next.healthFailures.trim()),
	});
}

function applyDraft(spec: DeploymentSpec, draft: SettingsDraft, base: Baseline): DeploymentSpec {
	const next = clone(DeploymentSpecSchema, spec);
	if (next.spec.case !== "service") return next;
	const svc = next.spec.value;
	const want = resolved(draft, base);
	if (draft.image !== undefined && want.image.trim() !== (svc.build?.image ?? "")) {
		svc.build = create(BuildSourceSchema, { type: IMAGE_BUILD, image: want.image.trim() });
	}
	if (draft.port !== undefined) svc.port = Number(want.port.trim());
	if (draft.observability !== undefined) svc.observability = create(ObservabilityConfigSchema, want.observability);
	if (healthChanges(want, base).length > 0)
		svc.healthCheck = want.healthEnabled ? healthFrom(want, svc.healthCheck) : undefined;
	if (draft.command !== undefined) svc.command = commandEntries(want.command);
	if (draft.args !== undefined) svc.args = commandEntries(want.args);
	return next;
}

interface RegionDeploy {
	commit: SourceCommit | undefined;
	region: string;
	spec: DeploymentSpec;
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

export function planSave(regions: RegionView[], draft: SettingsDraft, base: Baseline): RegionDeploy[] {
	const redeployAll = domainsChange(draft, base) !== null || variablesChanged(draft);
	const deploys: RegionDeploy[] = [];
	for (const region of deployedRegions(regions)) {
		const current = region.current;
		if (current?.spec === undefined) continue;
		const spec = applyDraft(current.spec, draft, base);
		if (!redeployAll && equals(DeploymentSpecSchema, spec, current.spec)) continue;
		deploys.push({
			region: region.name,
			spec,
			commit:
				draft.image === undefined || draft.image.trim() === getServiceSpec(current)?.build?.image
					? current.commit
					: undefined,
		});
	}
	return deploys;
}

/** The spec of one deployment to every planned region, each region keeping its own sizing. */
export function mergedSpec(deploys: readonly RegionDeploy[], spec: DeploymentSpec): DeploymentSpec {
	const next = clone(DeploymentSpecSchema, spec);
	if (next.spec.case !== "service") return next;
	next.spec.value.regions = Object.fromEntries(
		deploys.flatMap((d) => {
			const size = d.spec.spec.case === "service" ? d.spec.spec.value.regions[d.region] : undefined;
			return size === undefined ? [] : [[d.region, size]];
		}),
	);
	return next;
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
