import { createConnectQueryKey, useQuery, useTransport } from "@connectrpc/connect-query";
import { getOrg, listInvitations } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { listWorkspaceMembers } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import { useQueries, useQuery as useTanstackQuery, useQueryClient } from "@tanstack/react-query";

import { invitationsOptions, orgMembersOptions, workspaceMembersOptions } from "@/hooks/completeMembers";
import { orgWorkspacesOptions } from "@/hooks/orgWorkspaces";

import { parseScopeName, type Member, type ScopeName, type WorkspaceAccess } from "./model";

export function useTeamData(orgId: string) {
	const transport = useTransport();
	const queryClient = useQueryClient();
	const orgQuery = useQuery(getOrg, { orgId }, { enabled: orgId !== "" });
	const membersQuery = useTanstackQuery(orgMembersOptions(transport, orgId));
	const invitationsQuery = useTanstackQuery(invitationsOptions(transport, orgId));
	const wsQuery = useTanstackQuery(orgWorkspacesOptions(transport, orgId));
	const workspaces = wsQuery.data?.workspaces ?? [];
	const memberQueries = useQueries({
		queries: workspaces.map((w) => workspaceMembersOptions(transport, w.id)),
	});

	const access = new Map<string, WorkspaceAccess[]>();
	workspaces.forEach((w, i) => {
		for (const m of memberQueries[i]?.data?.members ?? []) {
			const scopes = m.scopes.map(parseScopeName).filter((s): s is ScopeName => s !== null);
			if (scopes.length === 0) continue;
			const list = access.get(m.userId) ?? [];
			list.push({ id: w.id, name: w.name, scopes });
			access.set(m.userId, list);
		}
	});

	const members: Member[] = (membersQuery.data?.members ?? []).map((member) => ({
		member,
		workspaces: access.get(member.user?.id ?? "") ?? [],
	}));

	return {
		org: orgQuery.data?.organization,
		members,
		invitations: invitationsQuery.data?.invitations ?? [],
		workspaces,
		isLoading: orgQuery.isLoading || membersQuery.isLoading || invitationsQuery.isLoading,
		accessLoading: wsQuery.isLoading || memberQueries.some((q) => q.isLoading),
		error: orgQuery.error ?? membersQuery.error,
		invalidateInvitations: async () => {
			const queryKey = createConnectQueryKey({ schema: listInvitations, cardinality: "finite" });
			await queryClient.invalidateQueries({ queryKey });
		},
		invalidateAccess: async () => {
			const queryKey = createConnectQueryKey({ schema: listWorkspaceMembers, cardinality: "finite" });
			await queryClient.invalidateQueries({ queryKey });
		},
	};
}
