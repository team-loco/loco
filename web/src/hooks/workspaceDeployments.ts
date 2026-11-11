import { create } from "@bufbuild/protobuf";
import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey } from "@connectrpc/connect-query";
import { queryOptions } from "@tanstack/react-query";
import { listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { DeploymentService, ListDeploymentsResponseSchema } from "@gen/loco/deployment/v1/deployment_pb";

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
