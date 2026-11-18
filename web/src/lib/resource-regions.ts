import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { getServiceSpec } from "@/lib/deployment-utils";

import type { Resource } from "@gen/loco/resource/v1/resource_pb";

// A mixed-region rollout still receives traffic when any current deployment has a port.
export function isWorker(resource: Resource, deployments: Deployment[]): boolean {
	if (deployments.length > 0) return deployments.every((d) => getServiceSpec(d)?.port === undefined);
	const spec = resource.spec?.spec;
	return spec?.case === "service" && spec.value.routing?.port === undefined;
}

export function configuredRegions(resource: Resource): string[] {
	const spec = resource.spec?.spec;
	if (spec?.case !== "service") return [];
	return Object.entries(spec.value.regions)
		.filter(([, target]) => target.enabled)
		.map(([name]) => name)
		.toSorted();
}
