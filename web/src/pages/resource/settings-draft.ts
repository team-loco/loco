import type { DefaultServiceConfig } from "@gen/loco/config/v1/config_pb";
import { clone, create, equals } from "@bufbuild/protobuf";
import {
	BuildSourceSchema,
	DeploymentSpecSchema,
	HealthCheckConfigSchema,
	type DeploymentSpec,
	type HealthCheckConfig,
	type SourceCommit,
} from "@gen/loco/deployment/v1/deployment_pb";
import { RegionTargetSchema, type RegionTarget } from "@gen/loco/resource/v1/resource_pb";

import { getServiceSpec } from "@/lib/deployment-utils";
import { IMAGE_RE, scalingError, scalingSpec, type Scaling } from "@/lib/new-resource";
import { SUBDOMAIN_RE } from "@/lib/platform-domain";

import { isDeployed, type RegionView } from "./model";

// These fields make up the resource's region map, which UpdateResource replaces in one request
// for every environment; the other spec fields redeploy this environment's regions.
export const REGION_MAP_KEYS = ["regions", "scaling", "cpu", "memory"] as const;

export const SECRET_NAME = /^[A-Z_][A-Z0-9_]*$/;

const IMAGE_BUILD = "image";
const WHOLE = /^\d+$/;

export interface SettingsDraft {
	name?: string;
	description?: string;
	image?: string;
	scaling?: Record<string, Scaling>;
	cpu?: string;
	memory?: string;
	port?: string;
	healthPath?: string;
	healthInterval?: string;
	healthTimeout?: string;
	healthFailures?: string;
	regions?: Record<string, boolean>;
	secrets?: Record<string, string>;
	removedSecrets?: string[];
	isPublic?: boolean;
	subdomain?: string;
	platformDomain?: string;
}

export interface Baseline {
	name: string;
	description: string;
	image: string;
	cpu: string;
	memory: string;
	port: string;
	healthPath: string;
	healthInterval: string;
	healthTimeout: string;
	healthFailures: string;
	scaling: Record<string, Scaling>;
	regions: Record<string, boolean>;
	isPublic: boolean;
}

function deployedRegions(regions: RegionView[]): RegionView[] {
	return regions.filter(isDeployed);
}

function templateRegion(regions: RegionView[]): RegionView | undefined {
	return deployedRegions(regions)[0];
}

export function baseline(
	name: string,
	description: string,
	regions: RegionView[],
	configured: string[],
	isPublic: boolean,
	defaults: DefaultServiceConfig,
): Baseline {
	const template = templateRegion(regions)?.current;
	const svc = template === undefined ? undefined : getServiceSpec(template);
	const health = svc?.healthCheck ?? defaults.healthCheck;
	const deployed = deployedRegions(regions);
	return {
		name,
		description,
		image: svc?.build?.image ?? "",
		cpu: svc?.cpu ?? "",
		memory: svc?.memory ?? "",
		port: svc === undefined ? "" : svc.port.toString(),
		healthPath: health?.path ?? "",
		healthInterval: health === undefined ? "" : health.intervalSeconds.toString(),
		healthTimeout: health === undefined ? "" : health.timeoutSeconds.toString(),
		healthFailures: health === undefined ? "" : health.failureThreshold.toString(),
		scaling: Object.fromEntries(
			deployed.map((r) => {
				const spec = r.current === undefined ? undefined : getServiceSpec(r.current);
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
	};
}

export function resolved(draft: SettingsDraft, base: Baseline): Baseline {
	return {
		name: draft.name ?? base.name,
		description: draft.description ?? base.description,
		image: draft.image ?? base.image,
		cpu: draft.cpu ?? base.cpu,
		memory: draft.memory ?? base.memory,
		port: draft.port ?? base.port,
		healthPath: draft.healthPath ?? base.healthPath,
		healthInterval: draft.healthInterval ?? base.healthInterval,
		healthTimeout: draft.healthTimeout ?? base.healthTimeout,
		healthFailures: draft.healthFailures ?? base.healthFailures,
		scaling: { ...base.scaling, ...draft.scaling },
		regions: { ...base.regions, ...draft.regions },
		isPublic: draft.isPublic ?? base.isPublic,
	};
}

const SCALAR_KEYS = [
	"name",
	"description",
	"image",
	"cpu",
	"memory",
	"port",
	"healthPath",
	"healthInterval",
	"healthTimeout",
	"healthFailures",
] as const;

function differs(a: string, b: string): boolean {
	return a.trim() !== b.trim();
}

export function changeCount(draft: SettingsDraft, base: Baseline): number {
	const next = resolved(draft, base);
	let count = SCALAR_KEYS.filter((k) => differs(next[k], base[k])).length;
	count += Object.keys(draft.scaling ?? {}).filter((r) => !sameScaling(next.scaling[r], base.scaling[r])).length;
	count += Object.keys(next.regions).filter((r) => next.regions[r] !== base.regions[r]).length;
	count += Object.keys(draft.secrets ?? {}).length;
	count += (draft.removedSecrets ?? []).length;
	if (next.isPublic !== base.isPublic) count++;
	return count;
}

export function domainsChange(draft: SettingsDraft, base: Baseline): "add" | "remove" | null {
	const isPublic = draft.isPublic ?? base.isPublic;
	if (isPublic === base.isPublic) return null;
	return isPublic ? "add" : "remove";
}

export type DraftErrors = Partial<Record<keyof SettingsDraft | `scaling.${string}`, string>>;

function positive(value: string): boolean {
	return WHOLE.test(value.trim()) && Number(value.trim()) > 0;
}

export function validateDraft(
	draft: SettingsDraft,
	base: Baseline,
	regions: RegionView[],
	served: string[],
	replicaLimit: number,
): DraftErrors {
	const next = resolved(draft, base);
	const errors: DraftErrors = {};
	for (const [region, scaling] of Object.entries(draft.scaling ?? {})) {
		const error = scalingError(scaling, replicaLimit);
		if (error !== null) errors[`scaling.${region}`] = error;
	}
	if (draft.name !== undefined && next.name.trim() === "") errors.name = "Enter a name";
	if (draft.image !== undefined && !IMAGE_RE.test(next.image.trim())) errors.image = "Use registry/repository:tag";
	if (draft.port !== undefined && !positive(next.port)) errors.port = "Enter a port number";
	if (draft.healthPath !== undefined && !next.healthPath.startsWith("/")) errors.healthPath = "Start the path with /";
	if (draft.healthInterval !== undefined && !positive(next.healthInterval))
		errors.healthInterval = "Whole seconds, at least 1";
	if (draft.healthTimeout !== undefined && !positive(next.healthTimeout))
		errors.healthTimeout = "Whole seconds, at least 1";
	if (draft.healthFailures !== undefined && !positive(next.healthFailures))
		errors.healthFailures = "A whole number, at least 1";
	if (!Object.values(next.regions).some(Boolean)) errors.regions = "Keep at least one region";
	else if (regionMapChanged(draft, base) && planSave(regions, draft, base).length > 0)
		errors.regions = "Save region, size and scaling changes separately from image, port, health and domain changes";
	if (domainsChange(draft, base) === "add") {
		if (draft.platformDomain === undefined || !served.includes(draft.platformDomain))
			errors.platformDomain = "No platform domain is served in every region";
		if (!SUBDOMAIN_RE.test(draft.subdomain ?? "")) errors.subdomain = "Use lowercase letters, digits and hyphens";
	}
	for (const name of Object.keys(draft.secrets ?? {})) {
		if (!SECRET_NAME.test(name)) errors.secrets = `${name}: use capital letters, digits and underscores`;
	}
	return errors;
}

function healthFrom(next: Baseline, current: HealthCheckConfig | undefined): HealthCheckConfig {
	return create(HealthCheckConfigSchema, {
		initialDelaySeconds: current?.initialDelaySeconds ?? 0,
		path: next.healthPath.trim(),
		intervalSeconds: Number(next.healthInterval.trim()),
		timeoutSeconds: Number(next.healthTimeout.trim()),
		failureThreshold: Number(next.healthFailures.trim()),
	});
}

function healthEdited(draft: SettingsDraft): boolean {
	return (
		draft.healthPath !== undefined ||
		draft.healthInterval !== undefined ||
		draft.healthTimeout !== undefined ||
		draft.healthFailures !== undefined
	);
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
	if (healthEdited(draft)) svc.healthCheck = healthFrom(want, svc.healthCheck);
	return next;
}

interface RegionDeploy {
	commit: SourceCommit | undefined;
	region: string;
	spec: DeploymentSpec;
}

export function planSave(regions: RegionView[], draft: SettingsDraft, base: Baseline): RegionDeploy[] {
	const redeployAll = domainsChange(draft, base) !== null;
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
	const fallback = Object.values(base.scaling)[0];
	if (fallback === undefined || !regionMapChanged(draft, base)) return undefined;
	const want = resolved(draft, base);
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

function sameScaling(a: Scaling | undefined, b: Scaling | undefined): boolean {
	return (
		a?.replicas === b?.replicas &&
		a?.maxReplicas === b?.maxReplicas &&
		(a?.replicas === a?.maxReplicas || a?.cpuTarget === b?.cpuTarget)
	);
}
