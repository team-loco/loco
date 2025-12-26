import { getResource, getResourceStatus } from "@/gen/resource/v1";
import { listDeployments } from "@/gen/deployment/v1";
import { useQuery } from "@connectrpc/connect-query";

export function useAppDetails(appId: string) {
	const {
		data: appRes,
		isLoading: appLoading,
		error: appError,
	} = useQuery(getResource, appId ? { resourceId: BigInt(appId) } : undefined, {
		enabled: !!appId,
	});

	const {
		data: statusRes,
		isLoading: statusLoading,
		error: statusError,
	} = useQuery(
		getResourceStatus,
		{ resourceId: BigInt(appId) },
		{ enabled: !!appId }
	);

	const {
		data: deploymentsRes,
		isLoading: deploymentsLoading,
		error: deploymentsError,
	} = useQuery(
		listDeployments,
		{ limit: 10, resourceId: BigInt(appId) },
		{ enabled: !!appId }
	);

	return {
		app: appRes?.resource ?? null,
		status: statusRes?.currentDeployment ?? null,
		deployments: deploymentsRes?.deployments ?? [],
		isLoading: appLoading || statusLoading || deploymentsLoading,
		error: appError || statusError || deploymentsError,
	};
}
