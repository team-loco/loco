import type { PlatformDomain } from "@gen/loco/domain/v1/domain_pb";

import { parseDotEnvLine } from "@/lib/dotenv";
import { parseImage } from "@/lib/image";
import { SUBDOMAIN_RE, type DomainCheck } from "@/lib/platform-domain";
import { MAX_SLUG_LENGTH, slug } from "@/lib/slug";

export const IMAGE_RE = /^([a-z0-9\-._]+(:[0-9]+)?(\/[a-z0-9\-._]+)*)(:[a-z0-9\-._]+)?(@sha256:[a-f0-9]{64})?$/;
const RESERVED_SLUG_RE = /^(localhost|resource-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})$/;
const PORT_RE = /^\d+$/;
const LOCO_YAML_VERSION = 1;

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
	envText: string;
	healthPath: string;
}

export interface FormErrors {
	image: string | null;
	name: string | null;
	regions: string | null;
	cpu: string | null;
	memory: string | null;
	subdomain: string | null;
	port: string | null;
	env: string | null;
	healthPath: string | null;
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

export function parseEnvText(text: string): { vars: [string, string][]; error: string | null } {
	const vars = new Map<string, string>();
	let error: string | null = null;
	text.split(/\r?\n/).forEach((raw, i) => {
		const line = parseDotEnvLine(raw);
		if (line.kind === "blank") return;
		if (line.kind === "invalid") {
			error ??= `Line ${(i + 1).toString()} is not KEY=value with a letter or _ first`;
			return;
		}
		if (line.value === "") error ??= `${line.key} has no value`;
		vars.set(line.key, line.value);
	});
	return { vars: [...vars], error };
}

function nameError(name: string, takenSlugs: ReadonlyMap<string, string>): string | null {
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

export function validateForm(
	form: NewResourceForm,
	takenSlugs: ReadonlyMap<string, string>,
	availability: DomainCheck["state"],
): FormErrors {
	return {
		image: imageError(form.image),
		name: nameError(form.name, takenSlugs),
		regions: form.regions.length === 0 ? "Choose at least one region" : null,
		cpu: form.cpu.trim() === "" ? "Enter a CPU amount" : null,
		memory: form.memory.trim() === "" ? "Enter a memory amount" : null,
		subdomain: subdomainError(form, availability),
		port: PORT_RE.test(form.port) ? null : "Enter a port number",
		env: parseEnvText(form.envText).error,
		healthPath: form.healthPath === "" || form.healthPath.startsWith("/") ? null : "Start the path with /",
	};
}

export function errorCount(errors: FormErrors): number {
	return Object.values(errors).filter((e) => e !== null).length;
}

const BLOCK_PLAIN_RE = /^[A-Za-z0-9_./-][A-Za-z0-9_./:@-]*$/;
const FLOW_PLAIN_RE = /^[A-Za-z0-9_./-]+$/;
const YAML_SPECIAL_RE =
	/^([-+.]?[\d._]+([eE][-+]?\d+)?|\d+(:\d+)+|0x[\da-f]+|0o[0-7]+|y|yes|n|no|true|false|on|off|null|~|\.inf|\.nan)$/i;

function scalar(value: string, plain: RegExp): string {
	return plain.test(value) && !YAML_SPECIAL_RE.test(value) && !value.endsWith(":") ? value : JSON.stringify(value);
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
		`    port: ${PORT_RE.test(form.port) ? form.port : JSON.stringify(form.port)}`,
	];
	if (form.healthPath !== "") lines.push(`    health: { path: ${flow(form.healthPath)} }`);
	if (form.isPublic) lines.push(`    domains: [${flow(`${form.subdomain}.${form.platformDomain}`)}]`);
	const vars = parseEnvText(form.envText).vars;
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
