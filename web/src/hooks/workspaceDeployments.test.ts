import { describe, expect, test } from "bun:test";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { QueryClient } from "@tanstack/react-query";
import { DeploymentService, type ListDeploymentsRequest } from "@gen/loco/deployment/v1/deployment_pb";
import { workspaceDeploymentsOptions } from "./workspaceDeployments";

describe("workspace environment deployments", () => {
	test("loads deployments across resources through one paged environment query", async () => {
		const requests: ListDeploymentsRequest[] = [];
		const transport = createRouterTransport((router) => {
			router.service(DeploymentService, {
				listDeployments(request) {
					requests.push(request);
					return request.pageToken === ""
						? { deployments: [{ id: "a", resourceId: "first" }], nextPageToken: "cursor" }
						: { deployments: [{ id: "b", resourceId: "second" }] };
				},
			});
		});
		const client = new QueryClient();
		try {
			const result = await client.query(workspaceDeploymentsOptions(transport, "workspace", "production"));
			expect(result.deployments.map((d) => d.resourceId)).toEqual(["first", "second"]);
			expect(requests.map((r) => [r.workspaceId, r.environmentId, r.resourceId, r.pageToken])).toEqual([
				["workspace", "production", "", ""],
				["workspace", "production", "", "cursor"],
			]);
			await client.query(workspaceDeploymentsOptions(transport, "workspace", "staging"));
			expect(requests.at(-1)?.environmentId).toBe("staging");
		} finally {
			client.clear();
		}
	});

	test("fails the whole query when a later page fails", async () => {
		const transport = createRouterTransport((router) => {
			router.service(DeploymentService, {
				listDeployments(request) {
					if (request.pageToken !== "") throw new ConnectError("unavailable", Code.Unavailable);
					return { deployments: [{ id: "a" }], nextPageToken: "cursor" };
				},
			});
		});
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		try {
			const options = workspaceDeploymentsOptions(transport, "workspace", "production");
			const error: unknown = await client.query(options).catch((failure: unknown) => failure);
			expect(error).toBeInstanceOf(ConnectError);
			expect((error as ConnectError).code).toBe(Code.Unavailable);
			expect(client.getQueryData(options.queryKey)).toBeUndefined();
		} finally {
			client.clear();
		}
	});
});
