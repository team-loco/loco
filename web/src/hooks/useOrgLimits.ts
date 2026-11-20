import { useQuery } from "@connectrpc/connect-query";

import type { CountLimits } from "@gen/loco/config/v1/config_pb";
import { getOrgLimits } from "@gen/loco/org/v1/org-OrgService_connectquery";
import type { OrgLimits } from "@gen/loco/org/v1/org_pb";
import { getScopes } from "@gen/loco/token/v1/token-TokenService_connectquery";

/**
 * The organization's effective limits, or undefined while they load and for a caller with no scope
 * in the organization, whom GetOrgLimits refuses. Callers below organization level read the limits
 * without who raised them and why.
 */
export function useOrgLimitDetails(orgId: string): OrgLimits | undefined {
	const scopes = useQuery(getScopes, {});
	const scoped = (scopes.data?.scopes.length ?? 0) > 0;
	const query = useQuery(getOrgLimits, { orgId }, { enabled: orgId !== "" && scoped, retry: false });
	return query.data?.limits;
}

export function useOrgLimits(orgId: string): CountLimits | undefined {
	return useOrgLimitDetails(orgId)?.limits;
}
