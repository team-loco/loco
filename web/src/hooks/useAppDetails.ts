import { useQuery } from "@connectrpc/connect-query";
import { getApp, getAppStatus } from "@/gen/app/v1";
import { listDeployments } from "@/gen/deployment/v1";

export function useAppDetails(appId: string) {
	const {
		data: appRes,
		isLoading: appLoading,
		error: appError,
	} = useQuery(getApp, { appId }, { enabled: !!appId });

	const {
		data: statusRes,
		isLoading: statusLoading,
		error: statusError,
	} = useQuery(getAppStatus, { appId }, { enabled: !!appId });

	const {
		data: deploymentsRes,
		isLoading: deploymentsLoading,
		error: deploymentsError,
	} = useQuery(
		listDeployments,
		{ appId, limit: 10 },
		{ enabled: !!appId }
	);

	return {
		app: appRes?.app ?? null,
		status: statusRes?.status ?? null,
		deployments: deploymentsRes?.deployments ?? [],
		isLoading: appLoading || statusLoading || deploymentsLoading,
		error: appError || statusError || deploymentsError,
	};
}
