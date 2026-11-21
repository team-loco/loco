import type { Transport } from "@connectrpc/connect";
import { callUnaryMethod, createConnectQueryKey, createQueryOptions } from "@connectrpc/connect-query";
import type { QueryClient } from "@tanstack/react-query";
import { listUserOrgs } from "@gen/loco/org/v1/org-OrgService_connectquery";
import type { Organization } from "@gen/loco/org/v1/org_pb";
import { getScopes } from "@gen/loco/token/v1/token-TokenService_connectquery";
import { whoAmI } from "@gen/loco/user/v1/user-UserService_connectquery";
import { getWorkspace, listOrgWorkspaces } from "@gen/loco/workspace/v1/workspace-WorkspaceService_connectquery";
import type { Workspace } from "@gen/loco/workspace/v1/workspace_pb";

import { ORG_STORAGE_KEY, pickActive, WORKSPACE_STORAGE_KEY } from "@/context/ContextProvider";
import { hasOrganizationMembership } from "@/hooks/useMyScopes";
import { pageImporters } from "@/lib/lazy-pages";
import { workspacePath } from "@/lib/routes";
import { readStorage } from "@/lib/storage";

export async function landingPath(
	transport: Transport,
	queryClient: QueryClient,
	next: string | null,
): Promise<string> {
	const { user } = await queryClient.query({
		...createQueryOptions(whoAmI, {}, { transport }),
		meta: { persist: false },
	});
	if (user === undefined) throw new Error("Your account could not be loaded");

	const orgs = await callUnaryMethod(transport, listUserOrgs, { userId: user.id, createIfMissing: true });
	queryClient.setQueryData(
		createConnectQueryKey({ schema: listUserOrgs, input: { userId: user.id }, transport, cardinality: "finite" }),
		orgs,
	);
	if (next !== null) return next;
	return await dashboardPath(transport, queryClient, orgs.orgs).catch(() => "/dashboard");
}

async function dashboardPath(
	transport: Transport,
	queryClient: QueryClient,
	orgs: readonly Organization[],
): Promise<string> {
	const orgId = pickActive(orgs, undefined, ORG_STORAGE_KEY);
	if (orgId === null) {
		await pageImporters.Organizations();
		return "/organizations";
	}
	const { scopes } = await queryClient.query(createQueryOptions(getScopes, {}, { transport }));
	const workspaces = hasOrganizationMembership(scopes, orgId)
		? await pickableWorkspaces(transport, queryClient, orgId)
		: [];
	const workspaceId = pickActive(workspaces, undefined, WORKSPACE_STORAGE_KEY);
	if (workspaceId === null) {
		await pageImporters.Team();
		return `/org/${orgId}/team`;
	}
	await pageImporters.Dashboard();
	return workspacePath(orgId, workspaceId);
}

// The organization's newest workspace, and the remembered one when it belongs to the organization.
async function pickableWorkspaces(transport: Transport, queryClient: QueryClient, orgId: string): Promise<Workspace[]> {
	const stored = readStorage(WORKSPACE_STORAGE_KEY);
	const [newest, remembered] = await Promise.all([
		queryClient.query(createQueryOptions(listOrgWorkspaces, { orgId, pageSize: 1 }, { transport })),
		stored === null
			? undefined
			: queryClient.query(createQueryOptions(getWorkspace, { workspaceId: stored }, { transport })).then(
					(res) => res.workspace,
					() => undefined,
				),
	]);
	return remembered?.orgId === orgId ? [...newest.workspaces, remembered] : newest.workspaces;
}
