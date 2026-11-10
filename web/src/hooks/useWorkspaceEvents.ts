import { useTransport } from "@connectrpc/connect-query";
import { useQueries, useQuery } from "@tanstack/react-query";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useEnvironments } from "@/hooks/useEnvironment";
import { runtimeEvent, type RuntimeEvent } from "@/lib/runtime-events";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";

import { runtimeEventsOptions } from "@/hooks/runtimeEvents";
import { workspaceResourcesOptions } from "@/hooks/workspaceResources";
import { tsMs } from "@/lib/time";

export interface WorkspaceEventWithResource extends RuntimeEvent {
	id: string;
	resourceId: string;
	resourceName: string;
}

export function useWorkspaceEvents(workspaceId: string, resourceFilter?: Resource[], pollMs?: number) {
	const transport = useTransport();
	const { activeOrgId } = useOrgWorkspace();
	const { active } = useEnvironments();
	const { data: resourcesData, isLoading: resourcesLoading } = useQuery({
		...workspaceResourcesOptions(transport, workspaceId),
		enabled: !!workspaceId && resourceFilter === undefined,
	});

	const resources = resourceFilter ?? resourcesData?.resources ?? [];

	const queries = useQueries({
		queries: resources.map((resource) => ({
			...runtimeEventsOptions(transport, {
				orgId: activeOrgId ?? "",
				workspaceId,
				environmentId: active?.id ?? "",
				resourceId: resource.id,
			}),
			refetchInterval: pollMs ?? false,
		})),
	});

	const events: WorkspaceEventWithResource[] = [];
	resources.forEach((resource, ri) => {
		const list = queries[ri]?.data?.events ?? [];
		list.forEach((event) => {
			events.push({
				...runtimeEvent(event),
				id: event.id,
				resourceId: resource.id,
				resourceName: resource.name,
			});
		});
	});
	events.sort((a, b) => tsMs(b.timestamp) - tsMs(a.timestamp));

	const error = queries.find((q) => q.error !== null)?.error ?? null;

	return {
		events,
		isLoading: resourcesLoading || queries.some((q) => q.isLoading),
		error,
	};
}
