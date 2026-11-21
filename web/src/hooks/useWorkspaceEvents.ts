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

export interface WorkspaceEventWithResource extends RuntimeEvent {
	resourceId: string;
	resourceName: string;
}

/** The newest events of `types` in the active environment, from one ListEvents over the resources the caller reads. */
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
			query: slugs === undefined ? "" : `(${slugs.map((slug) => `@resource:${slug}`).join(" OR ")})`,
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
	const events: WorkspaceEventWithResource[] = (query.data?.events ?? []).map((event) => ({
		...runtimeEvent(event),
		resourceId: event.resourceId,
		resourceName: names.get(event.resourceId) ?? "",
	}));
	const error = useHeldError(query.error, query.isFetching);
	return { events, isLoading: error === null && query.isLoading, error };
}
