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

/** The secrets an environment deletion lists by name, and how many more it deletes. */
export function shownSecrets(secretNames: readonly string[]): { shown: string[]; more: number } {
	return { shown: secretNames.slice(0, SHOWN_SECRETS), more: Math.max(0, secretNames.length - SHOWN_SECRETS) };
}
