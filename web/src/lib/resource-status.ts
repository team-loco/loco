import { ResourceStatus, type EnvironmentStatus, type Resource } from "@gen/loco/resource/v1/resource_pb";

export function environmentStatus(
	resource: Resource,
	environmentId: string | undefined,
): EnvironmentStatus | undefined {
	return resource.environmentStatuses.find((e) => e.environmentId === environmentId);
}

export function statusRank(status: ResourceStatus): number {
	switch (status) {
		case ResourceStatus.DEGRADED:
			return 0;
		case ResourceStatus.UNAVAILABLE:
			return 0;
		case ResourceStatus.UNKNOWN:
			return 1;
		case ResourceStatus.DEPLOYING:
			return 1;
		case ResourceStatus.UNSPECIFIED:
			return 2;
		case ResourceStatus.HEALTHY:
			return 3;
	}
}

export function primaryDomain(resource: Resource): string | null {
	const domain = resource.domains.find((d) => d.isPrimary) ?? resource.domains[0];
	return domain?.domain ?? null;
}
