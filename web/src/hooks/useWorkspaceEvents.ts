import { useMemo } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { listResources, getEvents } from "@/gen/resource/v1";
import type { Event } from "@/gen/resource/v1/resource_pb";

export interface WorkspaceEventWithResource extends Event {
	resourceId: bigint;
	resourceName: string;
}

export function useWorkspaceEvents(workspaceId: string) {
	const { data: resourcesData, isLoading: resourcesLoading } = useQuery(
		listResources,
		workspaceId ? { workspaceId: BigInt(workspaceId) } : undefined,
		{
			enabled: !!workspaceId,
		}
	);

	const resources = resourcesData?.resources || [];
	const firstResource = resources[0];

	// For now, just fetch events for the first resource
	// TODO: Implement workspace-level events endpoint on backend
	const { data: eventsData, isLoading: eventsLoading } = useQuery(
		getEvents,
		firstResource ? { resourceId: firstResource.id, limit: 100 } : undefined,
		{
			enabled: !!firstResource,
		}
	);

	const { events, isLoading } = useMemo(() => {
		const allEvents: WorkspaceEventWithResource[] = [];

		if (eventsData?.events && firstResource) {
			eventsData.events.forEach((event) => {
				allEvents.push({
					...event,
					resourceId: firstResource.id,
					resourceName: firstResource.name,
				});
			});
		}

		// Sort by timestamp descending (newest first)
		allEvents.sort((a, b) => {
			const timeA = a.timestamp
				? new Date(a.timestamp).getTime()
				: 0;
			const timeB = b.timestamp
				? new Date(b.timestamp).getTime()
				: 0;
			return timeB - timeA;
		});

		return {
			events: allEvents,
			isLoading: resourcesLoading || eventsLoading,
		};
	}, [resourcesLoading, eventsLoading, eventsData, firstResource]);

	return {
		events,
		isLoading,
	};
}
