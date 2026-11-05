import { create } from "@bufbuild/protobuf";
import { queryOptions } from "@tanstack/react-query";
import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey } from "@connectrpc/connect-query";
import { listInvitations, listOrgMembers } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { ListInvitationsResponseSchema, ListOrgMembersResponseSchema, OrgService } from "@gen/loco/org/v1/org_pb";
import { listWorkspaceMembers } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import { ListWorkspaceMembersResponseSchema, WorkspaceService } from "@gen/loco/workspace/v1/workspace_pb";
import { collectPages } from "@/lib/collectPages";

const PAGE_SIZE = 200;

export function orgMembersOptions(transport: Transport, orgId: string) {
	const input = { orgId, pageSize: PAGE_SIZE };
	const key = createConnectQueryKey({ schema: listOrgMembers, input, transport, cardinality: "finite" });
	return queryOptions({
		queryKey: [...key, "all-pages"] as const,
		queryFn: async ({ signal }: { signal: AbortSignal }) => {
			const client = createClient(OrgService, transport);
			const members = await collectPages(async (pageToken) => {
				const page = await client.listOrgMembers({ ...input, pageToken }, { signal });
				return { items: page.members, nextPageToken: page.nextPageToken };
			});
			return create(ListOrgMembersResponseSchema, { members });
		},
		enabled: orgId !== "",
	});
}

export function invitationsOptions(transport: Transport, orgId: string) {
	const input = { orgId, pageSize: PAGE_SIZE };
	const key = createConnectQueryKey({ schema: listInvitations, input, transport, cardinality: "finite" });
	return queryOptions({
		queryKey: [...key, "all-pages"] as const,
		queryFn: async ({ signal }: { signal: AbortSignal }) => {
			const client = createClient(OrgService, transport);
			const invitations = await collectPages(async (pageToken) => {
				const page = await client.listInvitations({ ...input, pageToken }, { signal });
				return { items: page.invitations, nextPageToken: page.nextPageToken };
			});
			return create(ListInvitationsResponseSchema, { invitations });
		},
		enabled: orgId !== "",
	});
}

export function workspaceMembersOptions(transport: Transport, workspaceId: string) {
	const input = { workspaceId, pageSize: PAGE_SIZE };
	const key = createConnectQueryKey({ schema: listWorkspaceMembers, input, transport, cardinality: "finite" });
	return queryOptions({
		queryKey: [...key, "all-pages"] as const,
		queryFn: async ({ signal }: { signal: AbortSignal }) => {
			const client = createClient(WorkspaceService, transport);
			const members = await collectPages(async (pageToken) => {
				const page = await client.listWorkspaceMembers({ ...input, pageToken }, { signal });
				return { items: page.members, nextPageToken: page.nextPageToken };
			});
			return create(ListWorkspaceMembersResponseSchema, { members });
		},
		enabled: workspaceId !== "",
	});
}
