import { useEffect, useState } from "react";
import { createClient } from "@connectrpc/connect";
import { EventService, type Event } from "@gen/loco/event/v1/event_pb";
import { eventTypesQuery } from "@/lib/audit-query";
import { msToTimestamp } from "@/lib/time";
import { useTransport } from "@connectrpc/connect-query";
import { keepPreviousData, useQueries, useQuery } from "@tanstack/react-query";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useMyScopes } from "@/hooks/useMyScopes";
import { useEnvironments } from "@/hooks/useEnvironment";
import { ENVIRONMENT_EVENT_TYPES, eventInEnvironment, runtimeEvent, type RuntimeEvent } from "@/lib/runtime-events";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";

import { mergeEvents, runtimeEventsOptions } from "@/hooks/runtimeEvents";
import { useWorkspaceResourcesOptions } from "@/hooks/workspaceResources";
import { tsMs } from "@/lib/time";

export interface WorkspaceEventWithResource extends RuntimeEvent {
	resourceId: string;
	resourceName: string;
}

export function useWorkspaceEvents(
	workspaceId: string,
	resourceFilter?: Resource[],
	live = false,
	startMs?: number,
	endMs?: number,
) {
	const transport = useTransport();
	const { activeOrgId } = useOrgWorkspace();
	const canStream = useMyScopes().resourceGrants(activeOrgId ?? "", workspaceId) === "workspace";
	const { active } = useEnvironments();
	const { data: resourcesData, isLoading: resourcesLoading } = useQuery({
		...useWorkspaceResourcesOptions(activeOrgId ?? "", workspaceId),
		enabled: !!workspaceId && activeOrgId !== null && resourceFilter === undefined,
	});

	const resources = resourceFilter ?? resourcesData?.resources ?? [];

	const environmentId = active?.id ?? "";
	const queries = useQueries({
		queries: resources.map((resource) => ({
			...runtimeEventsOptions(
				transport,
				{ orgId: activeOrgId ?? "", workspaceId, environmentId, resourceId: resource.id },
				{ types: ENVIRONMENT_EVENT_TYPES, startMs, endMs },
			),
			placeholderData: keepPreviousData,
			refetchInterval: live && !canStream ? 10_000 : false,
		})),
	});

	const streamKey = (activeOrgId ?? "") + "/" + workspaceId;
	const [openedAt] = useState(Date.now);
	const [tail, setTail] = useState<{ key: string; events: Event[]; error: Error | null }>({
		key: streamKey,
		events: [],
		error: null,
	});
	if (tail.key !== streamKey) setTail({ key: streamKey, events: [], error: null });
	const [streamAttempt, setStreamAttempt] = useState(0);
	useEffect(() => {
		if (!live || !canStream || activeOrgId === null || workspaceId === "") return;
		const abort = new AbortController();
		const consume = async () => {
			try {
				const client = createClient(EventService, transport);
				for await (const message of client.streamEvents(
					{
						orgId: activeOrgId,
						workspaceId,
						startTime: msToTimestamp(openedAt),
						query: eventTypesQuery(ENVIRONMENT_EVENT_TYPES),
					},
					{ signal: abort.signal },
				)) {
					if (abort.signal.aborted) return;
					setTail((previous) => ({
						key: streamKey,
						events: mergeEvents(previous.key === streamKey ? previous.events : [], message.events),
						error: null,
					}));
				}
			} catch (error) {
				if (!abort.signal.aborted)
					setTail((previous) => ({
						...previous,
						error: error instanceof Error ? error : new Error(String(error)),
					}));
			}
		};
		void consume();
		return () => {
			abort.abort();
		};
	}, [live, canStream, activeOrgId, workspaceId, transport, streamAttempt, openedAt, streamKey]);

	const events: WorkspaceEventWithResource[] = [];
	resources.forEach((resource, ri) => {
		const list = mergeEvents(
			queries[ri]?.data?.events ?? [],
			tail.events.filter((event) => event.resourceId === resource.id),
		);
		list.forEach((event) => {
			const e = runtimeEvent(event);
			if (!eventInEnvironment(e, environmentId)) return;
			events.push({ ...e, resourceId: resource.id, resourceName: resource.slug });
		});
	});
	events.sort((a, b) => tsMs(b.timestamp) - tsMs(a.timestamp));

	const error = queries.find((q) => q.error !== null)?.error ?? null;

	return {
		events,
		isLoading: resourcesLoading || queries.some((q) => q.isLoading),
		error: error ?? (live && canStream ? tail.error : null),
		refetch: async () => {
			setTail((previous) => ({ ...previous, error: null }));
			setStreamAttempt((attempt) => attempt + 1);
			return await Promise.all(queries.map(async (q) => await q.refetch()));
		},
	};
}
