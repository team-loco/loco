import type { Resource } from "@gen/loco/resource/v1/resource_pb";

// A service without a port is a worker: it takes no traffic and has no domains or health check.
export function isWorker(resource: Resource): boolean {
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
