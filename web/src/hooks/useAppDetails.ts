import { getApp, getAppStatus } from "@/gen/app/v1";
import { listDeployments } from "@/gen/deployment/v1";
import { useQuery } from "@connectrpc/connect-query";

export function useAppDetails(appId: string) {
	const {
		data: appRes,
		isLoading: appLoading,
		error: appError,
	} = useQuery(getApp, appId ? { appId: BigInt(appId) } : undefined, { enabled: !!appId });

	const {
		data: statusRes,
		isLoading: statusLoading,
		error: statusError,
	} = useQuery(getAppStatus, { appId: BigInt(appId) }, { enabled: !!appId });

	const {
		data: deploymentsRes,
		isLoading: deploymentsLoading,
		error: deploymentsError,
	} = useQuery(
		listDeployments,
		{ limit: 10, appId: BigInt(appId) },
		{ enabled: !!appId }
	);

	return {
		app: appRes?.app ?? null,
		status: statusRes?.currentDeployment ?? null,
		deployments: deploymentsRes?.deployments ?? [],
		isLoading: appLoading || statusLoading || deploymentsLoading,
		error: appError || statusError || deploymentsError,
	};
}
