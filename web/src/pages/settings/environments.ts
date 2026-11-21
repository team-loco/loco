import type { Resource } from "@gen/loco/resource/v1/resource_pb";

export function resourcesPerEnvironment(resources: readonly Resource[]): Map<string, number> {
	const counts = new Map<string, number>();
	for (const resource of resources) {
		const envIds = new Set(resource.environmentStatuses.map((s) => s.environmentId));
		for (const id of envIds) counts.set(id, (counts.get(id) ?? 0) + 1);
	}
	return counts;
}

const SHOWN_SECRETS = 5;

/** Names the secrets that deleting an environment also deletes; null while they are unknown. */
export function deleteDescription(workspaceName: string, secretNames: string[] | null): string {
	const base = `This removes the environment from ${workspaceName}. Loco refuses while it still has deployments.`;
	if (secretNames === null) return `${base} It also permanently deletes its secrets.`;
	if (secretNames.length === 0) return base;
	const shown = secretNames.slice(0, SHOWN_SECRETS).join(", ");
	const more = secretNames.length - SHOWN_SECRETS;
	const noun = secretNames.length === 1 ? "secret" : "secrets";
	return `${base} It also permanently deletes its ${secretNames.length.toString()} ${noun}: ${shown}${more > 0 ? ` and ${more.toString()} more` : ""}.`;
}
