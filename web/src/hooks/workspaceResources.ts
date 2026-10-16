import { queryOptions } from "@tanstack/react-query";
import { create } from "@bufbuild/protobuf";
import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey } from "@connectrpc/connect-query";
import { listWorkspaceResources } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { ListWorkspaceResourcesResponseSchema, ResourceService, type Resource } from "@gen/loco/resource/v1/resource_pb";

const RESOURCE_PAGE_SIZE = 200;
const incompleteResources = new Error("Could not load the complete resource list");

export function workspaceResourcesOptions(transport: Transport, workspaceId: string) {
	const input = { workspaceId, pageSize: RESOURCE_PAGE_SIZE };
	const key = createConnectQueryKey({ schema: listWorkspaceResources, input, transport, cardinality: "finite" });
	return queryOptions({
		queryKey: [...key, "all-pages"] as const,
		queryFn: async ({ signal }: { signal: AbortSignal }) => {
			const client = createClient(ResourceService, transport);
			const resources: Resource[] = [];
			const seen = new Set<string>();
			let pageToken = "";
			do {
				const page = await client.listWorkspaceResources({ ...input, pageToken }, { signal });
				resources.push(...page.resources);
				pageToken = page.nextPageToken;
				if (pageToken !== "" && seen.has(pageToken)) throw incompleteResources;
				seen.add(pageToken);
			} while (pageToken !== "");
			return create(ListWorkspaceResourcesResponseSchema, { resources });
		},
		enabled: workspaceId !== "",
	});
}
