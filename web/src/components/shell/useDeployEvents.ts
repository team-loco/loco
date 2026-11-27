import { createClient } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { EventService, type Event } from "@gen/loco/event/v1/event_pb";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useEffectEvent, useState } from "react";

import { followEvents } from "@/lib/follow-events";
import { workspaceResourcesKey } from "@/hooks/workspaceResources";
import { eventTypesQuery } from "@/lib/audit-query";
import { msToTimestamp } from "@/lib/time";

const DEPLOY_EVENT_TYPES = [
	"deployment.created",
	"deployment.ready",
	"deployment.failed",
	"deployment.crashed",
	"deployment.canceled",
	"resource.degraded",
];
/**
 * Streams the environment's deployment events from `sinceMs` on while the tab is visible, and hands each batch to
 * `onEvents`. Each batch refetches the deployment lists on screen and turns the result on, so the shell starts
 * reading deployments only once one changes; a new deployment also refetches the resource list, for its name.
 * A tab that becomes visible again streams from then on and refetches the deployments once to catch up.
 */
export function useDeployEvents(
	orgId: string | null,
	workspaceId: string | null,
	environmentId: string | undefined,
	sinceMs: number,
	onEvents: (events: readonly Event[]) => void,
): boolean {
	const transport = useTransport();
	const queryClient = useQueryClient();
	const [heard, setHeard] = useState(false);
	const handle = useEffectEvent(onEvents);
	useEffect(() => {
		if (orgId === null || workspaceId === null || environmentId === undefined) return;
		const client = createClient(EventService, transport);
		const refetch = (events: readonly Event[]) => {
			setHeard(true);
			void queryClient.invalidateQueries({
				queryKey: createConnectQueryKey({ schema: listDeployments, cardinality: "finite" }),
			});
			if (events.some((e) => e.type === "deployment.created"))
				void queryClient.invalidateQueries({ queryKey: workspaceResourcesKey(transport, workspaceId) });
		};
		let abort: AbortController | null = null;
		const open = (fromMs: number) => {
			abort = new AbortController();
			const query = eventTypesQuery(DEPLOY_EVENT_TYPES);
			const startTime = msToTimestamp(fromMs);
			void followEvents(
				(cursor, signal) =>
					client.streamEvents(
						{ orgId, workspaceId, environmentId, startTime, cursor, query, readableResources: true },
						{ signal },
					),
				(events) => {
					handle(events);
					refetch(events);
				},
				abort.signal,
			);
		};
		const close = () => {
			abort?.abort();
			abort = null;
		};
		// A hidden tab holds no stream; on return it streams from then on and refetches what changed meanwhile.
		const onVisibility = () => {
			if (document.hidden) {
				close();
				return;
			}
			if (abort !== null) return;
			open(Date.now());
			refetch([]);
		};
		if (!document.hidden) open(sinceMs);
		document.addEventListener("visibilitychange", onVisibility);
		return () => {
			close();
			document.removeEventListener("visibilitychange", onVisibility);
		};
	}, [orgId, workspaceId, environmentId, sinceMs, transport, queryClient]);
	return heard;
}
