import { ConnectError } from "@connectrpc/connect";
import { withCode, type Message } from "@/components/design/InlineCode";
import { BadRequestSchema } from "@gen/google/rpc/error_details_pb";
import { ViolationsSchema } from "@gen/buf/validate/validate_pb";
import type { AppDomain } from "@gen/loco/domain/v1/domain_pb";

import { isEnvKey } from "@/lib/dotenv";
import { parseImage } from "@/lib/image";
import { SUBDOMAIN_RE, type DomainCheck } from "@/lib/app-domain";
import { slug } from "@/lib/slug";

export const IMAGE_RE = /^([a-z0-9\-._]+(:[0-9]+)?(\/[a-z0-9\-._]+)*)(:[a-z0-9\-._]+)?(@sha256:[a-f0-9]{64})?$/;

export interface EnvRow {
	id: string;
	key: string;
	value: string;
}

export interface HealthForm {
	enabled: boolean;
	path: string;
	interval: string;
	timeout: string;
	failAfter: string;
}

export interface NewResourceForm {
	image: string;
	name: string;
	slug: string;
	description: string;
	regions: string[];
	cpu: string;
	memory: string;
	replicas: number;
	maxReplicas: number;
	cpuTarget: number;
	isPublic: boolean;
	subdomain: string;
	appDomain: string;
	port: string;
	vars: EnvRow[];
	health: HealthForm;
	command: string;
	args: string;
}

export interface FormErrors {
	image: string | null;
	regions: string | null;
	port: string | null;
	subdomain: Message | null;
	env: string | null;
	scaling: string | null;
	healthPath: string | null;
	healthTimeout: string | null;
}

export type ApiField =
	"image" | "port" | "path" | "interval" | "timeout" | "failAfter" | "cpu" | "memory" | "replicas" | "command" | "args";

export interface ApiFieldError {
	field: ApiField;
	message: string;
}

const LINE_BREAK_RE = /\r?\n/;

/** The entries of a command or args field: one per line, blank lines left out, each kept as typed. */
export function commandEntries(text: string): string[] {
	return text.split(LINE_BREAK_RE).filter((line) => line.trim() !== "");
}

/** The text a command or args field shows for a list: one entry per line. */
export function commandText(entries: readonly string[]): string {
	return entries.join("\n");
}

export function nameFromImage(image: string, taken: ReadonlySet<string>): string {
	const base = slug(parseImage(image.trim()).repository);
	if (base === "") return "";
	let name = base;
	for (let i = 2; taken.has(name); i++) name = `${base}-${i.toString()}`;
	return name;
}

export function servedDomains(domains: AppDomain[], regions: string[]): string[] {
	return domains.filter((d) => regions.every((r) => d.regions.includes(r))).map((d) => d.domain);
}

function isBlank(row: EnvRow): boolean {
	return row.key.trim() === "" && row.value === "";
}

export function envVars(rows: EnvRow[]): [string, string][] {
	return rows.filter((r) => !isBlank(r)).map((r) => [r.key.trim(), r.value]);
}

const KEY_RULE = "Use letters, digits and _, not starting with a digit";

/** The problem with each variable row, by row id; rows without a problem are absent. */
export function envRowErrors(rows: EnvRow[]): Map<string, Message> {
	const errors = new Map<string, Message>();
	const counts = new Map<string, number>();
	for (const row of rows.filter((r) => !isBlank(r))) {
		const key = row.key.trim();
		counts.set(key, (counts.get(key) ?? 0) + 1);
	}
	for (const row of rows.filter((r) => !isBlank(r))) {
		const key = row.key.trim();
		if (key === "") errors.set(row.id, "Enter a name for this value");
		else if (!isEnvKey(key)) errors.set(row.id, KEY_RULE);
		else if ((counts.get(key) ?? 0) > 1) errors.set(row.id, withCode`${key} is set more than once`);
	}
	return errors;
}

export function envError(rows: EnvRow[]): string | null {
	const count = envRowErrors(rows).size;
	if (count === 0) return null;
	return count === 1 ? "Fix the highlighted variable" : `Fix the ${count.toString()} highlighted variables`;
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

function subdomainError(form: NewResourceForm, availability: DomainCheck["state"]): Message | null {
	if (!form.isPublic) return null;
	if (form.appDomain === "") return "No app domain is served in every selected region";
	if (form.subdomain === "") return "Enter a subdomain";
	if (!SUBDOMAIN_RE.test(form.subdomain)) return "Use lowercase letters, digits and hyphens";
	if (availability === "taken") return withCode`${form.subdomain}.${form.appDomain} is taken`;
	return null;
}

export function imageError(image: string): string | null {
	const trimmed = image.trim();
	if (trimmed === "") return "Enter a container image.";
	if (/\s/.test(trimmed)) return "An image reference can't contain spaces";
	if (IMAGE_RE.test(trimmed)) return null;
	if (IMAGE_RE.test(trimmed.toLowerCase())) return "Use lowercase letters in the image reference";
	return "Invalid image reference. Use a repository with a tag or digest.";
}

export function validateForm(
	form: NewResourceForm,
	availability: DomainCheck["state"],
	replicaLimit: number,
): FormErrors {
	return {
		image: imageError(form.image),
		regions: form.regions.length === 0 ? "Choose at least one region" : null,
		port: portError(form),
		subdomain: subdomainError(form, availability),
		env: envError(form.vars),
		scaling: scalingError(form, replicaLimit),
		healthPath: form.health.enabled && form.health.path.trim() === "" ? "Needs a path" : null,
		healthTimeout: !checksHealth(form.health) ? null : healthTimeoutError(form.health.interval, form.health.timeout),
	};
}

/** The error for a health check timeout that is not shorter than a positive interval; other values are left to their own checks. */
export function healthTimeoutError(interval: string, timeout: string): string | null {
	const every = Number(interval.trim());
	if (interval.trim() === "" || timeout.trim() === "" || !(every > 0)) return null;
	return Number(timeout.trim()) >= every ? "Must be below the interval" : null;
}

/** The error for a port outside the deployment spec's range, 1 through 65535. */
export function portRangeError(port: string): string | null {
	const text = port.trim();
	const value = Number(text);
	return /^\d+$/.test(text) && value >= 1 && value <= 65535 ? null : "Use a port from 1 to 65535";
}

// A service without a port is a worker, which takes no traffic and has nothing to probe.
function portError(form: NewResourceForm): string | null {
	if (form.port.trim() !== "") return portRangeError(form.port);
	if (form.isPublic) return "A public service needs a port";
	if (checksHealth(form.health)) return "A health check needs a port";
	return null;
}

export type Scaling = Pick<NewResourceForm, "replicas" | "maxReplicas" | "cpuTarget">;

export function scalingError(form: Scaling, replicaLimit: number): string | null {
	if (form.maxReplicas > replicaLimit) return `Use at most ${replicaLimit.toString()} replicas per region`;
	if (!Number.isInteger(form.replicas) || form.replicas < 1) return "Minimum replicas must be a positive whole number";
	if (!Number.isInteger(form.maxReplicas) || form.maxReplicas < form.replicas)
		return "Maximum replicas must be at least the minimum";
	if (
		form.replicas !== form.maxReplicas &&
		(!Number.isInteger(form.cpuTarget) || form.cpuTarget < 1 || form.cpuTarget > 100)
	)
		return "CPU target must be between 1 and 100%";
	return null;
}

export function scalingSpec(form: Scaling) {
	return {
		minReplicas: form.replicas,
		maxReplicas: form.maxReplicas,
		scalers: form.replicas === form.maxReplicas ? { enabled: false } : { enabled: true, cpuTarget: form.cpuTarget },
	};
}

export function firstError(errors: FormErrors): Message | null {
	const ordered: (Message | null)[] = [
		errors.image,
		errors.regions,
		errors.port,
		errors.subdomain,
		errors.env,
		errors.scaling,
		errors.healthPath,
		errors.healthTimeout,
	];
	return ordered.find((e) => e !== null) ?? null;
}

/** The start command as one line, or "Image default" when neither field is set. */
export function commandSummary(value: Pick<NewResourceForm, "command" | "args">): string {
	const words = [...commandEntries(value.command), ...commandEntries(value.args)];
	return words.length === 0 ? "Image default" : words.join(" ");
}

/** Whether the form sends a health check: switched on with a path. */
export function checksHealth(health: HealthForm): boolean {
	return health.enabled && health.path !== "";
}

export function healthCheckSpec(health: HealthForm, initialDelaySeconds: number) {
	if (!checksHealth(health)) return undefined;
	return {
		path: health.path,
		intervalSeconds: Number(health.interval),
		timeoutSeconds: Number(health.timeout),
		failureThreshold: Number(health.failAfter),
		initialDelaySeconds,
	};
}

const VIOLATION_FIELDS: Partial<Record<string, ApiField>> = {
	image: "image",
	port: "port",
	path: "path",
	interval_seconds: "interval",
	timeout_seconds: "timeout",
	failure_threshold: "failAfter",
	cpu: "cpu",
	memory: "memory",
	min_replicas: "replicas",
	max_replicas: "replicas",
	command: "command",
	args: "args",
};

export function apiFieldError(err: unknown): ApiFieldError | null {
	if (!(err instanceof ConnectError)) return null;
	const violations = [
		...err
			.findDetails(ViolationsSchema)
			.flatMap((d) => d.violations)
			.map((v) => ({ name: v.field?.elements.at(-1)?.fieldName, message: v.message })),
		...err
			.findDetails(BadRequestSchema)
			.flatMap((d) => d.fieldViolations)
			.map((v) => ({ name: v.field.split(".").at(-1), message: v.description })),
	];
	for (const { name, message } of violations) {
		const field = name === undefined ? undefined : VIOLATION_FIELDS[name];
		if (field !== undefined) return { field, message: message.charAt(0).toUpperCase() + message.slice(1) };
	}
	return null;
}
