import { useQuery } from "@tanstack/react-query";
import type { Deployment } from "@gen/loco/deployment/v1/deployment_pb";
import type { KubernetesEvent } from "@gen/loco/observability/v1/observability_pb";

import { useObsAccess } from "@/hooks/useObsAccess";

import { eventProxyUrls, eventsKey, eventsRequest, mergeEvents, type EventsTarget } from "./events";
import { proxyClient } from "./proxy";

const EVENTS_REFRESH_MS = 30_000;

export function useResourceEvents(target: EventsTarget, deployments: Deployment[]) {
	const { data: access, isLoading: accessLoading } = useObsAccess(target.workspaceId);
	const proxyUrls = eventProxyUrls(access?.clusters ?? [], deployments);

	const query = useQuery({
		queryKey: eventsKey(target, proxyUrls),
		queryFn: async (): Promise<KubernetesEvent[]> => {
			const request = eventsRequest(target, Date.now());
			const perProxy = await Promise.all(
				proxyUrls.map(async (url) => {
					const resp = await proxyClient(url).queryEvents(request);
					return resp.events;
				}),
			);
			return mergeEvents(perProxy);
		},
		enabled: proxyUrls.length > 0 && target.workspaceId !== "" && target.environmentId !== "",
		refetchInterval: EVENTS_REFRESH_MS,
	});

	return {
		events: query.data ?? [],
		isLoading: accessLoading || query.isLoading,
		unavailable: !accessLoading && proxyUrls.length === 0,
		error: query.error,
	};
}
