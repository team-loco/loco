import { create } from "@bufbuild/protobuf";
import { createClient, type Transport } from "@connectrpc/connect";
import { createConnectQueryKey } from "@connectrpc/connect-query";
import { queryOptions } from "@tanstack/react-query";
import { listEvents } from "@gen/loco/event/v1/event-EventService_connectquery";
import { EventService, ListEventsResponseSchema, type ListEventsRequest } from "@gen/loco/event/v1/event_pb";
import { collectPages } from "@/lib/collectPages";
import { RUNTIME_EVENT_TYPES } from "@/lib/runtime-events";

type Scope = Pick<ListEventsRequest, "orgId" | "workspaceId" | "resourceId" | "environmentId">;

export function runtimeEventsOptions(transport: Transport, scope: Scope) {
	const input = { ...scope, types: RUNTIME_EVENT_TYPES, pageSize: 500 };
	const key = createConnectQueryKey({ schema: listEvents, input, transport, cardinality: "finite" });
	return queryOptions({
		queryKey: [...key, "all-pages"] as const,
		queryFn: async ({ signal }: { signal: AbortSignal }) => {
			const client = createClient(EventService, transport);
			const events = await collectPages(async (cursor) => {
				const page = await client.listEvents({ ...input, beforeSeq: cursor === "" ? 0n : BigInt(cursor) }, { signal });
				return { items: page.events, nextPageToken: page.nextBeforeSeq === 0n ? "" : page.nextBeforeSeq.toString() };
			});
			return create(ListEventsResponseSchema, { events });
		},
		enabled: scope.orgId !== "" && scope.workspaceId !== "" && scope.environmentId !== "",
	});
}
