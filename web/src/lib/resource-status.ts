import { PlacementStatus } from "@gen/loco/deployment/v1/deployment_pb";
import {
	ResourceStatus,
	type EnvironmentStatus,
	type RegionStatus,
	type Resource,
} from "@gen/loco/resource/v1/resource_pb";

export function environmentStatus(
	resource: Resource,
	environmentId: string | undefined,
): EnvironmentStatus | undefined {
	return resource.environmentStatuses.find((e) => e.environmentId === environmentId);
}

export type Rollout = "updating" | "failed";

/** A change to regions that keep serving: a newer deployment rolling out, or one that stopped while an older one serves. */
export function rolloutState(regions: readonly RegionStatus[]): Rollout | undefined {
	const serving = regions.filter((r) => r.readyReplicas > 0).map((r) => r.placementStatus);
	if (serving.some((s) => s === PlacementStatus.FAILED || s === PlacementStatus.CRASHED)) return "failed";
	if (serving.some((s) => s === PlacementStatus.PENDING || s === PlacementStatus.DEPLOYING)) return "updating";
	return undefined;
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

export function primaryDomain(resource: Resource, environmentId?: string): string | null {
	const domains = resource.domains.filter(
		(domain) => environmentId === undefined || domain.environmentId === environmentId,
	);
	const domain = domains.find((d) => d.isPrimary) ?? domains[0];
	return domain?.domain ?? null;
}
