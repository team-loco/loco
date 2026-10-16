import { create } from "@bufbuild/protobuf";
import { describe, expect, test } from "bun:test";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { createConnectQueryKey, createQueryOptions } from "@connectrpc/connect-query";
import { QueryClient } from "@tanstack/react-query";
import { ListWorkspaceResourcesResponseSchema, ResourceService, type ListWorkspaceResourcesRequest } from "@gen/loco/resource/v1/resource_pb";
import { listWorkspaceResources } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import { workspaceResourcesOptions } from "./workspaceResources";

const clientOptions = { defaultOptions: { queries: { retry: false } } };

describe("complete workspace resources", () => {
	test("loads resource 201 through an opaque cursor with the same scope", async () => {
		const requests: ListWorkspaceResourcesRequest[] = [];
		const transport = createRouterTransport(router => { router.service(ResourceService, {
			listWorkspaceResources(request) {
				requests.push(request);
				return request.pageToken === "" ? {
					resources: Array.from({ length: 200 }, (_, i) => ({ id: `resource-${i}`, workspaceId: request.workspaceId })),
					nextPageToken: "opaque/cursor",
				} : { resources: [{ id: "resource-201", workspaceId: request.workspaceId }] };
			},
		}); });
		const client = new QueryClient(clientOptions);
		try {
			const response = await client.query(workspaceResourcesOptions(transport, "workspace"));
			expect(response.resources).toHaveLength(201);
			expect(response.resources.at(-1)?.id).toBe("resource-201");
			expect(response.nextPageToken).toBe("");
			expect(requests.map(r => [r.workspaceId, r.pageSize, r.pageToken])).toEqual([
				["workspace", 200, ""], ["workspace", 200, "opaque/cursor"],
			]);
		} finally { client.clear(); }
	});

	test("rejects a later-page error without caching a partial list and can retry", async () => {
		let fail = true;
		const transport = createRouterTransport(router => { router.service(ResourceService, {
			listWorkspaceResources(request) {
				if (request.pageToken === "") return { resources: [{ id: "first" }], nextPageToken: "next" };
				if (fail) throw new ConnectError("Resource list unavailable", Code.Unavailable);
				return { resources: [{ id: "last" }] };
			},
		}); });
		const client = new QueryClient(clientOptions);
		const options = workspaceResourcesOptions(transport, "workspace");
		try {
			expect(await client.query(options).catch(String)).toContain("Resource list unavailable");
			const partial = client.getQueryData(options.queryKey);
			expect(partial).toBeUndefined();
			fail = false;
			expect((await client.query(options)).resources.map(r => r.id)).toEqual(["first", "last"]);
			fail = true;
			await client.invalidateQueries({ queryKey: options.queryKey });
			expect(await client.query(options).catch(String)).toContain("Resource list unavailable");
			expect(client.getQueryData(options.queryKey)?.resources.map(r => r.id)).toEqual(["first", "last"]);
		} finally { client.clear(); }
	});

	test("rejects repeated cursors", async () => {
		const transport = createRouterTransport(router => { router.service(ResourceService, {
			listWorkspaceResources() { return { nextPageToken: "same" }; },
		}); });
		const client = new QueryClient(clientOptions);
		try { expect(await client.query(workspaceResourcesOptions(transport, "workspace")).catch(String)).toContain("complete resource list"); }
		finally { client.clear(); }
	});

	test("separates complete lists from first pages and retains RPC invalidation", async () => {
		const transport = createRouterTransport(() => {});
		const options = workspaceResourcesOptions(transport, "workspace");
		const firstPage = createQueryOptions(listWorkspaceResources, { workspaceId: "workspace", pageSize: 200 }, { transport });
		expect(options.queryKey).not.toEqual(firstPage.queryKey);
		expect(options.queryKey).not.toEqual(workspaceResourcesOptions(transport, "other").queryKey);
		expect(options.queryKey).not.toEqual(workspaceResourcesOptions(createRouterTransport(() => {}), "workspace").queryKey);
		const client = new QueryClient(clientOptions);
		try {
			client.setQueryData(options.queryKey, create(ListWorkspaceResourcesResponseSchema));
			const queryKey = createConnectQueryKey({ schema: listWorkspaceResources, input: { workspaceId: "workspace", pageSize: 200 }, cardinality: "finite" });
			await client.invalidateQueries({ queryKey });
			expect(client.getQueryState(options.queryKey)?.isInvalidated).toBe(true);
		} finally { client.clear(); }
	});

	test("forwards cancellation on later pages", async () => {
		const controller = new AbortController();
		let calls = 0;
		const transport = createRouterTransport(router => { router.service(ResourceService, {
			listWorkspaceResources() { calls++; controller.abort(); return { nextPageToken: "next" }; },
		}); });
		const options = workspaceResourcesOptions(transport, "workspace");
		expect(await Promise.resolve(options.queryFn!({ signal: controller.signal, queryKey: options.queryKey, client: new QueryClient(), meta: undefined })).catch(String)).toContain("canceled");
		expect(calls).toBe(1);
	});
});
