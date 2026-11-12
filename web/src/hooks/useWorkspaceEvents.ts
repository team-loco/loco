import { useTransport } from "@connectrpc/connect-query";
import { useQueries, useQuery } from "@tanstack/react-query";
import { useOrgWorkspace } from "@/context/ContextProvider";
import { useEnvironments } from "@/hooks/useEnvironment";
import { ENVIRONMENT_EVENT_TYPES, ROLLOUT_EVENT_TYPES, runtimeEvent, type RuntimeEvent } from "@/lib/runtime-events";
import type { Resource } from "@gen/loco/resource/v1/resource_pb";

import { runtimeEventsOptions } from "@/hooks/runtimeEvents";
import { useWorkspaceResourcesOptions } from "@/hooks/workspaceResources";
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
		...useWorkspaceResourcesOptions(activeOrgId ?? "", workspaceId),
		enabled: !!workspaceId && activeOrgId !== null && resourceFilter === undefined,
	});

	const resources = resourceFilter ?? resourcesData?.resources ?? [];

	const environmentId = active?.id ?? "";
	const kinds = [
		{ types: ENVIRONMENT_EVENT_TYPES, anyEnvironment: false },
		{ types: ROLLOUT_EVENT_TYPES, anyEnvironment: true },
	];
	const queries = useQueries({
		queries: resources.flatMap((resource) =>
			kinds.map((kind) => ({
				...runtimeEventsOptions(
					transport,
					{ orgId: activeOrgId ?? "", workspaceId, environmentId, resourceId: resource.id },
					kind,
				),
				refetchInterval: pollMs ?? false,
			})),
		),
	});

	const events: WorkspaceEventWithResource[] = [];
	resources.forEach((resource, ri) => {
		const list = kinds.flatMap((_, ki) => queries[ri * kinds.length + ki]?.data?.events ?? []);
		list.forEach((event) => {
			const e = runtimeEvent(event);
			// Builds belong to no environment; deploys and scaling name theirs only in their data.
			if (e.environmentId !== "" && e.environmentId !== environmentId) return;
			events.push({ ...e, id: event.id, resourceId: resource.id, resourceName: resource.name });
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
