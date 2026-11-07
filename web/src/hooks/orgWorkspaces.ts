import { queryOptions } from "@tanstack/react-query";
import { create } from "@bufbuild/protobuf";
import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey } from "@connectrpc/connect-query";
import { listOrgWorkspaces } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import { ListOrgWorkspacesResponseSchema, WorkspaceService } from "@gen/loco/workspace/v1/workspace_pb";
import { collectPages } from "@/lib/collectPages";

const WORKSPACE_PAGE_SIZE = 200;

export function orgWorkspacesOptions(transport: Transport, orgId: string) {
	const input = { orgId, pageSize: WORKSPACE_PAGE_SIZE };
	const key = createConnectQueryKey({ schema: listOrgWorkspaces, input, transport, cardinality: "finite" });
	return queryOptions({
		queryKey: [...key, "all-pages"] as const,
		queryFn: async ({ signal }: { signal: AbortSignal }) => {
			const client = createClient(WorkspaceService, transport);
			const workspaces = await collectPages(async (pageToken) => {
				const page = await client.listOrgWorkspaces({ ...input, pageToken }, { signal });
				return { items: page.workspaces, nextPageToken: page.nextPageToken };
			});
			return create(ListOrgWorkspacesResponseSchema, { workspaces });
		},
		enabled: orgId !== "",
	});
}
