import { describe, expect, test } from "bun:test";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { QueryClient } from "@tanstack/react-query";
import {
	ObservabilityService,
	TraceStatus,
	TraceSummarySort,
	type ListTracesRequest,
} from "@gen/loco/observability/v1/observability_pb";
import { listTracesQueryOptions } from "./useListTraces";

const input = {
	workspaceId: "workspace",
	environmentId: "production",
	resourceIds: ["cart"],
	traceId: "",
	start: 1_800_000_000_123,
	end: 1_800_003_600_456,
	limit: 25,
	offset: 25,
	rootNameTerms: ["GET", "/cart"],
	status: TraceStatus.ERROR,
	sort: TraceSummarySort.DURATION,
};

describe("trace summary queries", () => {
	test("requests a complete trace page with server filters and total count", async () => {
		let received: ListTracesRequest | undefined;
		const transport = createRouterTransport((router) => {
			router.service(ObservabilityService, {
				listTraces(request) {
					received = request;
					return { totalCount: 67n, summaries: [{ traceId: "trace", rootSpanName: "GET /cart", spanCount: 900n }] };
				},
			});
		});
		const client = new QueryClient();
		const result = await client.query(listTracesQueryOptions(transport, input));
		expect(result.totalCount).toBe(67n);
		expect(result.summaries[0]?.spanCount).toBe(900n);
		expect(received?.scope?.workspaceId).toBe(input.workspaceId);
		expect(received?.scope?.environmentId).toBe(input.environmentId);
		expect(received?.scope?.resourceIds).toEqual(input.resourceIds);
		expect(received?.limit).toBe(25);
		expect(received).toMatchObject({
			offset: input.offset,
			rootNameTerms: input.rootNameTerms,
			status: input.status,
			sort: input.sort,
		});
		expect(received?.startTime?.nanos).toBe(123_000_000);
		expect(received?.endTime?.nanos).toBe(456_000_000);
		client.clear();
	});

	test("separates page sort filters and scope in cache identity", () => {
		const transport = createRouterTransport(() => {});
		const base = listTracesQueryOptions(transport, input).queryKey;
		for (const change of [
			{ workspaceId: "other" },
			{ environmentId: "staging" },
			{ resourceIds: ["checkout"] },
			{ traceId: "lookup" },
			{ start: input.start + 1 },
			{ end: input.end + 1 },
			{ limit: 50 },
			{ offset: 50 },
			{ status: TraceStatus.OK },
			{ rootNameTerms: ["POST"] },
			{ sort: TraceSummarySort.START_TIME },
		]) {
			expect(listTracesQueryOptions(transport, { ...input, ...change }).queryKey).not.toEqual(base);
		}
		expect(listTracesQueryOptions(transport, { ...input, workspaceId: "" }).enabled).toBe(false);
		expect(listTracesQueryOptions(transport, { ...input, environmentId: "" }).enabled).toBe(false);
	});

	test("preserves errors and cancels abandoned requests", async () => {
		const transport = createRouterTransport((router) => {
			router.service(ObservabilityService, {
				listTraces() {
					throw new ConnectError("Workspace read required", Code.PermissionDenied);
				},
			});
		});
		const options = listTracesQueryOptions(transport, input);
		expect(await options.queryFn({ signal: new AbortController().signal }).catch(String)).toContain(
			"Workspace read required",
		);
		const controller = new AbortController();
		controller.abort();
		expect(await options.queryFn({ signal: controller.signal }).catch(String)).toContain("canceled");
	});
});
