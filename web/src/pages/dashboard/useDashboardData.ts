import { createQueryOptions, useQuery, useTransport } from "@connectrpc/connect-query";
import { useQueries } from "@tanstack/react-query";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";
import {
	ResourceStatus,
	type EnvironmentStatus,
	type RegionInfo,
	type Resource,
} from "@gen/loco/resource/v1/resource_pb";
import { listRegions, listWorkspaceResources } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";

import { environmentStatus } from "@/lib/resource-status";
import { tsMs } from "@/lib/time";

import { isInFlight } from "./format";

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

const POLL_MS = 5000;
const IDLE_POLL_MS = 15_000;

function regionReplicas(resource: Resource, environment: EnvironmentStatus | undefined): RegionReplicas[] {
	const names = new Set(resource.regions.map((r) => r.region));
	for (const r of environment?.regions ?? []) names.add(r.region);
	return [...names].map((region) => {
		const status = environment?.regions.find((r) => r.region === region);
		return { region, desired: status?.desiredReplicas ?? 0, ready: status?.readyReplicas ?? 0 };
	});
}

function rollingOut(resource: Resource): boolean {
	return resource.environmentStatuses.some((e) => e.regions.some((r) => isInFlight(r.deploymentStatus)));
}

export function useDashboardData(workspaceId: string | null, env: Environment | undefined) {
	const transport = useTransport();
	const resourcesQuery = useQuery(
		listWorkspaceResources,
		{ workspaceId: workspaceId ?? "", pageSize: 200 },
		{
			enabled: workspaceId !== null,
			refetchInterval: (q) => {
				const list = q.state.data?.resources ?? [];
				return list.some(rollingOut) ? POLL_MS : IDLE_POLL_MS;
			},
		},
	);
	const regionsQuery = useQuery(listRegions, {});
	const resources = resourcesQuery.data?.resources ?? [];

	const deploymentQueries = useQueries({
		queries: resources.map((r) => ({
			...createQueryOptions(listDeployments, { resourceId: r.id, pageSize: 50 }, { transport }),
			refetchInterval: (q: { state: { data?: { deployments: Deployment[] } | undefined } }) => {
				const list = q.state.data?.deployments ?? [];
				return list.some((d) => d.isActive && isInFlight(d.status)) ? POLL_MS : false;
			},
		})),
	});

	const deploymentsLoading = deploymentQueries.some((q) => q.isLoading);

	const envId = env?.id ?? "";
	const envResources: EnvResource[] = [];
	const envDeployments: { deployment: Deployment; resource: Resource }[] = [];
	const usedEnvIds = new Set<string>();

	resources.forEach((resource, i) => {
		const all = deploymentQueries[i]?.data?.deployments ?? [];
		const mine = all
			.filter((d) => d.environmentId === envId)
			.sort((a, b) => tsMs(b.createdAt) - tsMs(a.createdAt));
		for (const d of all) usedEnvIds.add(d.environmentId);
		for (const d of mine) envDeployments.push({ deployment: d, resource });
		const neverDeployed = all.length === 0;
		if (mine.length === 0 && !neverDeployed) return;
		const environment = environmentStatus(resource, envId);
		const regions = regionReplicas(resource, environment);
		envResources.push({
			resource,
			environment,
			status: environment?.status ?? ResourceStatus.UNSPECIFIED,
			envDeployments: mine,
			last: mine[0],
			regions,
			totalReplicas: regions.reduce((n, g) => n + g.desired, 0),
			neverDeployed,
		});
	});

	envDeployments.sort((a, b) => tsMs(b.deployment.createdAt) - tsMs(a.deployment.createdAt));

	const regionInfos: RegionInfo[] = regionsQuery.data?.regions ?? [];

	return {
		resources,
		envResources,
		envDeployments,
		usedEnvIds,
		regions: regionInfos,
		isLoading: resourcesQuery.isLoading || deploymentsLoading,
		error: resourcesQuery.error,
		refetchResources: resourcesQuery.refetch,
	};
}
