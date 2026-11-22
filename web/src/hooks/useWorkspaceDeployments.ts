import type { Transport } from "@connectrpc/connect";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import { useQueries, useQuery } from "@tanstack/react-query";
import type { GetDeploymentResponse } from "@gen/loco/deployment/v1/deployment_pb";
import { getDeployment } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";

import { workspaceDeploymentsOptions } from "@/hooks/workspaceDeployments";
import { useWorkspaceResourcesOptions } from "@/hooks/workspaceResources";
import { isInFlight, isLive, placementPhase } from "@/lib/deployment-utils";

const POLL_MS = 5000;
const IDLE_POLL_MS = 15_000;

function rollingOut(resource: Resource): boolean {
	return resource.environmentStatuses.some((e) => e.regions.some((r) => isInFlight(placementPhase(r.placementStatus))));
}

/** How often a list of resources refreshes: faster while one of them rolls out. */
export function resourcesPollMs(resources: readonly Resource[] | undefined): number {
	return (resources ?? []).some(rollingOut) ? POLL_MS : IDLE_POLL_MS;
}

/** The workspace's resources the caller can read, polled faster while one rolls out. */
export function usePollingWorkspaceResources(orgId: string | null, workspaceId: string | null, enabled = true) {
	return useQuery({
		...useWorkspaceResourcesOptions(orgId ?? "", workspaceId ?? ""),
		enabled: enabled && orgId !== null && workspaceId !== null,
		refetchInterval: (q) => resourcesPollMs(q.state.data?.resources),
	});
}

type DeploymentQuery = { state: { data?: GetDeploymentResponse | undefined } };

/** A current deployment, polled while it rolls out; once settled its image, sizing and ready time never change. */
export function currentDeploymentOptions(transport: Transport, deploymentId: string) {
	const settled = (q: DeploymentQuery) => {
		const d = q.state.data?.deployment;
		return d !== undefined && !isInFlight(d.status);
	};
	return {
		...createQueryOptions(getDeployment, { deploymentId }, { transport }),
		staleTime: (q: DeploymentQuery) => (settled(q) ? Number.POSITIVE_INFINITY : 0),
		refetchInterval: (q: DeploymentQuery) => (q.state.data === undefined || settled(q) ? false : POLL_MS),
	};
}

function currentDeploymentIds(resource: Resource, environmentId: string | undefined): string[] {
	return [
		...new Set(
			resource.environmentStatuses
				.filter((e) => environmentId === undefined || e.environmentId === environmentId)
				.flatMap((e) => e.regions.map((r) => r.deploymentId))
				.filter((id) => id !== ""),
		),
	];
}

/** The current deployments of each of resources, in resources' order, in environmentId or every environment. */
export function useCurrentDeployments(resources: readonly Resource[], environmentId?: string) {
	const transport = useTransport();
	const ids = [...new Set(resources.flatMap((r) => currentDeploymentIds(r, environmentId)))];
	const deploymentQueries = useQueries({ queries: ids.map((id) => currentDeploymentOptions(transport, id)) });
	const byId = new Map(
		deploymentQueries.flatMap((q) =>
			q.data?.deployment === undefined ? [] : ([[q.data.deployment.id, q.data.deployment]] as const),
		),
	);
	return resources.map((r) => currentDeploymentIds(r, environmentId).flatMap((id) => byId.get(id) ?? []));
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
			return list.some((d) => isLive(d) && isInFlight(d.status)) ? POLL_MS : IDLE_POLL_MS;
		},
	});
	return {
		resources: resourcesQuery.data?.resources ?? [],
		deployments: deploymentsQuery.data?.deployments ?? [],
		isLoading: resourcesQuery.isLoading || deploymentsQuery.isLoading,
		error: resourcesQuery.error ?? deploymentsQuery.error,
		refetch: async () => await Promise.all([resourcesQuery.refetch(), deploymentsQuery.refetch()]),
	};
}
