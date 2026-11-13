import { useQuery } from "@connectrpc/connect-query";

import type { CountLimits } from "@gen/loco/config/v1/config_pb";
import { getOrgLimits } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { getScopes } from "@gen/loco/token/v1/token-TokenService_connectquery";

import { hasOrganizationMembership } from "@/hooks/useMyScopes";

/**
 * The organization's effective count limits, or undefined while they load and for a caller
 * outside the organization, whom GetOrgLimits refuses.
 */
export function useOrgLimits(orgId: string): CountLimits | undefined {
	const scopes = useQuery(getScopes, {});
	const member = hasOrganizationMembership(scopes.data?.scopes ?? [], orgId);
	const query = useQuery(getOrgLimits, { orgId }, { enabled: orgId !== "" && member });
	return query.data?.limits?.limits;
}
