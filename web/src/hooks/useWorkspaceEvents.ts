import { createQueryOptions, useQuery, useTransport } from "@connectrpc/connect-query";
import { useQueries } from "@tanstack/react-query";
import type { Timestamp } from "@bufbuild/protobuf/wkt";
import { listResourceEvents, listWorkspaceResources } from "@gen/loco/resource/v1/resource-ResourceService_connectquery";
import type { Event, Resource } from "@gen/loco/resource/v1/resource_pb";

export interface WorkspaceEventWithResource extends Event {
	id: string;
	resourceId: string;
	resourceName: string;
}

const EVENTS_PER_RESOURCE = 500;

function getTimestampMs(timestamp: Timestamp | undefined): number {
	if (!timestamp) return 0;
	const seconds = Number(timestamp.seconds);
	const nanos = timestamp.nanos || 0;
	return seconds * 1000 + Math.floor(nanos / 1000000);
}

export function useWorkspaceEvents(workspaceId: string, resourceFilter?: Resource[]) {
	const transport = useTransport();
	const { data: resourcesData, isLoading: resourcesLoading } = useQuery(
		listWorkspaceResources,
		workspaceId ? { workspaceId, pageSize: 200 } : undefined,
		{ enabled: !!workspaceId && resourceFilter === undefined },
	);

	const resources = resourceFilter ?? resourcesData?.resources ?? [];

	const queries = useQueries({
		queries: resources.map((resource) =>
			createQueryOptions(
				listResourceEvents,
				{ resourceId: resource.id, limit: EVENTS_PER_RESOURCE },
				{ transport },
			),
		),
	});

	const events: WorkspaceEventWithResource[] = [];
	resources.forEach((resource, ri) => {
		const list = queries[ri]?.data?.events ?? [];
		list.forEach((event, idx) => {
			events.push({
				...event,
				id: `${resource.id}-${idx.toString()}`,
				resourceId: resource.id,
				resourceName: resource.name,
			});
		});
	});
	events.sort((a, b) => getTimestampMs(b.timestamp) - getTimestampMs(a.timestamp));

	const error = queries.find((q) => q.error !== null)?.error ?? null;

	return {
		events,
		isLoading: resourcesLoading || queries.some((q) => q.isLoading),
		error,
	};
}
