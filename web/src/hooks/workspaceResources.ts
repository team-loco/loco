import { queryOptions } from "@tanstack/react-query";
import { create } from "@bufbuild/protobuf";
import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey } from "@connectrpc/connect-query";
import { listWorkspaceResources } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { ListWorkspaceResourcesResponseSchema, ResourceService } from "@gen/loco/resource/v1/resource_pb";
import { collectPages } from "@/lib/collectPages";

const RESOURCE_PAGE_SIZE = 200;

export function workspaceResourcesOptions(transport: Transport, workspaceId: string) {
	const input = { workspaceId, pageSize: RESOURCE_PAGE_SIZE };
	const key = createConnectQueryKey({ schema: listWorkspaceResources, input, transport, cardinality: "finite" });
	return queryOptions({
		queryKey: [...key, "all-pages"] as const,
		queryFn: async ({ signal }: { signal: AbortSignal }) => {
			const client = createClient(ResourceService, transport);
			const resources = await collectPages(async (pageToken) => {
				const page = await client.listWorkspaceResources({ ...input, pageToken }, { signal });
				return { items: page.resources, nextPageToken: page.nextPageToken };
			});
			return create(ListWorkspaceResourcesResponseSchema, { resources });
		},
		enabled: workspaceId !== "",
	});
}
