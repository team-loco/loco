import { useQuery } from "@connectrpc/connect-query";
import { getObservabilityAccess } from "@/gen/loco/observability/v1/observability_access-ObservabilityAccessService_connectquery";

export function useObsAccess(workspaceId: string) {
	return useQuery(
		getObservabilityAccess,
		workspaceId ? { workspaceId, resourceIds: [] } : undefined,
		{
			enabled: !!workspaceId,
			// Treat data as fresh for 9 minutes; tokens are typically valid for 15+ minutes
			staleTime: 9 * 60 * 1000,
			refetchOnWindowFocus: false,
		},
	);
}
