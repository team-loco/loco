import { describe, expect, test } from "bun:test";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { QueryClient } from "@tanstack/react-query";
import {
	ObservabilityProxyService,
	TraceStatus,
	TraceSummarySort,
	type QueryTracesRequest,
} from "@gen/loco/observability/v1/observability_pb";
import { traceSummaryQueryOptions } from "./useTraceSummaries";

const input = {
	workspaceId: "workspace",
	environmentId: "production",
	resourceIds: ["cart"],
	traceId: "",
	start: 1_800_000_000_123,
	end: 1_800_003_600_456,
	limit: 25,
	summary: { offset: 25, rootNameTerms: ["GET", "/cart"], status: TraceStatus.ERROR, sort: TraceSummarySort.DURATION },
};

describe("trace summary queries", () => {
	test("requests a complete trace page with server filters and total count", async () => {
		let received: QueryTracesRequest | undefined;
		const transport = createRouterTransport((router) => {
			router.service(ObservabilityProxyService, {
				queryTraces(request) {
					received = request;
					return { totalCount: 67n, summaries: [{ traceId: "trace", rootSpanName: "GET /cart", spanCount: 900n }] };
				},
			});
		});
		const client = new QueryClient();
		const result = await client.query(traceSummaryQueryOptions(transport, input));
		expect(result.totalCount).toBe(67n);
		expect(result.summaries[0]?.spanCount).toBe(900n);
		expect(received?.workspaceId).toBe(input.workspaceId);
		expect(received?.environmentId).toBe(input.environmentId);
		expect(received?.resourceIds).toEqual(input.resourceIds);
		expect(received?.limit).toBe(25);
		expect(received?.cursor).toBe("");
		expect(received?.summary).toMatchObject(input.summary);
		expect(received?.startTime?.nanos).toBe(123_000_000);
		expect(received?.endTime?.nanos).toBe(456_000_000);
		client.clear();
	});

	test("separates page sort filters and scope in cache identity", () => {
		const transport = createRouterTransport(() => {});
		const base = traceSummaryQueryOptions(transport, input).queryKey;
		for (const change of [
			{ workspaceId: "other" },
			{ environmentId: "staging" },
			{ resourceIds: ["checkout"] },
			{ traceId: "lookup" },
			{ start: input.start + 1 },
			{ end: input.end + 1 },
			{ limit: 50 },
			{ summary: { ...input.summary, offset: 50 } },
			{ summary: { ...input.summary, status: TraceStatus.OK } },
			{ summary: { ...input.summary, rootNameTerms: ["POST"] } },
			{ summary: { ...input.summary, sort: TraceSummarySort.START_TIME } },
		]) {
			expect(traceSummaryQueryOptions(transport, { ...input, ...change }).queryKey).not.toEqual(base);
		}
		expect(traceSummaryQueryOptions(transport, { ...input, workspaceId: "" }).enabled).toBe(false);
		expect(traceSummaryQueryOptions(transport, { ...input, environmentId: "" }).enabled).toBe(false);
	});

	test("preserves errors and cancels abandoned requests", async () => {
		const transport = createRouterTransport((router) => {
			router.service(ObservabilityProxyService, {
				queryTraces() {
					throw new ConnectError("Workspace read required", Code.PermissionDenied);
				},
			});
		});
		const options = traceSummaryQueryOptions(transport, input);
		expect(await options.queryFn({ signal: new AbortController().signal }).catch(String)).toContain(
			"Workspace read required",
		);
		const controller = new AbortController();
		controller.abort();
		expect(await options.queryFn({ signal: controller.signal }).catch(String)).toContain("canceled");
	});
});
