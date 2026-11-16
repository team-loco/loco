import { createQueryOptions, useTransport } from "@connectrpc/connect-query";
import type { Environment } from "@gen/loco/environment/v1/environment_pb";
import { listEnvironments } from "@gen/loco/environment/v1/environment-EnvironmentService_connectquery";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";
import { useQueries } from "@tanstack/react-query";

import { workspaceResourcesOptions } from "@/hooks/workspaceResources";

/** The resources and environments of workspaces the caller administers, to name them in events and filters. */
export function useWorkspaceDetails(workspaceIds: readonly string[]) {
	const transport = useTransport();
	const resources = useQueries({
		queries: workspaceIds.map((id) => workspaceResourcesOptions(transport, id, "workspace")),
	});
	const environments = useQueries({
		queries: workspaceIds.map((id) => createQueryOptions(listEnvironments, { workspaceId: id }, { transport })),
	});
	const resourcesOf = new Map<string, Resource[]>(
		workspaceIds.map((id, i) => [id, resources[i]?.data?.resources ?? []]),
	);
	const environmentsOf = new Map<string, Environment[]>(
		workspaceIds.map((id, i) => [id, environments[i]?.data?.environments ?? []]),
	);
	const names = new Map<string, string>([
		...[...resourcesOf.values()].flat().map((r): [string, string] => [r.id, r.name]),
		...[...environmentsOf.values()].flat().map((e): [string, string] => [e.id, e.name]),
	]);
	const resourcesReady = new Set(workspaceIds.filter((_, i) => resources[i]?.data !== undefined));
	const environmentsReady = new Set(workspaceIds.filter((_, i) => environments[i]?.data !== undefined));
	const isLoading = [...resources, ...environments].some((q) => q.isPending);
	return { resourcesOf, environmentsOf, names, resourcesReady, environmentsReady, isLoading };
}
