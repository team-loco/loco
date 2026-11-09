import { useTransport } from "@connectrpc/connect-query";
import { useQueries, useQuery } from "@tanstack/react-query";
import type { Event } from "@gen/loco/observability/v1/observability_pb";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";

import { resourceEventsOptions } from "@/hooks/resourceEvents";
import { workspaceResourcesOptions } from "@/hooks/workspaceResources";
import type { TimeWindow } from "@/lib/obs-time";
import { isUnimplemented } from "@/lib/error-handler";
import { tsMs } from "@/lib/time";

export interface WorkspaceEventWithResource extends Event {
	id: string;
	resourceId: string;
	resourceName: string;
}

const EVENTS_PER_RESOURCE = 500;

export function useWorkspaceEvents(
	workspaceId: string,
	window: TimeWindow,
	resourceFilter?: Resource[],
	pollMs?: number,
) {
	const transport = useTransport();
	const { data: resourcesData, isLoading: resourcesLoading } = useQuery({
		...workspaceResourcesOptions(transport, workspaceId),
		enabled: !!workspaceId && resourceFilter === undefined,
	});

	const resources = resourceFilter ?? resourcesData?.resources ?? [];

	const queries = useQueries({
		queries: resources.map((resource) => ({
			...resourceEventsOptions(transport, {
				workspaceId,
				resourceId: resource.id,
				window,
				limit: EVENTS_PER_RESOURCE,
			}),
			refetchInterval: pollMs ?? false,
		})),
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
	events.sort((a, b) => tsMs(b.timestamp) - tsMs(a.timestamp));

	const unavailable = queries.some((q) => isUnimplemented(q.error));
	const error = queries.find((q) => q.error !== null && !isUnimplemented(q.error))?.error ?? null;

	return {
		events,
		isLoading: resourcesLoading || queries.some((q) => q.isLoading),
		error,
		unavailable,
	};
}
