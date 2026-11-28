import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import type { Resource, ResourceEnvironment } from "@gen/loco/resource/v1/resource_pb";
import { getServiceSpec } from "@/lib/deployment-utils";

export function resourceConfiguration(resource: Resource, environmentId: string): ResourceEnvironment | undefined {
	return resource.environments.find((config) => config.environmentId === environmentId);
}

// A mixed-region rollout still receives traffic when any current deployment has a port.
export function isWorker(resource: Resource, deployments: Deployment[], environmentId: string): boolean {
	if (deployments.length > 0) return deployments.every((d) => getServiceSpec(d)?.port === undefined);
	const spec = resourceConfiguration(resource, environmentId)?.spec?.spec;
	return spec?.case === "service" && spec.value.routing?.port === undefined;
}

export function configuredRegions(resource: Resource, environmentId?: string): string[] {
	const configs = resource.environments.filter(
		(config) => environmentId === undefined || config.environmentId === environmentId,
	);
	const names = configs.flatMap((config) => {
		const spec = config.spec?.spec;
		if (spec?.case !== "service") return [];
		return Object.entries(spec.value.regions)
			.filter(([, target]) => target.enabled)
			.map(([name]) => name);
	});
	return [...new Set(names)].toSorted();
}
