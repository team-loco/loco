import { describe, expect, spyOn, test } from "bun:test";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { QueryClient } from "@tanstack/react-query";
import { LogOrder, ObservabilityProxyService, type QueryLogsRequest, type QueryMetricsRequest } from "@gen/loco/observability/v1/observability_pb";

import { latestLogsQueryOptions, logsQueryOptions } from "./useQueryLogs";
import { msToTimestamp } from "@/lib/time";
import { metricsQueryOptions } from "./useQueryMetrics";
import { toRows, visibleLogRows } from "@/pages/observability/rows";

const workspaceId = "11111111-2222-3333-4444-555555555555";
const logOptions = { workspaceId, resourceIds: [], timeRange: "1h" as const,
	parsedQuery: { search: "", levels: [], labels: {}, regions: ["us-*"], excludedRegions: ["us-west-*"] } };

describe("regional control-plane queries", () => {
	test("waits for an environment before enabling dashboard metrics", () => {
		const transport = createRouterTransport(() => {});
		const input = { workspaceId, resourceIds: [], metricName: "k8s.pod.cpu_limit_utilization", timeRange: "6h" as const };
		expect(metricsQueryOptions(transport, { ...input, environmentId: "" }).enabled).toBe(false);
		expect(metricsQueryOptions(transport, { ...input, environmentId: workspaceId }).enabled).toBe(true);
	});

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
				workspaceId, environmentId: workspaceId, resourceIds: [], metricName: "k8s.pod.cpu.usage", timeRange: "1h",
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

	test("loads logs on both sides of a selected time and preserves directional cursors", async () => {
		const received: QueryLogsRequest[] = [];
		const target = Date.UTC(2026, 9, 16, 6, 8, 29, 123);
		const transport = createRouterTransport((router) => {
			router.service(ObservabilityProxyService, { queryLogs(request) {
				received.push(request);
				const newer = request.order === LogOrder.OLDEST_FIRST;
				return { entries: [{ body: newer ? "after target" : "before target" }],
					nextCursor: newer ? "newer-cursor" : "older-cursor" };
			} });
		});
		const client = new QueryClient();
		const options = { ...logOptions, around: target,
			startTime: msToTimestamp(target - 30 * 60_000), endTime: msToTimestamp(target + 30 * 60_000) };
		try {
			const response = await client.query(logsQueryOptions(transport, options));
			expect(response.entries.map((entry) => entry.body)).toEqual(["before target", "after target"]);
			expect(received).toHaveLength(2);
			expect(received[0]?.endTime?.nanos).toBe(122_999_999);
			expect(received[1]?.startTime?.nanos).toBe(123_000_000);
			expect(response.olderCursor).toBe("older-cursor");
			expect(response.newerCursor).toBe("newer-cursor");
			await client.query(logsQueryOptions(transport, { ...options, cursor: response.olderCursor }));
			expect(received[2]?.order).toBe(LogOrder.NEWEST_FIRST);
			expect(received[2]?.endTime?.nanos).toBe(122_999_999);
			await client.query(logsQueryOptions(transport, { ...options, cursor: response.newerCursor, order: LogOrder.OLDEST_FIRST }));
			expect(received[3]?.order).toBe(LogOrder.OLDEST_FIRST);
			expect(received[3]?.startTime?.nanos).toBe(123_000_000);
			expect(received.every((request) => request.workspaceId === workspaceId)).toBe(true);
		} finally {
			client.clear();
		}
	});

	test("resumes anchored history with a fresh latest window and no directional cursor", async () => {
		const received: QueryLogsRequest[] = [];
		const target = Date.UTC(2026, 9, 16, 6, 0);
		const resumed = target + 40 * 60_000;
		const transport = createRouterTransport((router) => {
			router.service(ObservabilityProxyService, { queryLogs(request) {
				received.push(request);
				return { entries: request.endTime?.seconds === BigInt(resumed / 1000)
					? [{ body: "arrived while paused" }] : [], nextCursor: "latest-older" };
			} });
		});
		const client = new QueryClient();
		try {
			const response = await client.query(latestLogsQueryOptions(transport, {
				...logOptions, resourceIds: ["recommendation"], around: target,
				cursor: "focused-newer-page", order: LogOrder.OLDEST_FIRST,
				startTime: msToTimestamp(target - 30 * 60_000), endTime: msToTimestamp(target + 30 * 60_000),
			}, resumed));
			expect(received).toHaveLength(1);
			const request = received[0];
			expect(request?.cursor).toBe("");
			expect(request?.order).toBe(LogOrder.NEWEST_FIRST);
			expect(request?.startTime).toEqual(msToTimestamp(resumed - 60 * 60_000));
			expect(request?.endTime).toEqual(msToTimestamp(resumed));
			expect(request?.workspaceId).toBe(workspaceId);
			expect(request?.resourceIds).toEqual(["recommendation"]);
			expect(request?.regions).toEqual(["us-*"]);
			expect(request?.excludedRegions).toEqual(["us-west-*"]);
			expect(response.entries.map((entry) => entry.body)).toEqual(["arrived while paused"]);
			expect(response.olderCursor).toBe("latest-older");
			expect(response.newerCursor).toBe("");
		} finally {
			client.clear();
		}
	});

	test("keeps paused rows after a failed latest refresh and fills the gap on retry", async () => {
		let fail = false;
		const target = Date.UTC(2026, 9, 16, 6, 0);
		const resumed = target + 40 * 60_000;
		const transport = createRouterTransport((router) => {
			router.service(ObservabilityProxyService, { queryLogs(request) {
				if (fail) throw new ConnectError("history temporarily unavailable", Code.Unavailable);
				const latest = request.endTime?.seconds === BigInt(resumed / 1000);
				return { entries: [{ body: latest ? "pause gap" : "history", timestamp: msToTimestamp(latest ? resumed : target) }],
					nextCursor: "older-history" };
			} });
		});
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const historyOptions = logsQueryOptions(transport, { ...logOptions,
			startTime: msToTimestamp(target - 60 * 60_000), endTime: msToTimestamp(target) });
		try {
			const history = await client.query(historyOptions);
			const snapshot = visibleLogRows(toRows(history.entries, new Map()), []);
			fail = true;
			const latestOptions = latestLogsQueryOptions(transport, logOptions, resumed);
			expect(await client.query(latestOptions).catch(String)).toContain("history temporarily unavailable");
			expect(visibleLogRows([], [], snapshot).map((row) => row.entry.body)).toEqual(["history"]);
			expect(client.getQueryData<typeof history>(historyOptions.queryKey)).toEqual(history);
			fail = false;
			const latest = await client.query(latestOptions);
			const batch = toRows(latest.entries, new Map());
			expect(visibleLogRows(batch, batch).map((row) => row.entry.body)).toEqual(["pause gap"]);
			expect(latest.olderCursor).toBe("older-history");
		} finally {
			client.clear();
		}
	});

	test("keeps the two focus ranges disjoint at an exact second", async () => {
		const received: QueryLogsRequest[] = [];
		const transport = createRouterTransport((router) => {
			router.service(ObservabilityProxyService, { queryLogs(request) { received.push(request); return {}; } });
		});
		const client = new QueryClient();
		const target = Date.UTC(2026, 9, 16, 6, 8, 29);
		try {
			await client.query(logsQueryOptions(transport, { ...logOptions, around: target }));
			expect(received[0]?.endTime?.seconds).toBe(BigInt(target / 1000 - 1));
			expect(received[0]?.endTime?.nanos).toBe(999_999_999);
			expect(received[1]?.startTime?.seconds).toBe(BigInt(target / 1000));
			expect(received[1]?.startTime?.nanos).toBe(0);
		} finally {
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
		const input = { workspaceId, environmentId: workspaceId, resourceIds: [], metricName: "k8s.pod.cpu.usage", timeRange: "1h" as const };
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		const east = metricsQueryOptions(transport, { ...input, region: "us-east-1" });
		const west = metricsQueryOptions(transport, { ...input, region: "eu-west-1" });
		expect(east.enabled).toBe(true);
		expect(east.queryKey).not.toEqual(west.queryKey);
		expect(east.queryKey).not.toEqual(metricsQueryOptions(transport, { ...input, region: "us-east-1", environmentId: "22222222-3333-4444-5555-666666666666" }).queryKey);
		await client.query(east);
		await client.query(west);
		expect(received.map((request) => request.region)).toEqual(["us-east-1", "eu-west-1"]);
		expect(received.map((request) => request.environmentId)).toEqual([workspaceId, workspaceId]);
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
