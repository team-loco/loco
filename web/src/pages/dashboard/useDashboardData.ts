import { useQuery } from "@connectrpc/connect-query";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
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
	/** The resource's loaded deployments in the environment, newest first. */
	deployments: Deployment[];
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

/** The resource in the environment, from deployments of the environment that include its own. */
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
		deployments: mine,
		last: mine[0],
		regions,
		totalReplicas: regions.reduce((n, g) => n + g.desired, 0),
		neverDeployed: mine.length === 0 && resource.environmentStatuses.length === 0,
	};
}

/** The environment's online regions, for a caller who reads the whole workspace. */
export function useEnvironmentRegions(
	orgId: string | null,
	workspaceId: string | null,
	environmentId: string | undefined,
) {
	const grants = useMyScopes().resourceGrants(orgId ?? "", workspaceId ?? "");
	const regionsQuery = useQuery(
		listRegions,
		{ environmentId: environmentId ?? "" },
		{ enabled: environmentId !== undefined && grants === "workspace" },
	);
	const regions: RegionInfo[] = (regionsQuery.data?.regions ?? []).filter(
		(region) => region.availability !== RegionAvailability.OFFLINE,
	);
	return { grants, regions };
}

/** Every resource of the environment with every deployment of it, for the maps. */
export function useDashboardData(orgId: string, workspaceId: string, environmentId: string) {
	const { resources, deployments, isLoading, error, refetch } = useWorkspaceEnvironmentDeployments(
		orgId,
		workspaceId,
		environmentId,
	);
	const { regions } = useEnvironmentRegions(orgId, workspaceId, environmentId);
	const envResources = resources
		.map((resource) => envResource(resource, deployments, environmentId))
		.filter((item) => item.deployments.length > 0 || item.neverDeployed);
	return { envResources, regions, isLoading, error, refetch };
}
