import { createConnectQueryKey, useQuery, useTransport } from "@connectrpc/connect-query";
import { getOrg, listOrgPeople } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { useQuery as useTanstackQuery, useQueryClient } from "@tanstack/react-query";

import { orgWorkspacesOptions } from "@/hooks/orgWorkspaces";

/** The organization and its workspaces, which the Team page's filter and access drawer list. */
export function useTeamData(orgId: string) {
	const transport = useTransport();
	const queryClient = useQueryClient();
	const orgQuery = useQuery(getOrg, { orgId }, { enabled: orgId !== "" });
	const wsQuery = useTanstackQuery(orgWorkspacesOptions(transport, orgId));
	const invalidatePeople = async () => {
		await queryClient.invalidateQueries({
			queryKey: createConnectQueryKey({ schema: listOrgPeople, cardinality: "finite" }),
		});
	};
	return {
		org: orgQuery.data?.organization,
		workspaces: wsQuery.data?.workspaces ?? [],
		orgError: orgQuery.error,
		refetchOrg: async () => await orgQuery.refetch(),
		/** Refetches every page and count of the people list, the open-invitation count included. */
		invalidatePeople,
	};
}
