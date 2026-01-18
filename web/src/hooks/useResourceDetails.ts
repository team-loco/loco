import { getResource, getResourceStatus } from "@/gen/loco/resource/v1";
import { listDeployments } from "@/gen/loco/deployment/v1";
import { useQueries } from "@tanstack/react-query";
import { createQueryOptions, useTransport } from "@connectrpc/connect-query";

export function useResourceDetails(resourceId: string) {
	const transport = useTransport();

	const [resourceQuery, statusQuery, deploymentsQuery] = useQueries({
		queries: [
			createQueryOptions(
				getResource,
				{ key: { case: "resourceId" as const, value: BigInt(resourceId) } },
				{ transport }
			),
			createQueryOptions(
				getResourceStatus,
				{ resourceId: BigInt(resourceId) },
				{ transport }
			),
			createQueryOptions(
				listDeployments,
				{ pageSize: 10, resourceId: BigInt(resourceId) },
				{ transport }
			),
		],
	});

	// Disable queries if no resourceId
	const isEnabled = !!resourceId;

	return {
		resource: isEnabled && resourceQuery.data ? resourceQuery.data : null,
		status:
			isEnabled && statusQuery.data ? statusQuery.data.currentDeployment : null,
		deployments:
			isEnabled && deploymentsQuery.data
				? deploymentsQuery.data.deployments
				: [],
		isLoading:
			isEnabled &&
			(resourceQuery.isLoading ||
				statusQuery.isLoading ||
				deploymentsQuery.isLoading),
		error: isEnabled
			? resourceQuery.error || statusQuery.error || deploymentsQuery.error
			: null,
	};
}
