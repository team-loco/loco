import { describe, expect, test } from "bun:test";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { QueryClient } from "@tanstack/react-query";
import { ObservabilityProxyService, type QueryTracesRequest } from "@gen/loco/observability/v1/observability_pb";
import { tracesQueryOptions } from "./useQueryTraces";

const input = { workspaceId: "workspace", environmentId: "environment", resourceIds: ["cart"], traceId: "trace", start: 1_800_000_000_000, end: 1_800_003_600_000 };

describe("regional trace queries", () => {
	test("preserves scope and frozen bounds across opaque cursor pages", async () => {
		const received: QueryTracesRequest[] = [];
		const transport = createRouterTransport((router) => { router.service(ObservabilityProxyService, { queryTraces(request) { received.push(request); return { nextCursor: request.cursor === "" ? "opaque/not-a-date" : "" }; } }); });
		const options = tracesQueryOptions(transport, input);
		const client = new QueryClient();
		await client.infiniteQuery(options);
		await client.infiniteQuery({ ...options, staleTime: 0, pages: 2 });
		expect(received.at(-1)?.cursor).toBe("opaque/not-a-date");
		for (const request of received) {
			expect(request.workspaceId).toBe(input.workspaceId);
			expect(request.environmentId).toBe(input.environmentId);
			expect(request.resourceIds).toEqual(input.resourceIds);
			expect(request.traceId).toBe(input.traceId);
			expect(request.startTime?.seconds).toBe(1_800_000_000n);
			expect(request.endTime?.seconds).toBe(1_800_003_600n);
		}
		client.clear();
	});
	test("cache identity separates workspace environment transport filter trace and time bounds", () => {
		const transport = createRouterTransport(() => {});
		const base = tracesQueryOptions(transport, input).queryKey;
		for (const change of [{ workspaceId: "other" }, { environmentId: "other" }, { resourceIds: [] }, { traceId: "other" }, { start: input.start - 1000 }, { end: input.end + 1000 }]) expect(tracesQueryOptions(transport, { ...input, ...change }).queryKey).not.toEqual(base);
		expect(tracesQueryOptions(createRouterTransport(() => {}), input).queryKey).not.toEqual(base);
		expect(tracesQueryOptions(transport, { ...input, environmentId: "" }).enabled).toBe(false);
	});
	test("keeps millisecond bounds distinct and forwards cancellation", async () => {
		let received: QueryTracesRequest | undefined;
		const transport = createRouterTransport((router) => { router.service(ObservabilityProxyService, { queryTraces(request) { received = request; return {}; } }); });
		const options = tracesQueryOptions(transport, { ...input, start: input.start + 123, end: input.end + 456 });
		await options.queryFn({ pageParam: "", signal: new AbortController().signal });
		expect(received?.startTime?.nanos).toBe(123_000_000);
		expect(received?.endTime?.nanos).toBe(456_000_000);
		const controller = new AbortController();
		controller.abort();
		expect(await options.queryFn({ pageParam: "", signal: controller.signal }).catch(String)).toContain("canceled");
	});

	test("permission errors remain errors", async () => {
		const transport = createRouterTransport((router) => { router.service(ObservabilityProxyService, { queryTraces() { throw new ConnectError("Workspace read required", Code.PermissionDenied); } }); });
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		expect(await client.infiniteQuery(tracesQueryOptions(transport, input)).catch(String)).toContain("Workspace read required");
		client.clear();
	});
});
