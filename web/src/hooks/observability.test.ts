import { describe, expect, spyOn, test } from "bun:test";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { QueryClient } from "@tanstack/react-query";
import { ObservabilityProxyService, type QueryLogsRequest, type QueryMetricsRequest } from "@gen/loco/observability/v1/observability_pb";

import { logsQueryOptions } from "./useQueryLogs";
import { metricsQueryOptions } from "./useQueryMetrics";

const workspaceId = "11111111-2222-3333-4444-555555555555";
const logOptions = { workspaceId, resourceIds: [], timeRange: "1h" as const,
	parsedQuery: { search: "", levels: [], labels: {}, regions: ["us-*"], excludedRegions: ["us-west-*"] } };

describe("regional control-plane queries", () => {
	test("preserves milliseconds in log and metric query bounds", async () => {
		const received: (QueryLogsRequest | QueryMetricsRequest)[] = [];
		const transport = createRouterTransport((router) => {
			router.service(ObservabilityProxyService, {
				queryLogs(request) { received.push(request); return {}; },
				queryMetrics(request) { received.push(request); return {}; },
			});
		});
		const clock = spyOn(Date, "now").mockReturnValue(Date.UTC(2026, 9, 16, 6, 45, 12, 789));
		const client = new QueryClient();
		try {
			await client.query(logsQueryOptions(transport, logOptions));
			await client.query(metricsQueryOptions(transport, {
				workspaceId, resourceIds: [], metricName: "k8s.pod.cpu.usage", timeRange: "1h",
			}));
			for (const request of received) {
				expect(request.startTime?.nanos).toBe(789_000_000);
				expect(request.endTime?.nanos).toBe(789_000_000);
			}
			expect(received).toHaveLength(2);
		} finally {
			clock.mockRestore();
			client.clear();
		}
	});

	test("queries history once without cluster discovery and preserves an opaque cursor", async () => {
		const received: QueryLogsRequest[] = [];
		const transport = createRouterTransport((router) => {
			router.service(ObservabilityProxyService, { queryLogs(request) {
				received.push(request);
				return { entries: [{ body: "history" }], nextCursor: "opaque/not-a-date" };
			} });
		});
		const client = new QueryClient();
		const options = logsQueryOptions(transport, logOptions);
		expect(options.enabled).toBe(true);
		const response = await client.query(options);
		await client.query(options);
		expect(received).toHaveLength(1);
		expect(received[0]?.resourceIds).toEqual([]);
		expect(received[0]?.regions).toEqual(["us-*"]);
		expect(received[0]?.excludedRegions).toEqual(["us-west-*"]);
		expect(response.nextCursor).toBe("opaque/not-a-date");
		await client.query(logsQueryOptions(transport, { ...logOptions, cursor: response.nextCursor }));
		expect(received[1]?.cursor).toBe("opaque/not-a-date");
		client.clear();
	});

	test("isolates selected control-plane transports and region metric caches", async () => {
		const received: QueryMetricsRequest[] = [];
		const transport = createRouterTransport((router) => {
			router.service(ObservabilityProxyService, { queryMetrics(request) { received.push(request); return {}; } });
		});
		const otherTransport = createRouterTransport((router) => {
			router.service(ObservabilityProxyService, { queryMetrics() { throw new ConnectError("other control plane", Code.Unavailable); } });
		});
		const input = { workspaceId, resourceIds: [], metricName: "k8s.pod.cpu.usage", timeRange: "1h" as const };
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const east = metricsQueryOptions(transport, { ...input, region: "us-east-1" });
		const west = metricsQueryOptions(transport, { ...input, region: "eu-west-1" });
		expect(east.enabled).toBe(true);
		expect(east.queryKey).not.toEqual(west.queryKey);
		await client.query(east);
		await client.query(west);
		expect(received.map((request) => request.region)).toEqual(["us-east-1", "eu-west-1"]);
		expect(metricsQueryOptions(otherTransport, input).queryKey).not.toEqual(metricsQueryOptions(transport, input).queryKey);
		expect(await client.query(metricsQueryOptions(otherTransport, input)).catch(String)).toContain("other control plane");
		expect(received).toHaveLength(2);
		client.clear();
	});

	test("passes permission errors through without replacing them with empty history", async () => {
		const transport = createRouterTransport((router) => {
			router.service(ObservabilityProxyService, { queryLogs() { throw new ConnectError("workspace read required", Code.PermissionDenied); } });
		});
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		expect(await client.query(logsQueryOptions(transport, logOptions)).catch(String)).toContain("workspace read required");
		client.clear();
	});
});
