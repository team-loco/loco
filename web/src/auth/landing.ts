import type { Transport } from "@connectrpc/connect";
import { callUnaryMethod, createConnectQueryKey, createQueryOptions } from "@connectrpc/connect-query";
import type { QueryClient } from "@tanstack/react-query";
import { listUserOrgs } from "@gen/loco/org/v1/org-OrgService_connectquery";
import type { Organization } from "@gen/loco/org/v1/org_pb";
import { getScopes } from "@gen/loco/token/v1/token-TokenService_connectquery";
import { whoAmI } from "@gen/loco/user/v1/user-UserService_connectquery";

import { ORG_STORAGE_KEY, pickActive, WORKSPACE_STORAGE_KEY } from "@/context/ContextProvider";
import { orgWorkspacesOptions } from "@/hooks/orgWorkspaces";
import { hasOrganizationMembership } from "@/hooks/useMyScopes";
import { pageImporters } from "@/lib/lazy-pages";
import { workspacePath } from "@/lib/routes";

import { takeNextPath } from "./next";

export async function landingPath(transport: Transport, queryClient: QueryClient): Promise<string> {
	const { user } = await queryClient.query(createQueryOptions(whoAmI, {}, { transport }));
	if (user === undefined) throw new Error("Your account could not be loaded");

	const next = takeNextPath();
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
		? (await queryClient.query(orgWorkspacesOptions(transport, orgId))).workspaces
		: [];
	const workspaceId = pickActive(workspaces, undefined, WORKSPACE_STORAGE_KEY);
	if (workspaceId === null) {
		await pageImporters.Team();
		return `/org/${orgId}/team`;
	}
	await pageImporters.Dashboard();
	return workspacePath(orgId, workspaceId);
}
