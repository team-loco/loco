import { getServiceSpec } from "@/lib/deployment-utils";
import { parseCpuMilli, parseMemMi } from "@/lib/quantity";

import type { EnvResource } from "./useDashboardData";

export interface ResourceAllocation {
	id: string;
	name: string;
	cpuMilli: number;
	memMi: number;
}

export interface Allocation {
	cpuMilli: number;
	memMi: number;
	resources: ResourceAllocation[];
}

function resourceAllocation(
	item: Pick<EnvResource, "resource" | "environment" | "envDeployments">,
): ResourceAllocation {
	let cpuMilli = 0;
	let memMi = 0;
	for (const region of item.environment?.regions ?? []) {
		const deployment = item.envDeployments.find((d) => d.id === region.deploymentId);
		const spec = deployment === undefined ? undefined : getServiceSpec(deployment);
		cpuMilli += parseCpuMilli(spec?.cpu ?? "") * region.desiredReplicas;
		memMi += parseMemMi(spec?.memory ?? "") * region.desiredReplicas;
	}
	return { id: item.resource.id, name: item.resource.name, cpuMilli, memMi };
}

export function allocation(items: Pick<EnvResource, "resource" | "environment" | "envDeployments">[]): Allocation {
	const resources = items
		.map(resourceAllocation)
		.filter((r) => r.cpuMilli > 0 || r.memMi > 0)
		.toSorted((a, b) => b.cpuMilli - a.cpuMilli || b.memMi - a.memMi || a.name.localeCompare(b.name));
	return {
		cpuMilli: resources.reduce((n, r) => n + r.cpuMilli, 0),
		memMi: resources.reduce((n, r) => n + r.memMi, 0),
		resources,
	};
}
