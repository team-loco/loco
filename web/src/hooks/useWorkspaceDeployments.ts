import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQueries, useQuery } from "@tanstack/react-query";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import { listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";

import { workspaceDeploymentsOptions } from "@/hooks/workspaceDeployments";
import { useWorkspaceResourcesOptions } from "@/hooks/workspaceResources";
import { isInFlight } from "@/lib/deployment-utils";

const POLL_MS = 5000;
const IDLE_POLL_MS = 15_000;
const DEPLOYMENT_PAGE_SIZE = 50;

function rollingOut(resource: Resource): boolean {
	return resource.environmentStatuses.some((e) => e.regions.some((r) => isInFlight(r.deploymentStatus)));
}

function usePollingWorkspaceResources(orgId: string | null, workspaceId: string | null) {
	return useQuery({
		...useWorkspaceResourcesOptions(orgId ?? "", workspaceId ?? ""),
		enabled: orgId !== null && workspaceId !== null,
		refetchInterval: (q) => {
			const list = q.state.data?.resources ?? [];
			return list.some(rollingOut) ? POLL_MS : IDLE_POLL_MS;
		},
	});
}

export function useWorkspaceDeployments(orgId: string | null, workspaceId: string | null) {
	const transport = useTransport();
	const resourcesQuery = usePollingWorkspaceResources(orgId, workspaceId);
	const resources = resourcesQuery.data?.resources ?? [];

	const deploymentQueries = useQueries({
		queries: resources.map((r) => ({
			...createQueryOptions(listDeployments, { resourceId: r.id, pageSize: DEPLOYMENT_PAGE_SIZE }, { transport }),
			refetchInterval: (q: { state: { data?: { deployments: Deployment[] } | undefined } }) => {
				const list = q.state.data?.deployments ?? [];
				return list.some((d) => d.isActive && isInFlight(d.status)) ? POLL_MS : false;
			},
		})),
	});

	return {
		resources,
		deployments: resources.map((_, i) => deploymentQueries[i]?.data?.deployments ?? []),
		isLoading: resourcesQuery.isLoading || deploymentQueries.some((q) => q.isLoading),
		error: resourcesQuery.error,
		refetch: resourcesQuery.refetch,
	};
}

export function useWorkspaceEnvironmentDeployments(
	orgId: string | null,
	workspaceId: string | null,
	environmentId: string,
) {
	const transport = useTransport();
	const resourcesQuery = usePollingWorkspaceResources(orgId, workspaceId);
	const deploymentsQuery = useQuery({
		...workspaceDeploymentsOptions(transport, workspaceId ?? "", environmentId),
		refetchInterval: (q) => {
			const list = q.state.data?.deployments ?? [];
			return list.some((d) => d.isActive && isInFlight(d.status)) ? POLL_MS : IDLE_POLL_MS;
		},
	});
	return {
		resources: resourcesQuery.data?.resources ?? [],
		deployments: deploymentsQuery.data?.deployments ?? [],
		isLoading: resourcesQuery.isLoading || deploymentsQuery.isLoading,
		error: resourcesQuery.error ?? deploymentsQuery.error,
	};
}
