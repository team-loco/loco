import type { Resource } from "@gen/loco/resource/v1/resource_pb";

export function configuredRegions(resource: Resource): string[] {
	const spec = resource.spec?.spec;
	if (spec?.case !== "service") return [];
	return Object.entries(spec.value.regions)
		.filter(([, target]) => target.enabled)
		.map(([name]) => name)
		.toSorted();
}
