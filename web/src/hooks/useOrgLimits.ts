import { useQuery } from "@connectrpc/connect-query";

import type { CountLimits } from "@gen/loco/config/v1/config_pb";
import { getOrgLimits } from "@gen/loco/org/v1/org-OrgService_connectquery";
import type { OrgLimits } from "@gen/loco/org/v1/org_pb";
import { getScopes } from "@gen/loco/token/v1/token-TokenService_connectquery";

import { hasOrganizationMembership } from "@/hooks/useMyScopes";

/**
 * The organization's effective limits with who raised them and why, or undefined while they load
 * and for a caller outside the organization, whom GetOrgLimits refuses.
 */
export function useOrgLimitDetails(orgId: string): OrgLimits | undefined {
	const scopes = useQuery(getScopes, {});
	const member = hasOrganizationMembership(scopes.data?.scopes ?? [], orgId);
	const query = useQuery(getOrgLimits, { orgId }, { enabled: orgId !== "" && member });
	return query.data?.limits;
}

export function useOrgLimits(orgId: string): CountLimits | undefined {
	return useOrgLimitDetails(orgId)?.limits;
}
