import { useMemo } from "react";
import { useQuery } from "@connectrpc/connect-query";
import { listWorkspaceResources, listResourceEvents } from "@/gen/resource/v1";
import type { Event } from "@/gen/resource/v1/resource_pb";
import type { Timestamp } from "@bufbuild/protobuf/wkt";

export interface WorkspaceEventWithResource extends Event {
	id: string;
	resourceId: bigint;
	resourceName: string;
}

function getTimestampMs(timestamp: Timestamp | undefined): number {
	if (!timestamp) return 0;
	const seconds = typeof timestamp.seconds === "bigint" 
		? Number(timestamp.seconds) 
		: timestamp.seconds || 0;
	const nanos = timestamp.nanos || 0;
	return seconds * 1000 + Math.floor(nanos / 1000000);
}

export function useWorkspaceEvents(workspaceId: string) {
	const { data: resourcesData, isLoading: resourcesLoading } = useQuery(
		listWorkspaceResources,
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
		listResourceEvents,
		firstResource ? { resourceId: firstResource.id, limit: 100 } : undefined,
		{
			enabled: !!firstResource,
		}
	);

	const { events, isLoading } = useMemo(() => {
		const allEvents: WorkspaceEventWithResource[] = [];

		if (eventsData?.events && firstResource) {
			eventsData.events.forEach((event, idx) => {
				allEvents.push({
					...event,
					id: `${firstResource.id}-${idx}`,
					resourceId: firstResource.id,
					resourceName: firstResource.name,
				});
			});
		}

		// Sort by timestamp descending (newest first)
		allEvents.sort((a, b) => {
			const timeA = getTimestampMs(a.timestamp);
			const timeB = getTimestampMs(b.timestamp);
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
