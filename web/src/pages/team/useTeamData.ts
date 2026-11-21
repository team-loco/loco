import { createConnectQueryKey, useQuery } from "@connectrpc/connect-query";
import { getOrg, listOrgPeople } from "@gen/loco/org/v1/org-OrgService_connectquery";
import { useQueryClient } from "@tanstack/react-query";

/** The organization the Team page shows. */
export function useTeamData(orgId: string) {
	const queryClient = useQueryClient();
	const orgQuery = useQuery(getOrg, { orgId }, { enabled: orgId !== "" });
	const invalidatePeople = async () => {
		await queryClient.invalidateQueries({
			queryKey: createConnectQueryKey({ schema: listOrgPeople, cardinality: "finite" }),
		});
	};
	return {
		org: orgQuery.data?.organization,
		orgError: orgQuery.error,
		refetchOrg: async () => await orgQuery.refetch(),
		/** Refetches every page and count of the people list, the open-invitation count included. */
		invalidatePeople,
	};
}
