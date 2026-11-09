import { ResourceStatus, type Resource } from "@gen/loco/resource/v1/resource_pb";

import { environmentStatus } from "@/lib/resource-status";
import { maybeTsMs } from "@/lib/time";

export interface Alert {
	key: string;
	resourceId: string;
	name: string;
	description: string;
	region: string;
	status: ResourceStatus;
	message: string;
	reportedMs: number | undefined;
}

function needsAttention(status: ResourceStatus): boolean {
	switch (status) {
		case ResourceStatus.DEGRADED:
			return true;
		case ResourceStatus.UNAVAILABLE:
			return true;
		case ResourceStatus.DEPLOYING:
			return true;
		case ResourceStatus.UNKNOWN:
			return true;
		case ResourceStatus.HEALTHY:
			return false;
		case ResourceStatus.UNSPECIFIED:
			return false;
	}
}

export function buildAlerts(resources: Resource[], environmentId: string): Alert[] {
	return resources.flatMap((resource) =>
		(environmentStatus(resource, environmentId)?.regions ?? [])
			.filter((r) => needsAttention(r.status))
			.map((r) => ({
				key: `${resource.id}/${r.region}`,
				resourceId: resource.id,
				name: resource.name,
				description: resource.description ?? "",
				region: r.region,
				status: r.status,
				message: r.message ?? "",
				reportedMs: maybeTsMs(r.reportedAt),
			})),
	);
}

export function alertedResourceCount(alerts: Alert[]): number {
	return new Set(alerts.map((a) => a.resourceId)).size;
}
