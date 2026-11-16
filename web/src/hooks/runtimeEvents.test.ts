import { describe, expect, test } from "bun:test";
import { createRouterTransport } from "@connectrpc/connect";
import { QueryClient } from "@tanstack/react-query";
import { create } from "@bufbuild/protobuf";
import { EventSchema, EventService, type ListEventsRequest } from "@gen/loco/event/v1/event_pb";
import { mergeEvents, runtimeEventsOptions } from "./runtimeEvents";
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
		expect(requests.map((r) => [r.orgId, r.workspaceId, r.resourceId, r.environmentId, r.query, r.beforeSeq])).toEqual([
			["org", "workspace", "resource", "production", "(@type:resource.degraded OR @type:resource.recovered)", 0n],
			["org", "workspace", "resource", "production", "(@type:resource.degraded OR @type:resource.recovered)", 10n],
		]);
	} finally {
		client.clear();
	}
});

describe("streamed event history", () => {
	test("retains notifications received during history loading and deduplicates overlap", () => {
		const older = create(EventSchema, { id: "older", seq: 1n });
		const incoming = create(EventSchema, { id: "incoming", seq: 2n });
		const tail = mergeEvents([], [incoming]);
		expect(mergeEvents([older], tail)).toEqual([older, incoming]);
		expect(mergeEvents([older, incoming], tail)).toEqual([older, incoming]);
		expect(mergeEvents(tail, [incoming])).toEqual([incoming]);
	});
});
