import { useQuery } from "@connectrpc/connect-query";

import type { CountLimits } from "@gen/loco/config/v1/config_pb";
import { getOrgLimits } from "@gen/loco/org/v1/org-OrgService_connectquery";
import type { OrgLimits } from "@gen/loco/org/v1/org_pb";
import { getScopes } from "@gen/loco/token/v1/token-TokenService_connectquery";
import { EntityType } from "@gen/loco/token/v1/token_pb";

import { useOrgWorkspace } from "@/context/ContextProvider";
import { hasOrganizationMembership } from "@/hooks/useMyScopes";

/**
 * The organization's effective limits, or undefined while they load and for a caller with no scope
 * in the organization, whom GetOrgLimits refuses. A caller with only a workspace scope reads the
 * workspace-level limits without who raised them and why.
 */
export function useOrgLimitDetails(orgId: string): OrgLimits | undefined {
	const scopes = useQuery(getScopes, {});
	const { workspaces, activeOrgId, activeWorkspaceId } = useOrgWorkspace();
	const held = scopes.data?.scopes ?? [];
	const inOrg = new Set(workspaces.filter((w) => w.orgId === orgId).map((w) => w.id));
	if (activeOrgId === orgId && activeWorkspaceId !== null) inOrg.add(activeWorkspaceId);
	const scoped =
		hasOrganizationMembership(held, orgId) ||
		held.some((s) => s.entityType === EntityType.WORKSPACE && inOrg.has(s.entityId));
	const query = useQuery(getOrgLimits, { orgId }, { enabled: orgId !== "" && scoped, retry: false });
	return query.data?.limits;
}

export function useOrgLimits(orgId: string): CountLimits | undefined {
	return useOrgLimitDetails(orgId)?.limits;
}
