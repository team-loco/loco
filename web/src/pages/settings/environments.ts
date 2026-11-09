import type { Resource } from "@gen/loco/resource/v1/resource_pb";

export function resourcesPerEnvironment(resources: readonly Resource[]): Map<string, number> {
	const counts = new Map<string, number>();
	for (const resource of resources) {
		const envIds = new Set(resource.environmentStatuses.map((s) => s.environmentId));
		for (const id of envIds) counts.set(id, (counts.get(id) ?? 0) + 1);
	}
	return counts;
}
