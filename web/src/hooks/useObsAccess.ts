import { useQuery } from "@connectrpc/connect-query";
import { getObservabilityAccess } from "@/gen/loco/observability/v1/observability_access-ObservabilityAccessService_connectquery";

// Cluster endpoints change rarely — keep fresh for 10 minutes, hold in cache for 30.
const STALE_TIME_MS = 10 * 60 * 1000;
const GC_TIME_MS = 30 * 60 * 1000;

export function useObsAccess(workspaceId: string) {
	return useQuery(
		getObservabilityAccess,
		workspaceId ? { workspaceId, resourceIds: [] } : undefined,
		{
			enabled: !!workspaceId,
			staleTime: STALE_TIME_MS,
			gcTime: GC_TIME_MS,
			refetchOnWindowFocus: false,
		},
	);
}
