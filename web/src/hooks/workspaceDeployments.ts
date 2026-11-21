import { create } from "@bufbuild/protobuf";
import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey } from "@connectrpc/connect-query";
import { queryOptions, type QueryClient } from "@tanstack/react-query";
import { listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { DeploymentService, ListDeploymentsResponseSchema } from "@gen/loco/deployment/v1/deployment_pb";

import { workspaceResourcesKey } from "@/hooks/workspaceResources";
import { collectPages } from "@/lib/collectPages";

const PAGE_SIZE = 200;

export function workspaceDeploymentsOptions(transport: Transport, workspaceId: string, environmentId: string) {
	const input = { workspaceId, environmentId, pageSize: PAGE_SIZE };
	const client = createClient(DeploymentService, transport);
	const key = createConnectQueryKey({ schema: listDeployments, input, transport, cardinality: "finite" });
	return queryOptions({
		queryKey: [...key, "all-pages"],
		enabled: workspaceId !== "" && environmentId !== "",
		queryFn: async ({ signal }) => {
			const deployments = await collectPages(async (pageToken) => {
				const page = await client.listDeployments({ ...input, pageToken }, { signal });
				return { items: page.deployments, nextPageToken: page.nextPageToken };
			});
			return create(ListDeploymentsResponseSchema, { deployments });
		},
	});
}

/** Refetches the workspace's resource and deployment lists and the resource's deployment history after a deploy. */
export async function invalidateDeploys(
	queryClient: QueryClient,
	transport: Transport,
	workspaceId: string,
	resourceId: string | undefined,
) {
	const deployments = (input: { workspaceId: string } | { resourceId: string }) =>
		createConnectQueryKey({ schema: listDeployments, input, transport, cardinality: undefined });
	await Promise.all(
		[
			workspaceResourcesKey(transport, workspaceId),
			deployments({ workspaceId }),
			...(resourceId === undefined ? [] : [deployments({ resourceId })]),
		].map(async (queryKey) => {
			await queryClient.invalidateQueries({ queryKey });
		}),
	);
}
