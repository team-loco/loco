import { expect, test } from "bun:test";
import { createRouterTransport } from "@connectrpc/connect";
import { QueryClient } from "@tanstack/react-query";
import { create } from "@bufbuild/protobuf";
import { EventSchema, EventService, type ListEventsRequest } from "@gen/loco/event/v1/event_pb";
import { runtimeEventsOptions } from "./runtimeEvents";
import { ENVIRONMENT_EVENT_TYPES, eventInEnvironment, runtimeEvent } from "@/lib/runtime-events";
import { recentWarningCount } from "@/pages/resource/events";
import { msToTimestamp } from "@/lib/time";

test("event history requests use the selected environment and window start", async () => {
	const requests: ListEventsRequest[] = [];
	const transport = createRouterTransport((router) => {
		router.service(EventService, {
			listEvents(request) {
				requests.push(request);
				return {};
			},
		});
	});
	const client = new QueryClient();
	const scope = {
		orgId: "org",
		workspaceId: "workspace",
		resourceId: "resource",
		environmentId: "production",
	};
	try {
		await client.query(
			runtimeEventsOptions(transport, scope, { types: ENVIRONMENT_EVENT_TYPES, startMs: 1_700_000_000_000 }),
		);
		expect(requests.map((r) => [r.environmentId, r.query, Number(r.startTime?.seconds)])).toEqual([
			[
				"production",
				"(@type:resource.degraded OR @type:resource.recovered OR @type:resource.restarted OR @type:deployment.created OR @type:resource.scaled OR @type:build.created OR @type:build.started OR @type:build.succeeded OR @type:build.failed OR @type:build.canceled OR @type:deployment.ready OR @type:deployment.failed OR @type:deployment.crashed)",
				1_700_000_000,
			],
		]);
	} finally {
		client.clear();
	}
});

test("warning counts include later pages with the same environment scope", async () => {
	const requests: ListEventsRequest[] = [];
	const now = Date.now();
	const transport = createRouterTransport((router) => {
		router.service(EventService, {
			listEvents(request) {
				requests.push(request);
				return request.pageToken === ""
					? {
							events: Array.from({ length: 200 }, (_, i) => ({
								id: i.toString(),
								type: "resource.degraded",
								createdAt: msToTimestamp(now),
								data: { reason: "restarting" },
							})),
							nextPageToken: "next",
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
		expect(recentWarningCount(result.events.map(runtimeEvent), now)).toBe(201);
		expect(requests.map((r) => [r.orgId, r.workspaceId, r.resourceId, r.environmentId, r.query, r.pageToken])).toEqual([
			["org", "workspace", "resource", "production", "(@type:resource.degraded OR @type:resource.recovered)", ""],
			["org", "workspace", "resource", "production", "(@type:resource.degraded OR @type:resource.recovered)", "next"],
		]);
	} finally {
		client.clear();
	}
});

test("selected activity follows build origin and excludes missing origins", () => {
	for (const type of ["build.created", "build.started", "build.succeeded", "build.failed", "build.canceled"]) {
		const event = runtimeEvent(create(EventSchema, { type, environmentId: "staging" }));
		expect(eventInEnvironment(event, "staging")).toBe(true);
		expect(eventInEnvironment(event, "production")).toBe(false);
		expect(eventInEnvironment({ ...event, environmentId: "" }, "production")).toBe(false);
	}
});
