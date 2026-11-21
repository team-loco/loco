import { useQuery } from "@connectrpc/connect-query";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";
import {
	RegionAvailability,
	ResourceStatus,
	type EnvironmentStatus,
	type RegionInfo,
	type Resource,
} from "@gen/loco/resource/v1/resource_pb";
import { listRegions } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";

import { useMyScopes } from "@/hooks/useMyScopes";
import { useWorkspaceEnvironmentDeployments } from "@/hooks/useWorkspaceDeployments";
import { configuredRegions } from "@/lib/resource-regions";
import { environmentStatus } from "@/lib/resource-status";
import { tsMs } from "@/lib/time";

export interface RegionReplicas {
	region: string;
	desired: number;
	ready: number;
}

export interface EnvResource {
	resource: Resource;
	environment: EnvironmentStatus | undefined;
	status: ResourceStatus;
	envDeployments: Deployment[];
	last: Deployment | undefined;
	regions: RegionReplicas[];
	totalReplicas: number;
	neverDeployed: boolean;
}

function regionReplicas(resource: Resource, environment: EnvironmentStatus | undefined): RegionReplicas[] {
	const names = new Set(configuredRegions(resource));
	for (const r of environment?.regions ?? []) names.add(r.region);
	return [...names].map((region) => {
		const status = environment?.regions.find((r) => r.region === region);
		return { region, desired: status?.desiredReplicas ?? 0, ready: status?.readyReplicas ?? 0 };
	});
}

/** The resource in the environment, from the environment's deployments. */
export function envResource(
	resource: Resource,
	deployments: readonly Deployment[],
	environmentId: string,
): EnvResource {
	const mine = deployments
		.filter((d) => d.resourceId === resource.id)
		.toSorted((a, b) => tsMs(b.createdAt) - tsMs(a.createdAt));
	const environment = environmentStatus(resource, environmentId);
	const regions = regionReplicas(resource, environment);
	return {
		resource,
		environment,
		status: environment?.status ?? ResourceStatus.UNSPECIFIED,
		envDeployments: mine,
		last: mine[0],
		regions,
		totalReplicas: regions.reduce((n, g) => n + g.desired, 0),
		neverDeployed: mine.length === 0 && resource.environmentStatuses.length === 0,
	};
}

export function useDashboardData(orgId: string | null, workspaceId: string | null, env: Environment | undefined) {
	const { resources, deployments, isLoading, error, refetch } = useWorkspaceEnvironmentDeployments(
		orgId,
		workspaceId,
		env?.id ?? "",
	);
	const grants = useMyScopes().resourceGrants(orgId ?? "", workspaceId ?? "");
	const regionsQuery = useQuery(
		listRegions,
		{ environmentId: env?.id ?? "" },
		{ enabled: env !== undefined && grants === "workspace" },
	);

	const envId = env?.id ?? "";
	const envResources: EnvResource[] = [];
	const envDeployments: { deployment: Deployment; resource: Resource }[] = [];

	resources.forEach((resource) => {
		const item = envResource(resource, deployments, envId);
		for (const d of item.envDeployments) envDeployments.push({ deployment: d, resource });
		if (item.envDeployments.length > 0 || item.neverDeployed) envResources.push(item);
	});

	envDeployments.sort((a, b) => tsMs(b.deployment.createdAt) - tsMs(a.deployment.createdAt));

	const regionInfos: RegionInfo[] = (regionsQuery.data?.regions ?? []).filter(
		(region) => region.availability !== RegionAvailability.OFFLINE,
	);

	return {
		resources,
		scoped: grants !== undefined && grants !== "workspace",
		envResources,
		envDeployments,
		deployments,
		regions: regionInfos,
		// Until grants resolve a scoped viewer is indistinguishable from a workspace reader, and the
		// workspace view queries environment-wide metrics the proxy refuses them.
		isLoading: isLoading || grants === undefined,
		error,
		refetch,
	};
}
