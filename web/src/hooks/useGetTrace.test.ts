import { describe, expect, test } from "bun:test";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { ObservabilityProxyService, type GetTraceRequest } from "@gen/loco/observability/v1/observability_pb";
import { getTraceQueryOptions } from "./useGetTrace";

const input = { workspaceId: "workspace", environmentId: "production", resourceIds: ["cart"], traceId: "trace" };

describe("get trace", () => {
	test("fetches every span in one scoped request", async () => {
		let received: GetTraceRequest | undefined;
		const transport = createRouterTransport((router) => {
			router.service(ObservabilityProxyService, {
				getTrace(request) {
					received = request;
					return { spans: Array.from({ length: 700 }, (_, i) => ({ traceId: request.traceId, spanId: String(i) })) };
				},
			});
		});
		const response = await getTraceQueryOptions(transport, input).queryFn({ signal: new AbortController().signal });
		expect(response.spans).toHaveLength(700);
		expect(received?.scope).toMatchObject({
			workspaceId: input.workspaceId,
			environmentId: input.environmentId,
			resourceIds: input.resourceIds,
		});
		expect(received?.traceId).toBe(input.traceId);
	});
	test("separates trace and scope in the cache", () => {
		const transport = createRouterTransport(() => {});
		const base = getTraceQueryOptions(transport, input).queryKey;
		for (const change of [
			{ workspaceId: "other" },
			{ environmentId: "staging" },
			{ resourceIds: [] },
			{ traceId: "other" },
		]) {
			expect(getTraceQueryOptions(transport, { ...input, ...change }).queryKey).not.toEqual(base);
		}
		expect(getTraceQueryOptions(transport, { ...input, traceId: "" }).enabled).toBe(false);
	});
	test("preserves errors and cancellation", async () => {
		const transport = createRouterTransport((router) => {
			router.service(ObservabilityProxyService, {
				getTrace() {
					throw new ConnectError("Workspace read required", Code.PermissionDenied);
				},
			});
		});
		const options = getTraceQueryOptions(transport, input);
		expect(await options.queryFn({ signal: new AbortController().signal }).catch(String)).toContain(
			"Workspace read required",
		);
		const controller = new AbortController();
		controller.abort();
		expect(await options.queryFn({ signal: controller.signal }).catch(String)).toContain("canceled");
	});
});
