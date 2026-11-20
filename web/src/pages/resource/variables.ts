import { createConnectQueryKey } from "@connectrpc/connect-query";
import type { QueryClient } from "@tanstack/react-query";
import { getResourceEnv } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";

import { getServiceSpec } from "@/lib/deployment-utils";
import { isEnvKey } from "@/lib/dotenv";

import { isDeployed, type RegionView } from "./model";

/** Why name cannot be added as a new variable, given the names already in use. */
export function variableNameProblem(name: string, taken: ReadonlySet<string>): string | undefined {
	if (name === "") return "Enter a name.";
	if (!isEnvKey(name)) return "Use letters, digits and underscores, not starting with a digit.";
	if (taken.has(name)) return `${name} is already a variable.`;
	return undefined;
}

function variablesKey(deployment: Deployment): string {
	const env = Object.entries(getServiceSpec(deployment)?.env ?? {}).toSorted(([a], [b]) => a.localeCompare(b));
	return JSON.stringify(env);
}

/** The deployed regions when their deployments run different variables, else none. */
export function regionsWithDifferentVariables(regions: RegionView[]): string[] {
	const deployed = regions
		.filter(isDeployed)
		.flatMap((r) => (r.current === undefined ? [] : [{ name: r.name, key: variablesKey(r.current) }]));
	return new Set(deployed.map((r) => r.key)).size > 1 ? deployed.map((r) => r.name).toSorted() : [];
}

/** Refetches the variables of one resource in one environment. */
export async function refreshVariables(client: QueryClient, resourceId: string, environmentId: string): Promise<void> {
	await client.invalidateQueries({
		queryKey: createConnectQueryKey({
			schema: getResourceEnv,
			input: { resourceId, environmentId },
			cardinality: "finite",
		}),
	});
}

const REF_RE = /\$\{\{\s*([a-z0-9](?:[-a-z0-9]{0,61}[a-z0-9])?)\.([A-Za-z_][A-Za-z0-9_]*)\s*\}\}/g;
const SECRETS = "secrets";
const HIDDEN = "••••";

/** What the references in a value resolve against: the environment's secrets and each resource's outputs. */
export interface ReferenceContext {
	environment: string;
	secrets: ReadonlySet<string>;
	outputs: ReadonlyMap<string, ReadonlyMap<string, string>>;
}

export interface Resolution {
	tone: "ok" | "secret" | "bad";
	text: string;
}

/** The reference a variable uses to read key of source. */
export function referenceOf(source: string, key: string): string {
	return `\${{ ${source}.${key} }}`;
}

/** How a value with references resolves, or null when it has none. */
export function resolveReferences(value: string, ctx: ReferenceContext): Resolution | null {
	const refs = [...value.matchAll(REF_RE)];
	if (refs.length === 0) return null;
	const shown: string[] = [];
	let direct = false;
	for (const [, source = "", key = ""] of refs) {
		if (source === SECRETS) {
			if (!ctx.secrets.has(key)) return { tone: "bad", text: `No secret named ${key} in ${ctx.environment}` };
			shown.push(HIDDEN);
			direct = true;
			continue;
		}
		const output = ctx.outputs.get(source);
		if (output === undefined) return { tone: "bad", text: `No resource named ${source}` };
		const resolved = output.get(key);
		if (resolved === undefined) return { tone: "bad", text: `${source} doesn’t provide ${key}` };
		shown.push(resolved.replaceAll(REF_RE, HIDDEN));
	}
	if (refs.length === 1 && refs[0]?.[0] === value.trim()) {
		if (direct) return { tone: "secret", text: `secret in ${ctx.environment}, value hidden` };
		return { tone: "ok", text: `→ ${shown[0] ?? ""}` };
	}
	let index = 0;
	return { tone: "ok", text: `→ ${value.replaceAll(REF_RE, () => shown[index++] ?? "")}` };
}

/** The variable name a picked reference gets: a secret keeps its name, an output is prefixed with its resource. */
export function referenceKey(source: string, key: string): string {
	if (source === SECRETS) return key;
	return `${source.toUpperCase().replaceAll("-", "_")}_${key === "PRIVATE_URL" ? "URL" : key}`;
}
