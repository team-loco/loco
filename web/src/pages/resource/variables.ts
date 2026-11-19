import { createConnectQueryKey } from "@connectrpc/connect-query";
import type { QueryClient } from "@tanstack/react-query";
import { getResourceEnv } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { listSecrets } from "@gen/loco/secret/v1/secret-SecretService_connectquery";
import type { Secret, SecretUsage } from "@gen/loco/secret/v1/secret_pb";

import { isEnvKey } from "@/lib/dotenv";

import { SECRET_NAME } from "./settings-draft";

const USER_ENTITY = "user";
const SHORT_ID = 8;

export type SecretStatus = "set" | "missing" | "unused" | "new";

export interface SecretRow {
	name: string;
	secret: Secret | undefined;
	status: SecretStatus | undefined;
}

function rowStatus(stored: boolean, declared: ReadonlySet<string> | undefined, name: string): SecretStatus | undefined {
	if (declared === undefined) return undefined;
	if (!declared.has(name)) return "unused";
	if (stored) return "set";
	return "missing";
}

export function secretRows(secrets: Secret[], declaredNames: string[] | undefined, staged: string[]): SecretRow[] {
	const stored = new Map(secrets.map((s) => [s.name, s]));
	const declared = declaredNames === undefined ? undefined : new Set(declaredNames);
	const names = new Set([...stored.keys(), ...(declaredNames ?? []), ...staged]);
	return [...names]
		.toSorted((a, b) => a.localeCompare(b))
		.map((name) => {
			const secret = stored.get(name);
			const known = secret !== undefined || declared?.has(name) === true;
			const status = known ? rowStatus(secret !== undefined, declared, name) : "new";
			return { name, secret, status };
		});
}

export function setterLabel(secret: Secret, members: ReadonlyMap<string, string>): string {
	if (secret.updatedByType === USER_ENTITY)
		return members.get(secret.updatedById) ?? secret.updatedById.slice(0, SHORT_ID);
	return `${secret.updatedByType} token`;
}

export function secretNameProblem(name: string): string | undefined {
	if (name === "") return "Enter a name.";
	if (!SECRET_NAME.test(name)) return "Use capital letters, digits and underscores, not starting with a digit.";
	return undefined;
}

/** Why name cannot be added as a new secret or plain variable, given the names already in use. */
export function variableNameProblem(
	name: string,
	secret: boolean,
	taken: { secrets: ReadonlySet<string>; plain: ReadonlySet<string> },
): string | undefined {
	if (secret) {
		if (taken.plain.has(name)) return `${name} is already a plain variable.`;
		return secretNameProblem(name);
	}
	if (name === "") return "Enter a name.";
	if (!isEnvKey(name)) return "Use letters, digits and underscores, not starting with a digit.";
	if (taken.secrets.has(name) || taken.plain.has(name)) return `${name} is already a variable.`;
	return undefined;
}

export function secretUsageMessage(service: string, usedBy: Pick<SecretUsage, "region" | "names">[]): string {
	if (usedBy.length === 0) return "";
	const names = [...new Set(usedBy.flatMap((usage) => usage.names))].toSorted();
	const regions = new Intl.ListFormat("en", { style: "long", type: "conjunction" }).format(
		usedBy.map((usage) => usage.region),
	);
	return `${service} uses ${names.join(", ")} in ${regions}. The new value applies on the next deploy or restart.`;
}

/** Names among saved that no deployment in usedBy reads. */
export function unusedNames(saved: string[], usedBy: Pick<SecretUsage, "names">[]): string[] {
	const used = new Set(usedBy.flatMap((usage) => usage.names));
	return saved.filter((name) => !used.has(name)).toSorted();
}

/** How to deliver saved secrets that a loco.yaml service does not declare. */
export function yamlBindingHint(service: string, names: string[]): string {
	const keys = names.length === 1 ? "this key" : "these keys";
	return `Saved. To use ${keys}, add ${names.join(", ")} to services.${service}.secrets and redeploy.`;
}

/** Refetches the variables list of one resource in one environment. */
export async function refreshSecrets(client: QueryClient, resourceId: string, environmentId: string): Promise<void> {
	await client.invalidateQueries({
		queryKey: createConnectQueryKey({
			schema: listSecrets,
			input: { resourceId, environmentId },
			cardinality: "finite",
		}),
	});
}

/** Refetches the plain variables and secret bindings of one resource in one environment. */
export async function refreshVariables(client: QueryClient, resourceId: string, environmentId: string): Promise<void> {
	await client.invalidateQueries({
		queryKey: createConnectQueryKey({
			schema: getResourceEnv,
			input: { resourceId, environmentId },
			cardinality: "finite",
		}),
	});
}
