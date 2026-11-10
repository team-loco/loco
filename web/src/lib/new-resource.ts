import { ConnectError } from "@connectrpc/connect";
import { ViolationsSchema } from "@gen/buf/validate/validate_pb";
import type { PlatformDomain } from "@gen/loco/domain/v1/domain_pb";

import { isEnvKey } from "@/lib/dotenv";
import { parseImage } from "@/lib/image";
import { SUBDOMAIN_RE, type DomainCheck } from "@/lib/platform-domain";
import { MAX_SLUG_LENGTH, slug } from "@/lib/slug";

export const IMAGE_RE = /^([a-z0-9\-._]+(:[0-9]+)?(\/[a-z0-9\-._]+)*)(:[a-z0-9\-._]+)?(@sha256:[a-f0-9]{64})?$/;
const RESERVED_SLUG_RE = /^(localhost|resource-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$/;
const DIGITS_RE = /^\d+$/;
const LOCO_YAML_VERSION = 1;

export interface EnvRow {
	id: string;
	key: string;
	value: string;
	secret?: boolean;
}

export interface HealthForm {
	path: string;
	interval: string;
	timeout: string;
	failAfter: string;
}

export interface NewResourceForm {
	image: string;
	name: string;
	regions: string[];
	cpu: string;
	memory: string;
	replicas: number;
	isPublic: boolean;
	subdomain: string;
	platformDomain: string;
	port: string;
	vars: EnvRow[];
	health: HealthForm;
}

export interface FormErrors {
	regions: string | null;
	subdomain: string | null;
	cpu: string | null;
	memory: string | null;
	env: string | null;
}

export type ApiField = "port" | "path" | "interval" | "timeout" | "failAfter" | "cpu" | "memory" | "replicas";

export interface ApiFieldError {
	field: ApiField;
	message: string;
}

export function nameFromImage(image: string, takenSlugs: ReadonlySet<string>): string {
	const base = slug(parseImage(image.trim()).repository);
	if (base === "") return "";
	let name = base;
	for (let i = 2; takenSlugs.has(name); i++) name = `${base}-${i.toString()}`;
	return name;
}

const SECRET_WORDS = new Set(["SECRET", "TOKEN", "PASSWORD", "PASSWD", "KEY", "CREDENTIALS", "DSN", "PRIVATE"]);
const SECRET_NAMES = new Set(["DATABASE_URL"]);

export function looksSecret(key: string): boolean {
	const upper = key.toUpperCase();
	return SECRET_NAMES.has(upper) || upper.split("_").some((word) => SECRET_WORDS.has(word));
}

export function servedDomains(domains: PlatformDomain[], regions: string[]): string[] {
	return domains.filter((d) => regions.every((r) => d.regions.includes(r))).map((d) => d.domain);
}

function isBlank(row: EnvRow): boolean {
	return row.key.trim() === "" && row.value === "";
}

export function envVars(rows: EnvRow[]): [string, string][] {
	return rows.filter((r) => !isBlank(r)).map((r) => [r.key.trim(), r.value]);
}

export function deploymentVariables(rows: EnvRow[]) {
	return {
		env: Object.fromEntries(envVars(rows.filter((row) => !row.secret))),
		secrets: Object.fromEntries(envVars(rows.filter((row) => row.secret))),
	};
}

export function envError(rows: EnvRow[]): string | null {
	const seen = new Set<string>();
	for (const row of rows) {
		if (isBlank(row)) continue;
		const key = row.key.trim();
		const value = row.value;
		if (row.secret && !/^[A-Z_][A-Z0-9_]*$/.test(key))
			return `${key || "Secret name"} must use uppercase letters, digits and _, not starting with a digit`;
		if (key === "") return "A variable has a value but no name";
		if (!isEnvKey(key)) return `${key} isn't a valid name: use letters, digits and _, not starting with a digit`;
		if (value === "") return `${key} has no value`;
		if (seen.has(key)) return `${key} is set twice`;
		seen.add(key);
	}
	return null;
}

export function mergeEnv(rows: EnvRow[], pairs: [string, string][], newId: () => string): EnvRow[] {
	const pasted = new Map(pairs);
	const kept = rows
		.filter((r) => !isBlank(r))
		.map((r) => {
			const value = pasted.get(r.key.trim());
			return value === undefined ? r : { ...r, value };
		});
	const present = new Set(kept.map((r) => r.key.trim()));
	return [...kept, ...pairs.filter(([key]) => !present.has(key)).map(([key, value]) => ({ id: newId(), key, value }))];
}

export function nameError(name: string, takenSlugs: ReadonlyMap<string, string>): string | null {
	if (name.trim() === "") return "Enter a name";
	if (Array.from(name).length > MAX_SLUG_LENGTH) return `Use at most ${MAX_SLUG_LENGTH.toString()} characters`;
	const s = slug(name);
	if (s === "") return "Use at least one letter or digit";
	if (RESERVED_SLUG_RE.test(s)) return `${s} is reserved`;
	const holder = takenSlugs.get(s);
	if (holder === undefined) return null;
	return holder === name ? `${name} already exists in this workspace` : `${holder} already uses the name ${s}`;
}

function subdomainError(form: NewResourceForm, availability: DomainCheck["state"]): string | null {
	if (!form.isPublic) return null;
	if (form.platformDomain === "") return "No platform domain is served in every selected region";
	if (form.subdomain === "") return "Enter a subdomain";
	if (!SUBDOMAIN_RE.test(form.subdomain)) return "Use lowercase letters, digits and hyphens";
	if (availability === "taken") return `${form.subdomain}.${form.platformDomain} is taken`;
	return null;
}

export function imageError(image: string): string | null {
	const trimmed = image.trim();
	if (trimmed === "") return "Enter an image";
	return IMAGE_RE.test(trimmed) ? null : "Use registry/repository:tag";
}

export function validateForm(form: NewResourceForm, availability: DomainCheck["state"]): FormErrors {
	return {
		regions: form.regions.length === 0 ? "Choose at least one region" : null,
		subdomain: subdomainError(form, availability),
		cpu: form.cpu.trim() === "" ? "Enter a CPU amount" : null,
		memory: form.memory.trim() === "" ? "Enter a memory amount" : null,
		env: envError(form.vars),
	};
}

export function firstError(errors: FormErrors): string | null {
	const ordered: (string | null)[] = [errors.regions, errors.subdomain, errors.cpu, errors.memory, errors.env];
	return ordered.find((e) => e !== null) ?? null;
}

export function healthSummary(health: HealthForm): string {
	return health.path === "" ? "None" : `${health.path} · every ${health.interval}s`;
}

export function healthCheckSpec(health: HealthForm, initialDelaySeconds: number) {
	if (health.path === "") return undefined;
	return {
		path: health.path,
		intervalSeconds: Number(health.interval),
		timeoutSeconds: Number(health.timeout),
		failureThreshold: Number(health.failAfter),
		initialDelaySeconds,
	};
}

const VIOLATION_FIELDS: Partial<Record<string, ApiField>> = {
	port: "port",
	path: "path",
	interval_seconds: "interval",
	timeout_seconds: "timeout",
	failure_threshold: "failAfter",
	cpu: "cpu",
	memory: "memory",
	min_replicas: "replicas",
	max_replicas: "replicas",
};

export function apiFieldError(err: unknown): ApiFieldError | null {
	if (!(err instanceof ConnectError)) return null;
	for (const v of err.findDetails(ViolationsSchema).flatMap((d) => d.violations)) {
		const name = v.field?.elements.at(-1)?.fieldName;
		const field = name === undefined ? undefined : VIOLATION_FIELDS[name];
		if (field !== undefined) return { field, message: v.message.charAt(0).toUpperCase() + v.message.slice(1) };
	}
	return null;
}

const BLOCK_PLAIN_RE = /^[A-Za-z0-9_./-][A-Za-z0-9_./:@-]*$/;
const FLOW_PLAIN_RE = /^[A-Za-z0-9_./-]+$/;
const YAML_SPECIAL_RE =
	/^([-+.]?[\d._]+([eE][-+]?\d+)?|\d+(:\d+)+|0x[\da-f]+|0o[0-7]+|y|yes|n|no|true|false|on|off|null|~|\.inf|\.nan)$/i;

function scalar(value: string, plain: RegExp): string {
	return plain.test(value) && !YAML_SPECIAL_RE.test(value) && !value.endsWith(":") ? value : JSON.stringify(value);
}

function yamlInt(value: string): string {
	return DIGITS_RE.test(value) ? value : JSON.stringify(value);
}

function healthYaml(health: HealthForm, flow: (v: string) => string): string {
	const fields = [
		`path: ${flow(health.path)}`,
		...(
			[
				["interval", health.interval],
				["timeout", health.timeout],
				["failThreshold", health.failAfter],
			] as const
		)
			.filter(([, v]) => v !== "")
			.map(([k, v]) => `${k}: ${yamlInt(v)}`),
	];
	return `    health: { ${fields.join(", ")} }`;
}

export interface LocoYamlInput {
	schemaUrl: string;
	form: NewResourceForm;
}

export function locoYaml({ schemaUrl, form }: LocoYamlInput): string {
	const name = scalar(form.name, BLOCK_PLAIN_RE);
	const flow = (v: string) => scalar(v, FLOW_PLAIN_RE);
	const replicas = form.replicas.toString();
	const lines = [
		...(schemaUrl === "" ? [] : [`# yaml-language-server: $schema=${schemaUrl}`]),
		`version: ${LOCO_YAML_VERSION.toString()}`,
		`partial: ${scalar(slug(form.name), FLOW_PLAIN_RE)}`,
		"",
		"services:",
		`  ${name}:`,
		`    image: ${scalar(form.image.trim(), BLOCK_PLAIN_RE)}`,
		`    port: ${yamlInt(form.port)}`,
	];
	if (form.health.path !== "") lines.push(healthYaml(form.health, flow));
	if (form.isPublic) lines.push(`    domains: [${flow(`${form.subdomain}.${form.platformDomain}`)}]`);
	const vars = envVars(form.vars.filter((row) => !row.secret));
	const secrets = form.vars.filter((row) => row.secret && row.key.trim() !== "").map((row) => row.key.trim());
	if (secrets.length > 0) lines.push(`    secrets: [${secrets.map(flow).join(", ")}]`);
	if (vars.length > 0) {
		lines.push("    env:");
		for (const [key, value] of vars) lines.push(`      ${scalar(key, FLOW_PLAIN_RE)}: ${JSON.stringify(value)}`);
	}
	if (form.regions.length > 0) lines.push("    regions:");
	for (const region of form.regions) {
		const sizing = `cpu: ${flow(form.cpu.trim())}, memory: ${flow(form.memory.trim())}`;
		lines.push(`      ${flow(region)}: { ${sizing}, replicas: { min: ${replicas}, max: ${replicas} } }`);
	}
	return `${lines.join("\n")}\n`;
}
