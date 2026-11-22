import { Code, ConnectError, createClient } from "@connectrpc/connect";
import { createConnectQueryKey, useTransport } from "@connectrpc/connect-query";
import { listDeployments } from "@gen/loco/deployment/v1/deployment-DeploymentService_connectquery";
import { EventService, type Event } from "@gen/loco/event/v1/event_pb";
import { useQueryClient } from "@tanstack/react-query";
import { useEffect, useEffectEvent, useState } from "react";

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
const RETRY_MS = 15_000;

/**
 * Reads `open(cursor)` and hands each batch of events to `onEvents` until `signal` aborts. After the stream
 * ends or fails it waits `retryMs` and resumes from the last cursor it received, so no event arrives twice.
 * A refused stream (permission denied) stops for good.
 */
export async function followEvents(
	open: (cursor: string, signal: AbortSignal) => AsyncIterable<{ events: Event[]; cursor: string }>,
	onEvents: (events: Event[]) => void,
	signal: AbortSignal,
	retryMs = RETRY_MS,
): Promise<void> {
	let cursor = "";
	while (!signal.aborted) {
		try {
			for await (const message of open(cursor, signal)) {
				if (message.cursor !== "") cursor = message.cursor;
				if (message.events.length > 0) onEvents(message.events);
			}
		} catch (error) {
			if (error instanceof ConnectError && error.code === Code.PermissionDenied) return;
		}
		await new Promise<void>((resolve) => {
			const timer = setTimeout(resolve, retryMs);
			signal.addEventListener(
				"abort",
				() => {
					clearTimeout(timer);
					resolve();
				},
				{ once: true },
			);
		});
	}
}

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
					client.streamEvents({ orgId, workspaceId, environmentId, startTime, cursor, query }, { signal }),
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
