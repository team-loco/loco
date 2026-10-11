import type { EnvironmentStatus, Resource } from "@gen/loco/resource/v1/resource_pb";

export function environmentStatus(resource: Resource, environmentId: string | undefined): EnvironmentStatus | undefined {
	return resource.environmentStatuses.find((e) => e.environmentId === environmentId);
}
