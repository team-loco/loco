import { clone, create, equals } from "@bufbuild/protobuf";
import {
	BuildSourceSchema,
	DeploymentSpecSchema,
	HealthCheckConfigSchema,
	type DeploymentSpec,
	type HealthCheckConfig,
} from "@gen/loco/deployment/v1/deployment_pb";

import { getServiceSpec } from "@/lib/deployment-utils";
import { IMAGE_RE } from "@/lib/new-resource";
import { SUBDOMAIN_RE } from "@/lib/platform-domain";

import { isDeployed, type RegionView } from "./model";

export const SECRET_NAME = /^[A-Z_][A-Z0-9_]*$/;

const IMAGE_BUILD = "image";
const WHOLE = /^\d+$/;

export interface SettingsDraft {
	name?: string;
	image?: string;
	replicas?: Record<string, number>;
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
	image: string;
	cpu: string;
	memory: string;
	port: string;
	healthPath: string;
	healthInterval: string;
	healthTimeout: string;
	healthFailures: string;
	replicas: Record<string, number>;
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
	regions: RegionView[],
	isPublic: boolean,
	defaultHealth: HealthCheckConfig | undefined,
): Baseline {
	const template = templateRegion(regions)?.current;
	const svc = template === undefined ? undefined : getServiceSpec(template);
	const health = svc?.healthCheck ?? defaultHealth;
	const deployed = deployedRegions(regions);
	return {
		name,
		image: svc?.build?.image ?? "",
		cpu: svc?.cpu ?? "",
		memory: svc?.memory ?? "",
		port: svc === undefined ? "" : svc.port.toString(),
		healthPath: health?.path ?? "",
		healthInterval: health === undefined ? "" : health.intervalSeconds.toString(),
		healthTimeout: health === undefined ? "" : health.timeoutSeconds.toString(),
		healthFailures: health === undefined ? "" : health.failureThreshold.toString(),
		replicas: Object.fromEntries(deployed.map((r) => [r.name, r.current?.replicas ?? 0])),
		regions: Object.fromEntries(regions.map((r) => [r.name, isDeployed(r)])),
		isPublic,
	};
}

export function resolved(draft: SettingsDraft, base: Baseline): Baseline {
	return {
		name: draft.name ?? base.name,
		image: draft.image ?? base.image,
		cpu: draft.cpu ?? base.cpu,
		memory: draft.memory ?? base.memory,
		port: draft.port ?? base.port,
		healthPath: draft.healthPath ?? base.healthPath,
		healthInterval: draft.healthInterval ?? base.healthInterval,
		healthTimeout: draft.healthTimeout ?? base.healthTimeout,
		healthFailures: draft.healthFailures ?? base.healthFailures,
		replicas: { ...base.replicas, ...draft.replicas },
		regions: { ...base.regions, ...draft.regions },
		isPublic: draft.isPublic ?? base.isPublic,
	};
}

const SCALAR_KEYS = [
	"name",
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
	count += Object.keys(next.replicas).filter((r) => next.replicas[r] !== base.replicas[r]).length;
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

export type DraftErrors = Partial<Record<keyof SettingsDraft | `replicas.${string}`, string>>;

function positive(value: string): boolean {
	return WHOLE.test(value.trim()) && Number(value.trim()) > 0;
}

export function validateDraft(draft: SettingsDraft, base: Baseline, served: string[]): DraftErrors {
	const next = resolved(draft, base);
	const errors: DraftErrors = {};
	if (draft.name !== undefined && next.name.trim() === "") errors.name = "Enter a name";
	if (draft.image !== undefined && !IMAGE_RE.test(next.image.trim())) errors.image = "Use registry/repository:tag";
	if (draft.cpu !== undefined && next.cpu.trim() === "") errors.cpu = "Enter a CPU amount";
	if (draft.memory !== undefined && next.memory.trim() === "") errors.memory = "Enter a memory amount";
	if (draft.port !== undefined && !positive(next.port)) errors.port = "Enter a port number";
	if (draft.healthPath !== undefined && !next.healthPath.startsWith("/")) errors.healthPath = "Start the path with /";
	if (draft.healthInterval !== undefined && !positive(next.healthInterval))
		errors.healthInterval = "Whole seconds, at least 1";
	if (draft.healthTimeout !== undefined && !positive(next.healthTimeout))
		errors.healthTimeout = "Whole seconds, at least 1";
	if (draft.healthFailures !== undefined && !positive(next.healthFailures))
		errors.healthFailures = "A whole number, at least 1";
	if (!Object.values(next.regions).some(Boolean)) errors.regions = "Keep at least one region";
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

function applyDraft(
	spec: DeploymentSpec,
	replicas: number | undefined,
	draft: SettingsDraft,
	base: Baseline,
): DeploymentSpec {
	const next = clone(DeploymentSpecSchema, spec);
	if (next.spec.case !== "service") return next;
	const svc = next.spec.value;
	const want = resolved(draft, base);
	if (draft.image !== undefined && want.image.trim() !== (svc.build?.image ?? "")) {
		svc.build = create(BuildSourceSchema, { type: IMAGE_BUILD, image: want.image.trim() });
	}
	if (draft.cpu !== undefined) svc.cpu = want.cpu.trim();
	if (draft.memory !== undefined) svc.memory = want.memory.trim();
	if (draft.port !== undefined) svc.port = Number(want.port.trim());
	if (healthEdited(draft)) svc.healthCheck = healthFrom(want, svc.healthCheck);
	if (replicas !== undefined) {
		svc.minReplicas = replicas;
		svc.maxReplicas = replicas;
	}
	return next;
}

interface RegionDeploy {
	region: string;
	spec: DeploymentSpec;
	added: boolean;
}

interface SavePlan {
	deploy: RegionDeploy[];
	retire: { region: string; deploymentId: string }[];
}

export function planSave(regions: RegionView[], draft: SettingsDraft, base: Baseline): SavePlan {
	const want = resolved(draft, base);
	const redeployAll = domainsChange(draft, base) !== null;
	const template = templateRegion(regions)?.current;
	const plan: SavePlan = { deploy: [], retire: [] };
	for (const region of regions) {
		const on = want.regions[region.name] ?? false;
		const current = region.current;
		if (isDeployed(region) && current !== undefined) {
			if (!on) {
				plan.retire.push({ region: region.name, deploymentId: current.id });
				continue;
			}
			if (current.spec === undefined) continue;
			const requested = draft.replicas?.[region.name];
			const replicas = requested === current.replicas ? undefined : requested;
			const spec = applyDraft(current.spec, replicas, draft, base);
			if (redeployAll || !equals(DeploymentSpecSchema, spec, current.spec)) {
				plan.deploy.push({ region: region.name, spec, added: false });
			}
			continue;
		}
		if (!on || template?.spec === undefined) continue;
		const replicas = draft.replicas?.[region.name] ?? template.replicas;
		plan.deploy.push({ region: region.name, spec: applyDraft(template.spec, replicas, draft, base), added: true });
	}
	return plan;
}
