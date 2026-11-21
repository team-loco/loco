import { useQuery } from "@connectrpc/connect-query";
import { listEvents } from "@gen/loco/event/v1/event-EventService_connectquery";
import { keepPreviousData } from "@tanstack/react-query";

import { useOrgWorkspace } from "@/context/ContextProvider";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useHeldError } from "@/hooks/useHeldError";
import { runtimeEvent, type RuntimeEvent } from "@/lib/runtime-events";
import { msToTimestamp } from "@/lib/time";

// The most events one list shows; the API's largest page.
const EVENTS_PAGE = 200;
// The longest query ListEvents accepts, in bytes.
const MAX_QUERY_LENGTH = 4096;

// A resource filter too long for the server is left to the `names` check on the rows.
function resourceQuery(slugs: string[] | undefined): string {
	const query = slugs === undefined ? "" : `(${slugs.map((slug) => `@resource:${slug}`).join(" OR ")})`;
	return query.length > MAX_QUERY_LENGTH ? "" : query;
}

export interface WorkspaceEventWithResource extends RuntimeEvent {
	resourceId: string;
	resourceName: string;
}

/** The newest events of `types` in the active environment for the resources in `names`, from one ListEvents. */
export function useWorkspaceEvents(
	workspaceId: string,
	names: ReadonlyMap<string, string>,
	{ types, slugs, startMs, endMs }: { types: string[]; slugs?: string[]; startMs?: number; endMs?: number },
) {
	const { activeOrgId } = useOrgWorkspace();
	const { active } = useEnvironments();
	const query = useQuery(
		listEvents,
		{
			orgId: activeOrgId ?? "",
			workspaceId,
			environmentId: active?.id ?? "",
			types,
			query: resourceQuery(slugs),
			readableResources: true,
			pageSize: EVENTS_PAGE,
			...(startMs === undefined ? {} : { startTime: msToTimestamp(startMs) }),
			...(endMs === undefined ? {} : { endTime: msToTimestamp(endMs) }),
		},
		{
			enabled:
				activeOrgId !== null && workspaceId !== "" && active !== undefined && (slugs === undefined || slugs.length > 0),
			placeholderData: keepPreviousData,
		},
	);
	const events: WorkspaceEventWithResource[] = (query.data?.events ?? [])
		.filter((event) => names.has(event.resourceId))
		.map((event) => ({
			...runtimeEvent(event),
			resourceId: event.resourceId,
			resourceName: names.get(event.resourceId) ?? "",
		}));
	const error = useHeldError(query.error, query.isFetching);
	return { events, isLoading: error === null && query.isLoading, error };
}
