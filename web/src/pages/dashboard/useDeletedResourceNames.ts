import { createClient } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { useQuery } from "@tanstack/react-query";
import { listEvents } from "@gen/loco/event/v1/event-EventService_connectquery";
import { EventService, type Event } from "@gen/loco/event/v1/event_pb";
import { Scope } from "@gen/loco/token/v1/token_pb";

import { scopeLevel, useMyScopes } from "@/hooks/useMyScopes";

const DELETIONS = "@type:resource.deleted";
const PAGE_SIZE = 200;
const MAX_DELETIONS = 500;

/** The names the audit log recorded for the workspace's newest deleted resources, for callers who may read it. */
export function useDeletedResourceNames(orgId: string, workspaceId: string, enabled: boolean): Map<string, string> {
	const admin = useMyScopes().workspaceLevel(orgId, workspaceId) >= scopeLevel(Scope.ADMIN);
	const transport = useTransport();
	const input = { orgId, workspaceId, query: DELETIONS, pageSize: PAGE_SIZE };
	const { data } = useQuery({
		queryKey: [...createConnectQueryKey({ schema: listEvents, input, transport, cardinality: "finite" }), "deletions"],
		queryFn: async ({ signal }) => {
			const client = createClient(EventService, transport);
			const events: Event[] = [];
			let pageToken = "";
			do {
				const page = await client.listEvents({ ...input, pageToken }, { signal });
				events.push(...page.events);
				pageToken = page.nextPageToken;
			} while (pageToken !== "" && events.length < MAX_DELETIONS);
			return events.slice(0, MAX_DELETIONS);
		},
		enabled: enabled && admin,
	});
	const names = new Map<string, string>();
	for (const event of data ?? []) {
		const name = event.data?.["name"];
		if (typeof name === "string") names.set(event.subjectId, name);
	}
	return names;
}
