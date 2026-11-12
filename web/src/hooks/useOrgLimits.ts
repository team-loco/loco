import { useQuery } from "@connectrpc/connect-query";

import type { CountLimits } from "@gen/loco/config/v1/config_pb";
import { getOrgLimits } from "@gen/loco/org/v1/org-OrgService_connectquery";

/** The organization's effective count limits, or undefined while they load. */
export function useOrgLimits(orgId: string): CountLimits | undefined {
	const query = useQuery(getOrgLimits, { orgId }, { enabled: orgId !== "" });
	return query.data?.limits?.limits;
}
