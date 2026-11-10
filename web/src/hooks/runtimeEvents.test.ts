import { expect, test } from "bun:test";
import { createRouterTransport } from "@connectrpc/connect";
import { QueryClient } from "@tanstack/react-query";
import { EventService, type ListEventsRequest } from "@gen/loco/event/v1/event_pb";
import { runtimeEventsOptions } from "./runtimeEvents";
import { runtimeEvent } from "@/lib/runtime-events";
import { recentWarningCount } from "@/pages/resource/events";
import { msToTimestamp } from "@/lib/time";

test("warning counts include later pages with the same environment scope", async () => {
	const requests: ListEventsRequest[] = [];
	const now = Date.now();
	const transport = createRouterTransport((router) => {
		router.service(EventService, {
			listEvents(request) {
				requests.push(request);
				return request.beforeSeq === 0n
					? {
							events: Array.from({ length: 500 }, (_, i) => ({
								id: i.toString(),
								type: "resource.degraded",
								createdAt: msToTimestamp(now),
								data: { reason: "crash_looping" },
							})),
							nextBeforeSeq: 10n,
						}
					: {
							events: [
								{
									id: "last",
									type: "resource.degraded",
									createdAt: msToTimestamp(now),
									data: { reason: "out_of_memory" },
								},
							],
						};
			},
		});
	});
	const client = new QueryClient();
	try {
		const result = await client.query(
			runtimeEventsOptions(transport, {
				orgId: "org",
				workspaceId: "workspace",
				resourceId: "resource",
				environmentId: "production",
			}),
		);
		expect(recentWarningCount(result.events.map(runtimeEvent), now)).toBe(501);
		expect(requests.map((r) => [r.orgId, r.workspaceId, r.resourceId, r.environmentId, r.beforeSeq])).toEqual([
			["org", "workspace", "resource", "production", 0n],
			["org", "workspace", "resource", "production", 10n],
		]);
	} finally {
		client.clear();
	}
});
