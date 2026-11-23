import { useQuery } from "@connectrpc/connect-query";
import { listEvents } from "@gen/loco/event/v1/event-EventService_connectquery";
import { Scope } from "@gen/loco/token/v1/token_pb";

import { scopeLevel, useMyScopes } from "@/hooks/useMyScopes";

const DELETIONS = "@type:resource.deleted";
const PAGE_SIZE = 500;

/** The names the audit log recorded for the workspace's deleted resources, for callers who may read it. */
export function useDeletedResourceNames(orgId: string, workspaceId: string, enabled: boolean): Map<string, string> {
	const admin = useMyScopes().workspaceLevel(orgId, workspaceId) >= scopeLevel(Scope.ADMIN);
	const { data } = useQuery(
		listEvents,
		{ orgId, workspaceId, query: DELETIONS, pageSize: PAGE_SIZE },
		{ enabled: enabled && admin },
	);
	const names = new Map<string, string>();
	for (const event of data?.events ?? []) {
		const name = event.data?.["name"];
		if (typeof name === "string") names.set(event.subjectId, name);
	}
	return names;
}
