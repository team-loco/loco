import { useQuery } from "@connectrpc/connect-query";
import { listOrgPeople } from "@gen/loco/org/v1/org-OrgService_connectquery";

/** Invitations not yet expired, which count against the organization's invitation limit; undefined until loaded. */
export function useOpenInvitations(orgId: string): number | undefined {
	const { data } = useQuery(listOrgPeople, { orgId, pageSize: 1, includeTotalCount: true }, { enabled: orgId !== "" });
	return data?.openInvitationCount;
}
