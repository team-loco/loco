import { getResource, getResourceStatus } from "@/gen/resource/v1";
import { listDeployments } from "@/gen/deployment/v1";
import { useQueries } from "@tanstack/react-query";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";

export function useAppDetails(appId: string) {
	const transport = useTransport();

	const [appQuery, statusQuery, deploymentsQuery] = useQueries({
		queries: [
			createQueryOptions(
				getResource,
				{ resourceId: BigInt(appId) },
				{ transport }
			),
			createQueryOptions(
				getResourceStatus,
				{ resourceId: BigInt(appId) },
				{ transport }
			),
			createQueryOptions(
				listDeployments,
				{ limit: 10, resourceId: BigInt(appId) },
				{ transport }
			),
		],
	});

	// Disable queries if no appId
	const isEnabled = !!appId;

	return {
		app: isEnabled && appQuery.data ? appQuery.data.resource : null,
		status:
			isEnabled && statusQuery.data ? statusQuery.data.currentDeployment : null,
		deployments:
			isEnabled && deploymentsQuery.data
				? deploymentsQuery.data.deployments
				: [],
		isLoading:
			isEnabled &&
			(appQuery.isLoading ||
				statusQuery.isLoading ||
				deploymentsQuery.isLoading),
		error: isEnabled
			? appQuery.error || statusQuery.error || deploymentsQuery.error
			: null,
	};
}
