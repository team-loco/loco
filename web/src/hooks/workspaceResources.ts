import { queryOptions, skipToken } from "@tanstack/react-query";
import { create } from "@bufbuild/protobuf";
import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { listWorkspaceResources } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { ListWorkspaceResourcesResponseSchema, ResourceService } from "@gen/loco/resource/v1/resource_pb";
import { useMyScopes, type ResourceGrants } from "@/hooks/useMyScopes";
import { collectPages } from "@/lib/collectPages";

const RESOURCE_PAGE_SIZE = 200;

/** workspaceResourcesOptions with the signed-in user's grants on the workspace. */
export function useWorkspaceResourcesOptions(orgId: string, workspaceId: string) {
	const transport = useTransport();
	const { resourceGrants } = useMyScopes();
	return workspaceResourcesOptions(transport, workspaceId, resourceGrants(orgId, workspaceId));
}

/** Prefix of every list of this workspace's resources, for invalidating them after a change. */
export function workspaceResourcesKey(transport: Transport, workspaceId: string) {
	const input = { workspaceId, pageSize: RESOURCE_PAGE_SIZE };
	const key = createConnectQueryKey({ schema: listWorkspaceResources, input, transport, cardinality: "finite" });
	return [...key, "all-pages"] as const;
}

/**
 * Lists the workspace's resources the caller can read. A caller who cannot read the workspace
 * gets the resources its grants name, one GetResource each, since ListWorkspaceResources
 * refuses it. The query waits while grants is undefined.
 */
export function workspaceResourcesOptions(
	transport: Transport,
	workspaceId: string,
	grants: ResourceGrants | undefined,
) {
	const client = createClient(ResourceService, transport);
	const listAll = async ({ signal }: { signal: AbortSignal }) => {
		const resources = await collectPages(async (pageToken) => {
			const page = await client.listWorkspaceResources(
				{ workspaceId, pageSize: RESOURCE_PAGE_SIZE, pageToken },
				{ signal },
			);
			return { items: page.resources, nextPageToken: page.nextPageToken };
		});
		return create(ListWorkspaceResourcesResponseSchema, { resources });
	};
	const listGranted =
		(ids: readonly string[]) =>
		async ({ signal }: { signal: AbortSignal }) => {
			const responses = await Promise.all(
				ids.map(async (id) => await client.getResource({ key: { case: "resourceId", value: id } }, { signal })),
			);
			const resources = responses.flatMap((r) => (r.resource?.workspaceId === workspaceId ? [r.resource] : []));
			return create(ListWorkspaceResourcesResponseSchema, { resources });
		};
	const prefix = workspaceResourcesKey(transport, workspaceId);
	return queryOptions({
		queryKey: grants === undefined || grants === "workspace" ? prefix : [...prefix, ...grants],
		queryFn: grants === undefined ? skipToken : grants === "workspace" ? listAll : listGranted(grants),
		enabled: workspaceId !== "",
	});
}
