import { useInfiniteQuery } from "@connectrpc/connect-query";
import { listEvents } from "@gen/loco/event/v1/event-EventService_connectquery";
import { keepPreviousData } from "@tanstack/react-query";

import { useOrgWorkspace } from "@/context/ContextProvider";
import { useEnvironments } from "@/hooks/useEnvironment";
import { useHeldError } from "@/hooks/useHeldError";
import { runtimeEvent, type RuntimeEvent } from "@/lib/runtime-events";
import { msToTimestamp } from "@/lib/time";

// The events one page holds; the API's largest page.
const EVENTS_PAGE = 200;

export interface WorkspaceEventWithResource extends RuntimeEvent {
	resourceId: string;
	resourceName: string;
}

/** Events of `types` in the active environment, newest first, a page at a time, from the resources the caller reads. */
export function useWorkspaceEvents(
	workspaceId: string,
	names: ReadonlyMap<string, string>,
	{ types, resourceIds, startMs, endMs }: { types: string[]; resourceIds?: string[]; startMs?: number; endMs?: number },
	enabled = true,
) {
	const { activeOrgId } = useOrgWorkspace();
	const { active } = useEnvironments(enabled);
	const query = useInfiniteQuery(
		listEvents,
		{
			orgId: activeOrgId ?? "",
			workspaceId,
			environmentId: active?.id ?? "",
			types,
			resourceIds: resourceIds ?? [],
			readableResources: true,
			pageSize: EVENTS_PAGE,
			pageToken: "",
			...(startMs === undefined ? {} : { startTime: msToTimestamp(startMs) }),
			...(endMs === undefined ? {} : { endTime: msToTimestamp(endMs) }),
		},
		{
			enabled:
				enabled &&
				activeOrgId !== null &&
				workspaceId !== "" &&
				active !== undefined &&
				(resourceIds === undefined || resourceIds.length > 0),
			placeholderData: keepPreviousData,
			pageParamKey: "pageToken",
			getNextPageParam: (last) => last.nextPageToken || undefined,
		},
	);
	// Events of a deleted resource have no name to show.
	const events: WorkspaceEventWithResource[] = (query.data?.pages ?? [])
		.flatMap((page) => page.events)
		.filter((event) => names.has(event.resourceId))
		.map((event) => ({
			...runtimeEvent(event),
			resourceId: event.resourceId,
			resourceName: names.get(event.resourceId) ?? "",
		}));
	const error = useHeldError(query.isFetchNextPageError ? null : query.error, query.isFetching);
	return { events, query, error, isLoading: error === null && query.isLoading };
}
